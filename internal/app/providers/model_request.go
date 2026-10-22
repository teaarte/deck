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
	"net/textproto"
	"path/filepath"
	"strings"

	"deck2/internal/files"
	"deck2/internal/gateway"
	"deck2/internal/registry"
)

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

// attached are the files a call sends: the recording of stt, the picture an image model edits, the frames of a
// video as pictures of a message.
type attached struct {
	audio  *files.Ref
	image  []byte
	frames []any
}

// picture reads a picture a call sends, at most 32 MB.
func (s *Service) picture(ctx context.Context, act registry.Actor, file, what string) ([]byte, error) {
	ref, err := s.Resolve(ctx, act.ProjectID, act.ProcessID, file)
	if err != nil {
		return nil, err
	}
	r, n, err := ref.Open()
	if err != nil {
		return nil, err
	}
	defer r.Close()
	if n > 32<<20 {
		return nil, registry.Invalid("deck: %s is %d MB; a picture is at most 32 MB", what, n>>20)
	}
	return io.ReadAll(io.LimitReader(r, n))
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
		var content any = ask(in)
		if att.image != nil {
			url := "data:" + http.DetectContentType(att.image) + ";base64," + base64.StdEncoding.EncodeToString(att.image)
			content = []any{map[string]any{"type": "text", "text": ask(in)}, map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}}
		}
		chat := jsonReq("chat/completions", merged(map[string]any{"model": h.Model, "modalities": []string{"image", "text"},
			"messages": []any{map[string]any{"role": "user", "content": content}}}, options))
		r := jsonReq("images/generations", merged(map[string]any{"model": h.Model, "prompt": ask(in), "n": 1, "response_format": "b64_json"}, options))
		if att.image != nil {
			r = edit(h, ask(in), options, att.image)
		}
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
		body := map[string]any{"model": h.Model, "input": in.Text, "response_format": "mp3"}
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

// edit is a picture with what to change in it, posted as a form the way OpenAI's images/edits takes it.
func edit(h gateway.ModelHop, prompt string, options map[string]any, image []byte) gateway.ModelCall {
	var b bytes.Buffer
	w := multipart.NewWriter(&b)
	ct := http.DetectContentType(image)
	ext, _ := mime.ExtensionsByType(ct)
	name := "image"
	if len(ext) > 0 {
		name += ext[len(ext)-1]
	}
	head := textproto.MIMEHeader{}
	head.Set("Content-Disposition", fmt.Sprintf(`form-data; name="image"; filename=%q`, name))
	head.Set("Content-Type", ct)
	part, _ := w.CreatePart(head)
	_, _ = part.Write(image)
	fields := merged(map[string]any{"model": h.Model, "prompt": prompt, "response_format": "b64_json"}, options)
	delete(fields, "image")
	for k, v := range fields {
		if s, ok := v.(string); ok {
			_ = w.WriteField(k, s)
		} else {
			j, _ := json.Marshal(v)
			_ = w.WriteField(k, string(j))
		}
	}
	_ = w.Close()
	return gateway.ModelCall{Path: "images/edits", Header: http.Header{"Content-Type": {w.FormDataContentType()}}, Body: b.Bytes()}
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
