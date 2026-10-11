package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	"deck2/internal/files"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// modelCallIn is one call of a model of a kind: what a command of type model asks, or a script or agent directly.
type modelCallIn struct {
	Kind    string `enum:"text|image|tts|stt|music|video" desc:"What the model makes: text (by a JSON schema when given), image, tts (speech of text), stt (text of a recording), music or video"`
	Model   string `opt:"" desc:"An @role or the name of a model; the models assigned to the kind when left out"`
	Prompt  string `type:"text" opt:"" desc:"What to do; for tts how to speak"`
	Input   string `type:"json" opt:"" desc:"Data for the prompt, as JSON"`
	Schema  string `type:"json" opt:"" desc:"JSON schema of the text result"`
	Text    string `type:"text" opt:"" desc:"The words tts speaks"`
	File    string `type:"file" opt:"" desc:"The recording stt turns into text"`
	First   string `json:"first_frame" type:"file" opt:"" desc:"The picture a video starts from"`
	Last    string `json:"last_frame" type:"file" opt:"" desc:"The picture a video ends on"`
	Options string `type:"json" opt:"" desc:"Fields deck adds to the request as they are, such as voice or image_config"`
}

// call is what a call of a model names: a kind with its model, or a request of a script through the proxy.
type call struct {
	Connection string `json:"connection"`
	Method     string `json:"method"`
	Path       string `json:"path"`
	Body       string `json:"body"`
	Model      string `json:"model"`
	Kind       string `json:"kind"`
}

func parseCall(input json.RawMessage) (call, error) {
	var in call
	if err := json.Unmarshal(input, &in); err != nil {
		return in, registry.Invalid("deck: %v", err)
	}
	return in, nil
}

// answer reads what a model API answered: a binary body is kept as a file in the results folder of the calling
// process, the rest comes back as its bytes; an answer past 400 is an error.
func (s *Service) answer(act registry.Actor, name string, resp *http.Response, h gateway.ModelHop) (any, []byte, error) {
	defer resp.Body.Close()
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	text := ct == "" || ct == "application/json" || strings.HasSuffix(ct, "+json") || strings.HasPrefix(ct, "text/")
	if resp.StatusCode < 400 && !text {
		file, err := s.keep(act, name, ct, resp.Body)
		return file, nil, err
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, nil, registry.Invalid("deck: %s answered HTTP %d: %s", h.Connection, resp.StatusCode, untrusted.Cut(string(raw), 2000))
	}
	return nil, raw, nil
}

// modelRef is where a call of a model goes: an @role on any connection, or a named model on its own.
func (s *Service) modelRef(ctx context.Context, workspace int64, model, kind string) (gateway.ModelAsk, error) {
	if model == "" {
		return gateway.ModelAsk{Connection: "@", Model: "@" + kind}, nil
	}
	if strings.HasPrefix(model, "@") {
		return gateway.ModelAsk{Connection: "@", Model: model}, nil
	}
	row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: model})
	if err != nil {
		return gateway.ModelAsk{}, registry.NotFound("deck: no model %q; deck.models_list names the models", model)
	}
	return gateway.ModelAsk{Connection: row.Connection.Name, Model: row.Model.Model, Name: row.Model.Name}, nil
}

