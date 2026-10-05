package providers

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"path/filepath"
	"strings"
	"time"

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
	Kind    string `enum:"text|image|tts|stt" desc:"What the model makes: text (by a JSON schema when given), image, tts (speech of text) or stt (text of a recording)"`
	Model   string `opt:"" desc:"An @role or the name of a model; the models assigned to the kind when left out"`
	Prompt  string `type:"text" opt:"" desc:"What to do; for tts how to speak"`
	Input   string `type:"json" opt:"" desc:"Data for the prompt, as JSON"`
	Schema  string `type:"json" opt:"" desc:"JSON schema of the text result"`
	Text    string `type:"text" opt:"" desc:"The words tts speaks"`
	File    string `type:"file" opt:"" desc:"The recording stt turns into text"`
	Options string `type:"json" opt:"" desc:"Fields deck adds to the request as they are, such as voice or image_config"`
}

// modelRef is where a call of a model goes: an @role on any connection, or a named model on its own.
func (s *Service) modelRef(ctx context.Context, workspace int64, model, kind string) (string, string, error) {
	if model == "" {
		return "@", "@" + kind, nil
	}
	if strings.HasPrefix(model, "@") {
		return "@", model, nil
	}
	row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: model})
	if err != nil {
		return "", "", registry.NotFound("deck: no model %q; deck.models_list names the models", model)
	}
	return row.Connection.Name, row.Model.Model, nil
}

func (s *Service) modelCall(ctx context.Context, act registry.Actor, in modelCallIn) (any, error) {
	connection, asked, err := s.modelRef(ctx, act.Workspace, in.Model, in.Kind)
	if err != nil {
		return nil, err
	}
	if role, ok := strings.CutPrefix(asked, "@"); ok && role != in.Kind {
		return nil, registry.Invalid("deck: a call of kind %s takes @%s, not %s", in.Kind, in.Kind, asked)
	}
	hops, err := s.Route(ctx, act, connection, asked)
	if err != nil {
		return nil, err
	}
	var options map[string]any
	if strings.TrimSpace(in.Options) != "" {
		if err := json.Unmarshal([]byte(in.Options), &options); err != nil {
			return nil, registry.Invalid("deck: options are a JSON object: %v", err)
		}
	}
	var audio *files.Ref
	if in.Kind == RoleSTT {
		if in.File == "" {
			return nil, registry.Invalid("deck: stt needs the recording in file")
		}
		f, err := s.Resolve(ctx, act.ProjectID, act.ProcessID, in.File)
		if err != nil {
			return nil, err
		}
		audio = &f
	}
	_, approved := gateway.Approved(ctx)
	var last error = registry.Invalid("deck: no model to call")
	for i, h := range hops {
		if h.Over != "" && !approved {
			last = registry.Invalid("%s", h.Over)
			continue
		}
		req, err := build(in, h, options, audio)
		if err != nil {
			return nil, err
		}
		start := time.Now()
		resp, err := gateway.SendModel(ctx, h.Target, http.MethodPost, req.path, "", req.header, req.body)
		if err == nil && req.retry != nil && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed) {
			_ = resp.Body.Close()
			req = *req.retry
			resp, err = gateway.SendModel(ctx, h.Target, http.MethodPost, req.path, "", req.header, req.body)
		}
		if err != nil {
			h.Done(ctx, 0, nil)
			last = fmt.Errorf("deck: %s: %v", h.Connection, err)
			continue
		}
		if gateway.Fallible(resp.StatusCode) && i < len(hops)-1 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
			_ = resp.Body.Close()
			h.Done(ctx, resp.StatusCode, nil)
			last = fmt.Errorf("deck: %s answered %s: %s", h.Connection, resp.Status, untrusted.Cut(strings.TrimSpace(string(raw)), 500))
			continue
		}
		return s.read(act, in, h, resp, start)
	}
	return nil, last
}

// modelReq is a request of one kind in the words of one API; retry is the other way to ask the same when the
// API has no such path.
type modelReq struct {
	path   string
	header http.Header
	body   []byte
	retry  *modelReq
}

