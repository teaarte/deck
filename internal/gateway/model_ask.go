package gateway

import (
	"bytes"
	"cmp"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"strings"

	"deck2/internal/registry"
)

// ModelPath reports whether a request is a call of a model: a POST of one, or a poll of a job a model started; not
// the account, files or other paths of the service behind a connection.
func ModelPath(method, path string) bool {
	path = strings.TrimPrefix(path, "/")
	if Polls(method, path) {
		return true
	}
	if method != http.MethodPost {
		return false
	}
	for _, p := range []string{"chat/completions", "completions", "responses", "embeddings", "messages", "images/generations",
		"images/edits", "audio/speech", "audio/transcriptions", "audio/translations", "audio/music", "videos"} {
		if path == p {
			return true
		}
	}
	return strings.HasPrefix(path, "models/") && (strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent"))
}

// DeckModel is the header that names the model of a request whose API has no field for it, as the API knows it.
const DeckModel = "X-Deck-Model"

// ModelAsk is a call of a model as deck routes it: the connection, the model the request names and Name of deck's when
// given, InBody, InForm or ByHeader for where the request names it, and the method and path of a request a module made.
type ModelAsk struct {
	Connection, Model, Name  string
	InBody, InForm, ByHeader bool
	Method, Path             string
}

// AskModel is the model a request names: of a JSON body, a form, Google's path, else the header DeckModel; names
// that disagree, and OpenRouter's models and route that reach models past the one named, are refused.
func AskModel(connection, method, path string, header http.Header, body []byte) (ModelAsk, error) {
	ask := ModelAsk{Connection: connection, Method: cmp.Or(method, http.MethodPost), Path: strings.TrimPrefix(path, "/")}
	for i, seg := range strings.Split(ask.Path, "/") {
		if seg == "." || seg == ".." || seg == "" && i > 0 {
			return ask, registry.Refuse("deck: the path %s of a call of a model has an empty, . or .. part", path)
		}
	}
	var named []string
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) == nil {
		for k := range m {
			switch {
			case strings.EqualFold(k, "model") && k != "model":
				return ask, registry.Refuse("deck: a call of a model names its model in model, not %s", k)
			case strings.EqualFold(k, "models") || strings.EqualFold(k, "route"):
				return ask, registry.Refuse("deck: a call of a model names its one model in model; %s reaches models deck's settings do not hold", k)
			}
		}
		if raw, ok := m["model"]; ok {
			var v string
			if json.Unmarshal(raw, &v) != nil || v == "" {
				return ask, registry.Refuse("deck: model of the body is the name of a model, a string")
			}
			named, ask.InBody = append(named, v), true
		}
	}
	if ct, params, err := mime.ParseMediaType(header.Get("Content-Type")); err == nil && ct == "multipart/form-data" {
		r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		var form []string
		for {
			p, err := r.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return ask, registry.Refuse("deck: the form of a call of a model does not read: %v", err)
			}
			if p.FormName() == "model" {
				b, _ := io.ReadAll(io.LimitReader(p, 1<<10))
				form = append(form, strings.TrimSpace(string(b)))
			}
		}
		if len(form) > 1 {
			return ask, registry.Refuse("deck: a form names its model once")
		}
		named, ask.InForm = append(named, form...), len(form) > 0
	}
	if rest, ok := strings.CutPrefix(ask.Path, "models/"); ok {
		if model, _, ok := strings.Cut(rest, ":"); ok {
			named = append(named, model)
		}
	}
	if h := header.Get(DeckModel); h != "" {
		if len(named) == 0 {
			ask.Model, ask.ByHeader = h, true
			return ask, nil
		}
		named = append(named, h)
	}
	for _, n := range named {
		if n != named[0] {
			return ask, registry.Refuse("deck: the request names two models, %s and %s", named[0], n)
		}
	}
	if len(named) > 0 {
		ask.Model = named[0]
	}
	return ask, nil
}

// Polls reports whether a request asks after a job of a model it started, such as a video: it names no model.
func Polls(method, path string) bool {
	rest, ok := strings.CutPrefix(strings.TrimPrefix(path, "/"), "videos/")
	id, tail, _ := strings.Cut(rest, "/")
	return ok && method == http.MethodGet && id != "" && (tail == "" || tail == "content")
}

// WithModel is a JSON body that names model and only it, the rest as it came: other spellings of model and
// OpenRouter's models and route go.
func WithModel(body []byte, model string) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return body
	}
	for k := range m {
		if strings.EqualFold(k, "model") || strings.EqualFold(k, "models") || strings.EqualFold(k, "route") {
			delete(m, k)
		}
	}
	m["model"], _ = json.Marshal(model)
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}