func (s *Service) modelCall(ctx context.Context, act registry.Actor, in modelCallIn) (any, error) {
	ask, err := s.modelRef(ctx, act.Workspace, in.Model, in.Kind)
	if err != nil {
		return nil, err
	}
	if role, ok := strings.CutPrefix(ask.Model, "@"); ok && role != in.Kind {
		return nil, registry.Invalid("deck: a call of kind %s takes @%s, not %s", in.Kind, in.Kind, ask.Model)
	}
	hops, err := s.Route(ctx, act, ask)
	if err != nil {
		return nil, err
	}
	var spent *provider.Usage
	var finish func(*provider.Usage)
	for i := range hops {
		done := hops[i].Done
		hops[i].Done = func(ctx context.Context, status int, u *provider.Usage) {
			if u == nil && status > 0 && status < 400 {
				u = &provider.Usage{}
			}
			if in.Kind == RoleVideo && status > 0 && status < 400 {
				finish = func(u *provider.Usage) { done(ctx, status, u); spent = u }
				return
			}
			done(ctx, status, u)
			spent = u
		}
	}
	var options map[string]any
	if strings.TrimSpace(in.Options) != "" {
		if err := json.Unmarshal([]byte(in.Options), &options); err != nil {
			return nil, registry.Invalid("deck: options are a JSON object: %v", err)
		}
	}
	var att attached
	if in.Kind == RoleSTT {
		if in.File == "" {
			return nil, registry.Invalid("deck: stt needs the recording in file")
		}
		f, err := s.Resolve(ctx, act.ProjectID, act.ProcessID, in.File)
		if err != nil {
			return nil, err
		}
		att.audio = &f
	}
	if in.Kind == RoleVideo {
		if strings.TrimSpace(in.Prompt) == "" {
			return nil, registry.Invalid("deck: a video needs a prompt")
		}
		if att.frames, err = s.frames(ctx, act, in); err != nil {
			return nil, err
		}
	}
	resp, h, err := gateway.SendHops(ctx, hops, func(h gateway.ModelHop) (gateway.ModelCall, error) {
		return build(in, h, merged(maps.Clone(h.Options), options), att)
	})
	if err != nil {
		return nil, err
	}
	var out any
	if in.Kind == RoleVideo {
		out, err = s.video(ctx, act, h, resp, func(u *provider.Usage) {
			if finish != nil {
				finish(u)
			}
		})
	} else {
		out, err = s.read(act, in, h, resp)
	}
	if file, ok := out.(map[string]any); ok && err == nil && file["file"] != nil {
		file["model"] = h.Name
		if spent != nil && spent.CostUSD != nil {
			file["cost_usd"] = *spent.CostUSD
		}
	}
	return out, err
}

func jsonReq(path string, v any) gateway.ModelCall {
	b, _ := json.Marshal(v)
	return gateway.ModelCall{Path: path, Header: http.Header{"Content-Type": {"application/json"}}, Body: b}
}

// ask is the prompt with its input, the way a model reads a task and its data.
func ask(in modelCallIn) string {
	text := strings.TrimSpace(in.Prompt)
	if data := strings.TrimSpace(in.Input); data != "" && data != "{}" {
		text += "\n\nInput (JSON):\n" + data
	}
	if strings.TrimSpace(in.Schema) != "" {
		text += "\n\nAnswer with JSON only, by this JSON schema:\n" + in.Schema
	}
	return text
}

// merged is a body with the options added; the model stays the one deck picked and priced.
func merged(body map[string]any, options map[string]any) map[string]any {
	if body == nil {
		body = map[string]any{}
	}
	for k, v := range options {
		if k != "model" {
			body[k] = v
		}
	}
	return body
}

// attached are the files a call sends: the recording of stt, the frames of a video as pictures of a message.
type attached struct {
	audio  *files.Ref
	frames []any
}