func jsonReq(path string, v any) modelReq {
	b, _ := json.Marshal(v)
	return modelReq{path: path, header: http.Header{"Content-Type": {"application/json"}}, body: b}
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

func merged(body map[string]any, options map[string]any) map[string]any {
	for k, v := range options {
		body[k] = v
	}
	return body
}

// build is the request of a call for the API of a hop.
func build(in modelCallIn, h gateway.ModelHop, options map[string]any, audio *files.Ref) (modelReq, error) {
	var schema any
	if strings.TrimSpace(in.Schema) != "" {
		if err := json.Unmarshal([]byte(in.Schema), &schema); err != nil {
			return modelReq{}, registry.Invalid("deck: schema is JSON: %v", err)
		}
	}
	api := h.Target.API
	switch {
	case in.Kind == RoleText && api == "google":
		cfg := map[string]any{}
		if schema != nil {
			cfg["responseMimeType"], cfg["responseJsonSchema"] = "application/json", schema
		}
		return jsonReq("models/"+url.PathEscape(h.Model)+":generateContent", merged(map[string]any{
			"contents": []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": ask(in)}}}}, "generationConfig": cfg}, options)), nil
	case in.Kind == RoleImage && api == "google":
		return jsonReq("models/"+url.PathEscape(h.Model)+":generateContent", merged(map[string]any{
			"contents":         []any{map[string]any{"role": "user", "parts": []any{map[string]any{"text": ask(in)}}}},
			"generationConfig": map[string]any{"responseModalities": []string{"TEXT", "IMAGE"}}}, options)), nil
	case in.Kind == RoleText && api == "anthropic":
		return jsonReq("messages", merged(map[string]any{"model": h.Model, "max_tokens": 8192,
			"messages": []any{map[string]any{"role": "user", "content": ask(in)}}}, options)), nil
	case api == "google" || api == "anthropic":
		return modelReq{}, registry.Invalid("deck: connection %s speaks %s, which deck calls for text only; %s goes through an openai-compatible connection or a script", h.Connection, api, in.Kind)
	case in.Kind == RoleText:
		body := map[string]any{"model": h.Model, "messages": []any{map[string]any{"role": "user", "content": ask(in)}}}
		if schema != nil {
			body["response_format"] = map[string]any{"type": "json_schema", "json_schema": map[string]any{"name": "result", "schema": schema}}
		}
		return jsonReq("chat/completions", merged(body, options)), nil
	case in.Kind == RoleImage:
		chat := jsonReq("chat/completions", merged(map[string]any{"model": h.Model, "modalities": []string{"image", "text"},
			"messages": []any{map[string]any{"role": "user", "content": ask(in)}}}, options))
		r := jsonReq("images/generations", merged(map[string]any{"model": h.Model, "prompt": ask(in), "n": 1, "response_format": "b64_json"}, options))
		r.retry = &chat
		return r, nil
	case in.Kind == RoleTTS:
		if strings.TrimSpace(in.Text) == "" {
			return modelReq{}, registry.Invalid("deck: tts needs the words in text")
		}
		body := map[string]any{"model": h.Model, "input": in.Text, "voice": "alloy"}
		if p := strings.TrimSpace(in.Prompt); p != "" {
			body["instructions"] = p
		}
		return jsonReq("audio/speech", merged(body, options)), nil
	case in.Kind == RoleSTT:
		return transcription(h, options, audio)
	}
	return modelReq{}, registry.Invalid("deck: no kind %q", in.Kind)
}

// transcription is a recording posted as a form the way OpenAI's transcriptions and whisper.cpp's server take it;
// a raw connection is the full address of the service.
func transcription(h gateway.ModelHop, options map[string]any, audio *files.Ref) (modelReq, error) {
	f, n, err := audio.Open()
	if err != nil {
		return modelReq{}, err
	}
	defer f.Close()
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	part, err := w.CreateFormFile("file", filepath.Base(audio.Name))
	if err != nil {
		return modelReq{}, err
	}
	if _, err := io.Copy(part, io.LimitReader(f, n)); err != nil {
		return modelReq{}, err
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
	return modelReq{path: path, header: http.Header{"Content-Type": {w.FormDataContentType()}}, body: b.Bytes()}, nil
}

// read turns what the API answered into the result of the call and keeps its usage.
func (s *Service) read(act registry.Actor, in modelCallIn, h gateway.ModelHop, resp *http.Response, start time.Time) (any, error) {
	ctx := context.Background()
	body := gateway.Metered(resp.Body, start, func(u *provider.Usage) { h.Done(ctx, resp.StatusCode, u) })
	defer body.Close()
	ct, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if resp.StatusCode < 400 && (strings.HasPrefix(ct, "audio/") || strings.HasPrefix(ct, "image/") || ct == "application/octet-stream") {
		return s.keep(act, in.Kind, ct, body)
	}
	raw, err := io.ReadAll(io.LimitReader(body, 64<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, registry.Invalid("deck: %s answered HTTP %d: %s", h.Connection, resp.StatusCode, untrusted.Cut(string(raw), 2000))
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
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text   string `json:"text"`
					Inline *struct {
						MimeType string `json:"mimeType"`
						Data     string `json:"data"`
					} `json:"inlineData"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("deck: %s answered what is not JSON: %s", h.Connection, untrusted.Cut(string(raw), 500))
	}
	switch in.Kind {
	case RoleSTT:
		out, err := manifest.DecodeValue(raw)
		if err != nil {
			return nil, err
		}
		return out, nil
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
		for _, c := range v.Candidates {
			for _, p := range c.Content.Parts {
				if p.Inline != nil && p.Inline.Data != "" {
					return s.keepBase64(act, p.Inline.MimeType, p.Inline.Data)
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
	for _, c := range v.Candidates {
		for _, p := range c.Content.Parts {
			text.WriteString(p.Text)
		}
	}
	for _, c := range v.Content {
		if c.Type == "text" {
			text.WriteString(c.Text)
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
	name, n, err := s.Dirs.Save(act.ProcessID, kind, ct, body, 1<<30)
	if err != nil {
		return nil, err
	}
	return map[string]any{"file": name, "type": ct, "size": n}, nil
}

// modelCallCommand is a call of a model of a kind through deck, on the rules of deck.model_request.
func (s *Service) modelCallCommand() *registry.Command {
	c := registry.Def("model_call", "Ask a model",
		"Asks the model assigned to a kind of work, or one named: text by a JSON schema, a picture, speech of text or text of a recording; pictures and speech come back as files.",
		registry.Everyone, s.modelCall)
	c.Class, c.Allowed = registry.External, s.allowed
	c.Fingerprint = func(context.Context, int64) string { return gateway.ModelRequest }
	return c
}
