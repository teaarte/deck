package providers

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"deck2/internal/fault"
	"deck2/internal/files"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/untrusted"
)

// answer reads what a model API answered: a binary body is kept as a file in the results folder of the calling
// process, the rest comes back as its bytes; an answer past 400 is an error, of the model's status when it refused.
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
		text := fmt.Sprintf("deck: %s answered HTTP %d: %s", h.Connection, resp.StatusCode, untrusted.Cut(string(raw), 2000))
		if t := fault.OfStatus(fault.ModelStatus(resp.StatusCode), true); fault.Whose(t) == fault.OfModel {
			return nil, nil, fault.New(t, text)
		}
		return nil, nil, registry.Invalid("%s", text)
	}
	return nil, raw, nil
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

// keep saves what a model made as a file in the results folder of the calling process; the owner's call without a
// process, as the check of a model, gets only its type and size.
func (s *Service) keep(act registry.Actor, kind, ct string, body io.Reader) (any, error) {
	if act.ProcessID == 0 && act.Kind == registry.Owner {
		n, err := io.Copy(io.Discard, io.LimitReader(body, 1<<30))
		return map[string]any{"type": ct, "size": n, "kept": false}, err
	}
	name, n, err := files.Save(s.Home, act.ProcessID, kind, ct, body, 1<<30)
	if err != nil {
		return nil, err
	}
	return map[string]any{"file": name, "type": ct, "size": n}, nil
}