// build is the request of a call in the OpenAI-compatible protocol, the one deck asks models in.
func build(in modelCallIn, h gateway.ModelHop, options map[string]any, att attached) (gateway.ModelCall, error) {
	if api := h.Target.API; api == "google" || api == "anthropic" {
		return gateway.ModelCall{}, registry.Invalid("deck: connection %s speaks the %s API; deck asks models in the OpenAI-compatible protocol, so give the connection the OpenAI-compatible address of %s, or call its own API from a script through $DECK_MODELS", h.Connection, api, api)
	}
	switch in.Kind {
	case RoleText:
		body := map[string]any{"model": h.Model, "messages": []any{map[string]any{"role": "user", "content": ask(in)}}}
		if strings.TrimSpace(in.Schema) != "" {
			var schema any
			if err := json.Unmarshal([]byte(in.Schema), &schema); err != nil {
				return gateway.ModelCall{}, registry.Invalid("deck: schema is JSON: %v", err)
			}
			body["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "result", "schema": schema}}
		}
		return jsonReq("chat/completions", merged(body, options)), nil
	case RoleImage:
		chat := jsonReq("chat/completions", merged(map[string]any{"model": h.Model, "modalities": []string{"image", "text"},
			"messages": []any{map[string]any{"role": "user", "content": ask(in)}}}, options))
		r := jsonReq("images/generations", merged(map[string]any{"model": h.Model, "prompt": ask(in), "n": 1, "response_format": "b64_json"}, options))
		r.Retry = &chat
		return r, nil
	case RoleMusic:
		chat := jsonReq("chat/completions", merged(map[string]any{"model": h.Model, "modalities": []string{"audio", "text"}, "stream": true,
			"messages": []any{map[string]any{"role": "user", "content": ask(in)}}}, options))
		r := jsonReq("audio/music", merged(map[string]any{"model": h.Model, "input": ask(in)}, options))
		r.Retry = &chat
		return r, nil
	case RoleTTS:
		if strings.TrimSpace(in.Text) == "" {
			return gateway.ModelCall{}, registry.Invalid("deck: tts needs the words in text")
		}
		body := map[string]any{"model": h.Model, "input": in.Text, "voice": "alloy"}
		if p := strings.TrimSpace(in.Prompt); p != "" {
			body["instructions"] = p
		}
		return jsonReq("audio/speech", merged(body, options)), nil
	case RoleSTT:
		return transcription(h, options, att.audio)
	case RoleVideo:
		body := map[string]any{"model": h.Model, "prompt": ask(in)}
		if len(att.frames) > 0 {
			body["frame_images"] = att.frames
		}
		return jsonReq("videos", merged(body, options)), nil
	}
	return gateway.ModelCall{}, registry.Invalid("deck: no kind %q", in.Kind)
}

// transcription is a recording posted as a form the way OpenAI's transcriptions and whisper.cpp's server take it;
// a raw connection is the full address of the service.
func transcription(h gateway.ModelHop, options map[string]any, audio *files.Ref) (gateway.ModelCall, error) {
	f, n, err := audio.Open()
	if err != nil {
		return gateway.ModelCall{}, err
	}
	defer f.Close()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	part, err := w.CreateFormFile("file", filepath.Base(audio.Name))
	if err != nil {
		return gateway.ModelCall{}, err
	}
	if _, err := io.Copy(part, io.LimitReader(f, n)); err != nil {
		return gateway.ModelCall{}, err
	}
	fields := map[string]any{"response_format": "json"}
	if h.Model != "" {
		fields["model"] = h.Model
	}
	for k, v := range merged(fields, options) {
		_ = w.WriteField(k, fmt.Sprint(v))
	}
	_ = w.Close()
	path := "audio/transcriptions"
	if h.Target.API == "raw" {
		path = ""
	}
	return gateway.ModelCall{Path: path, Header: http.Header{"Content-Type": {w.FormDataContentType()}}, Body: b.Bytes()}, nil
}

// read turns what the API answered into the result of the call.
func (s *Service) read(act registry.Actor, in modelCallIn, h gateway.ModelHop, resp *http.Response) (any, error) {
	file, raw, err := s.answer(act, in.Kind, resp, h)
	if file != nil || err != nil {
		return file, err
	}
	if audio, ok := chatAudio(raw); ok {
		return s.keep(act, in.Kind, http.DetectContentType(audio), bytes.NewReader(audio))
	}
	var v struct {
		Choices []struct {
			Message struct {
				Content any `json:"content"`
				Images  []struct {
					ImageURL struct {
						URL string `json:"url"`
					} `json:"image_url"`
				} `json:"images"`
			} `json:"message"`
		} `json:"choices"`
		Data []struct {
			B64 string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("deck: %s answered what is not JSON: %s", h.Connection, untrusted.Cut(string(raw), 500))
	}
	switch in.Kind {
	case RoleSTT:
		return manifest.DecodeValue(raw)
	case RoleImage:
		for _, d := range v.Data {
			if d.B64 != "" {
				return s.keepBase64(act, "image/png", d.B64)
			}
		}
		for _, c := range v.Choices {
			for _, img := range c.Message.Images {
				if ct, data, ok := dataURL(img.ImageURL.URL); ok {
					return s.keepBase64(act, ct, data)
				}
			}
		}
		return nil, fmt.Errorf("deck: %s answered no picture: %s", h.Connection, untrusted.Cut(string(raw), 500))
	}
	var text strings.Builder
	for _, c := range v.Choices {
		switch x := c.Message.Content.(type) {
		case string:
			text.WriteString(x)
		case []any:
			for _, p := range x {
				if m, ok := p.(map[string]any); ok {
					t, _ := m["text"].(string)
					text.WriteString(t)
				}
			}
		}
	}
	if strings.TrimSpace(in.Schema) == "" {
		return map[string]any{"text": text.String()}, nil
	}
	out, err := manifest.DecodeValue([]byte(unfence(text.String())))
	if err != nil {
		return nil, fmt.Errorf("deck: the model answered what is not JSON: %s", untrusted.Cut(text.String(), 1000))
	}
	return out, nil
}

// chatAudio is the audio a chat model made in the OpenAI protocol: message.audio of an answer, or the pieces of
// delta.audio of a stream.
func chatAudio(raw []byte) ([]byte, bool) {
	var audio []byte
	for _, line := range bytes.Split(raw, []byte("\n")) {
		line = bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(line), []byte("data:")))
		var v struct {
			Choices []struct {
				Message, Delta struct {
					Audio struct {
						Data string `json:"data"`
					} `json:"audio"`
				}
			} `json:"choices"`
		}
		if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &v) != nil {
			continue
		}
		for _, c := range v.Choices {
			for _, piece := range []string{c.Message.Audio.Data, c.Delta.Audio.Data} {
				b, err := base64.StdEncoding.DecodeString(piece)
				if err != nil {
					return nil, false
				}
				audio = append(audio, b...)
			}
		}
	}
	return audio, len(audio) > 0
}

