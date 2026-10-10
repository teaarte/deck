package providers

import (
	"cmp"
	"context"
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"strings"

	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// turnModel is deck's proxy of model m of an agent turn: every call goes to that model with deck's key, and its
// usage, priced, goes to onUsage.
func (s *Service) turnModel(workspace int64, m db.Model, c db.Connection) func(onUsage func(provider.Usage)) http.Handler {
	return func(onUsage func(provider.Usage)) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			body, err := gateway.ReadBody(w, r)
			if err != nil {
				return
			}
			h, err := s.hop(workspace, m, c)
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadGateway)
				return
			}
			h.Done = func(_ context.Context, _ int, u *provider.Usage) {
				if u == nil || onUsage == nil {
					return
				}
				u.RequestModel, u.Connection = m.Model, c.ID
				if u.CostUSD == nil {
					u.CostUSD = price(m, *u)
				}
				onUsage(*u)
			}
			gateway.ServeHops(w, r, r.URL.Path, body, true, []gateway.ModelHop{h})
		})
	}
}

// modelRequestIn is one request of a model API through deck: what the proxy freezes when a call waits for the owner.
type modelRequestIn struct {
	Connection string `desc:"Connection the request goes through; @ with an @role in the body takes the models assigned to it"`
	Path       string `desc:"Path of the request under the API of the connection, such as chat/completions; only a call of a model, unless the connection is raw"`
	Method     string `opt:"" desc:"HTTP method; POST when left out"`
	Body       string `type:"json" opt:"" desc:"JSON body of the request; its model may be an @role such as @text"`
	Model      string `opt:"" desc:"Model of the request as the API knows it, when the body has no field for it"`
}

// call is what a call of a model names: a request through a connection with its body, or a model or kind.
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

func (s *Service) modelRequest(ctx context.Context, act registry.Actor, in modelRequestIn) (any, error) {
	if in.Connection == "" {
		in.Connection = "@"
	}
	method := cmp.Or(strings.ToUpper(in.Method), http.MethodPost)
	if in.Body != "" && !json.Valid([]byte(in.Body)) {
		return nil, registry.Invalid("deck: the body of deck.model_request is JSON; a form goes through $DECK_MODELS")
	}
	ask, err := frozen(call{Connection: in.Connection, Method: method, Path: in.Path, Body: in.Body, Model: in.Model})
	if err != nil {
		return nil, err
	}
	hops, err := s.Route(ctx, act, ask)
	if err != nil {
		return nil, err
	}
	header := http.Header{}
	if in.Body != "" {
		header.Set("Content-Type", "application/json")
	}
	resp, h, err := gateway.SendHops(ctx, hops, func(h gateway.ModelHop) (gateway.ModelCall, error) {
		body := []byte(in.Body)
		if ask.InBody {
			body = gateway.WithModel(body, h.Model)
		}
		return gateway.ModelCall{Method: method, Path: in.Path, Header: header, Body: body}, nil
	})
	if err != nil {
		return nil, err
	}
	file, raw, err := s.answer(act, "model", resp, h)
	if file != nil || err != nil {
		return file, err
	}
	if v, err := manifest.DecodeValue(raw); err == nil && !strings.Contains(resp.Header.Get("Content-Type"), "event-stream") {
		return v, nil
	}
	return map[string]any{"text": string(raw)}, nil
}

// modelRequestCommand is a request of a model API through deck: deck puts in the key and keeps the call in the
// journal.
func (s *Service) modelRequestCommand() *registry.Command {
	c := registry.Def("model_request", "Call a model",
		"Sends one request to the API of a connection with deck's key, such as chat/completions with model @text; the answer comes back as the API gave it, a file for audio or pictures.",
		registry.Everyone, s.modelRequest)
	c.Class, c.Allowed, c.Free = registry.External, s.allowed, s.free
	c.Fingerprint = func(context.Context, int64) string { return gateway.ModelRequest }
	return c
}