// unfence is the JSON of an answer without the code fence some models put around it.
func unfence(s string) string {
	s = strings.TrimSpace(s)
	if rest, ok := strings.CutPrefix(s, "```"); ok {
		if i := strings.Index(rest, "\n"); i >= 0 {
			rest = rest[i+1:]
		}
		s = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(rest), "```"))
	}
	return s
}

func dataURL(u string) (string, string, bool) {
	rest, ok := strings.CutPrefix(u, "data:")
	if !ok {
		return "", "", false
	}
	meta, data, ok := strings.Cut(rest, ",")
	ct, enc, _ := strings.Cut(meta, ";")
	return ct, data, ok && enc == "base64"
}

func (s *Service) keepBase64(act registry.Actor, ct, data string) (any, error) {
	b, err := base64.StdEncoding.DecodeString(data)
	if err != nil {
		return nil, fmt.Errorf("deck: the picture is not base64: %v", err)
	}
	return s.keep(act, RoleImage, ct, bytes.NewReader(b))
}

// keep saves what a model made as a file in the results folder of the calling process.
func (s *Service) keep(act registry.Actor, kind, ct string, body io.Reader) (any, error) {
	name, n, err := files.Save(s.Home, act.ProcessID, kind, ct, body, 1<<30)
	if err != nil {
		return nil, err
	}
	return map[string]any{"file": name, "type": ct, "size": n}, nil
}

// modelCallCommand is a call of a model of a kind through deck; a script's call through the proxy goes by its rules.
func (s *Service) modelCallCommand() *registry.Command {
	c := registry.Def("model_call", "Ask a model",
		"Asks the model assigned to a kind of work, or one named: text by a JSON schema, a picture, speech of text, text of a recording, music or a video from its first and last frames; pictures, sound and video come back as files.",
		registry.Everyone, s.modelCall)
	c.Class, c.Allowed, c.Free = registry.External, s.allowed, s.free
	c.Fingerprint = func(context.Context, int64) string { return gateway.ModelCommand }
	return c
}
