package gateway

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/untrusted"
)

// ModelTarget is the API of a connection: where it is, the family it speaks (openai, google, anthropic, raw) and its key.
type ModelTarget struct {
	URL string
	API string
	Key string
}

// ModelHop is one place a call of a model may go, in the order deck tries them.
type ModelHop struct {
	Target ModelTarget
	// Connection names the target, Name the model in deck's settings; Model is the name the API knows, which
	// replaces an @role the caller asked for.
	Connection, Name string
	Model            string
	// Local is a connection on the owner's hardware; Free is a model that costs nothing.
	Local, Free bool
	// Options are the fields a call of a kind adds to the request of this model, such as language.
	Options map[string]any
	// Done keeps the call: the status the API answered and what it used, nil when it did not say.
	Done func(ctx context.Context, status int, u *provider.Usage)
}

// ModelRoute finds where a call of a model goes for an actor: the model of deck's settings the request names on the
// connection; an @role names the models assigned to it, the fallbacks after the first.
type ModelRoute func(ctx context.Context, act registry.Actor, ask ModelAsk) ([]ModelHop, error)

// ModelRequest is the command a call of a model through the proxy is frozen as when it waits for the owner.
const ModelRequest = registry.Builtin + ".model_request"

var modelClient = &http.Client{
	Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		ResponseHeaderTimeout: 5 * time.Minute, ForceAttemptHTTP2: true},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// dropped are headers of the caller deck does not pass on: its own keys, cookies and how the body is packed.
var dropped = []string{DeckModel, "Authorization", "X-Api-Key", "X-Goog-Api-Key", "Cookie", "Proxy-Authorization", "Accept-Encoding",
	"Connection", "Keep-Alive", "Te", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Length", "Host"}

// ModelCall is one request of a model API; Retry is the other way to ask the same when the API has no such path.
type ModelCall struct {
	Method, Path, Query string
	Header              http.Header
	Body                []byte
	Retry               *ModelCall
}

// SendModel makes one request under the API of t with deck's key in place of any the caller sent.
func SendModel(ctx context.Context, t ModelTarget, c ModelCall) (*http.Response, error) {
	u, err := url.Parse(t.URL)
	if err != nil {
		return nil, err
	}
	if path := strings.TrimPrefix(c.Path, "/"); path != "" {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/" + path
		u.RawPath = ""
	}
	q, _ := url.ParseQuery(c.Query)
	q.Del("key")
	for k, vs := range u.Query() {
		q[k] = vs
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, cmp.Or(c.Method, http.MethodPost), u.String(), bytes.NewReader(c.Body))
	if err != nil {
		return nil, err
	}
	for k, vs := range c.Header {
		req.Header[k] = vs
	}
	for _, h := range dropped {
		req.Header.Del(h)
	}
	if t.Key != "" {
		switch t.API {
		case "google":
			req.Header.Set("X-Goog-Api-Key", t.Key)
		case "anthropic":
			req.Header.Set("X-Api-Key", t.Key)
			if req.Header.Get("Anthropic-Version") == "" {
				req.Header.Set("Anthropic-Version", "2023-06-01")
			}
		default:
			req.Header.Set("Authorization", "Bearer "+t.Key)
		}
	}
	return modelClient.Do(req)
}

// SendHops makes the call each hop builds, in order, past one unsent, a 429 or a 5xx; the usage of the answer
// reaches the Done of its hop when its body closes.
func SendHops(ctx context.Context, hops []ModelHop, build func(ModelHop) (ModelCall, error)) (*http.Response, ModelHop, error) {
	var last error = registry.Invalid("deck: no model to call")
	for i, h := range hops {
		c, err := build(h)
		if err != nil {
			return nil, h, err
		}
		start := time.Now()
		resp, err := SendModel(ctx, h.Target, c)
		if err == nil && c.Retry != nil && (resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed) {
			_ = resp.Body.Close()
			resp, err = SendModel(ctx, h.Target, *c.Retry)
		}
		if err != nil {
			h.Done(ctx, 0, nil)
			last = &registry.Error{Status: http.StatusBadGateway, Text: fmt.Sprintf("deck: %s: %v", h.Connection, err)}
			if Unsent(err) {
				continue
			}
			return nil, h, last
		}
		if Fallible(resp.StatusCode) && i < len(hops)-1 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
			_ = resp.Body.Close()
			h.Done(ctx, resp.StatusCode, nil)
			last = &registry.Error{Status: http.StatusBadGateway,
				Text: fmt.Sprintf("deck: %s answered %s: %s", h.Connection, resp.Status, untrusted.Cut(strings.TrimSpace(string(raw)), 500))}
			continue
		}
		done, status := context.WithoutCancel(ctx), resp.StatusCode
		resp.Body = metered(resp.Body, start, func(u *provider.Usage) { h.Done(done, status, u) })
		return resp, h, nil
	}
	return nil, ModelHop{}, last
}

// Unsent reports whether a failed request never reached the API, so a fallback may take it: only a failed dial
// proves that; a timeout or a cut answer may have been billed.
func Unsent(err error) bool {
	var op *net.OpError
	return errors.As(err, &op) && op.Op == "dial"
}

// RolePath reports whether a request is one an @role may make: a POST of a model call, not the account, files or
// other paths of the connection behind the role.
func RolePath(method, path string) bool {
	path = strings.TrimPrefix(path, "/")
	if method != http.MethodPost {
		return false
	}
	for _, p := range []string{"chat/completions", "completions", "responses", "embeddings", "messages", "images/generations",
		"images/edits", "audio/speech", "audio/transcriptions", "audio/translations"} {
		if path == p {
			return true
		}
	}
	return strings.HasPrefix(path, "models/") && (strings.HasSuffix(path, ":generateContent") || strings.HasSuffix(path, ":streamGenerateContent"))
}

// Fallible reports whether a hop's answer is an explicit refusal a fallback may take over: a limit or a fault of
// the service, not an answer about the request.
func Fallible(status int) bool { return status == http.StatusTooManyRequests || status >= 500 }

// metered passes an answer of a model API through and calls done with the usage it reports once it is read.
func metered(body io.ReadCloser, start time.Time, done func(*provider.Usage)) io.ReadCloser {
	return &meter{body: body, done: func(model string, u *apiUsage) {
		if u == nil {
			done(nil)
			return
		}
		pu := u.usage(model, time.Since(start))
		done(&pu)
	}}
}

// ServeHops serves one call of a model through the hops and passes the answer on as it comes; replace puts the
// model of each hop in the body.
func ServeHops(w http.ResponseWriter, r *http.Request, path string, body []byte, replace bool, hops []ModelHop) {
	resp, _, err := SendHops(r.Context(), hops, func(h ModelHop) (ModelCall, error) {
		b := body
		if replace {
			b = WithModel(body, h.Model)
		}
		return ModelCall{Method: r.Method, Path: path, Query: r.URL.RawQuery, Header: r.Header, Body: b}, nil
	})
	if err != nil {
		http.Error(w, err.Error(), registry.Status(err))
		return
	}
	defer resp.Body.Close()
	for k, vs := range resp.Header {
		if k != "Content-Length" && k != "Connection" && k != "Transfer-Encoding" {
			w.Header()[k] = vs
		}
	}
	w.WriteHeader(resp.StatusCode)
	flush(w, resp.Body)
}

// flush copies an answer as it comes, so a stream of events reaches the caller event by event.
func flush(w http.ResponseWriter, body io.Reader) {
	f, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if f != nil {
				f.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// DeckModel is the header that names the model of a request whose API has no field for it, as the API knows it.
const DeckModel = "X-Deck-Model"

// ModelAsk is a call of a model as deck routes it: the connection, the model the request names, ByHeader when only
// the header DeckModel names it, and the method and path of a request through a connection.
type ModelAsk struct {
	Connection, Model string
	ByHeader          bool
	Method, Path      string
}

// AskModel is what a request through a connection asks: the model of a JSON body or of a form, the one in a path
// of Google's API, else the header DeckModel. Names that disagree, or OpenRouter's models and route that reach
// models past the one named, are refused.
func AskModel(connection, method, path string, header http.Header, body []byte) (ModelAsk, error) {
	ask := ModelAsk{Connection: connection, Method: cmp.Or(method, http.MethodPost), Path: strings.TrimPrefix(path, "/")}
	var named []string
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) == nil {
		for _, k := range []string{"models", "route"} {
			if _, ok := m[k]; ok {
				return ask, registry.Refuse("deck: a call of a model names its one model in model; %s reaches models deck's settings do not hold", k)
			}
		}
		if raw, ok := m["model"]; ok {
			var v string
			if json.Unmarshal(raw, &v) != nil || v == "" {
				return ask, registry.Refuse("deck: model of the body is the name of a model, a string")
			}
			named = append(named, v)
		}
	}
	if ct, params, err := mime.ParseMediaType(header.Get("Content-Type")); err == nil && ct == "multipart/form-data" {
		r := multipart.NewReader(bytes.NewReader(body), params["boundary"])
		var form []string
		for {
			p, err := r.NextPart()
			if err != nil {
				break
			}
			if p.FormName() == "model" {
				b, _ := io.ReadAll(io.LimitReader(p, 1<<10))
				form = append(form, strings.TrimSpace(string(b)))
			}
		}
		if len(form) > 1 {
			return ask, registry.Refuse("deck: a form names its model once")
		}
		named = append(named, form...)
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

// WithModel is a JSON body with its model replaced, the rest as it came.
func WithModel(body []byte, model string) []byte {
	var m map[string]json.RawMessage
	if json.Unmarshal(body, &m) != nil {
		return body
	}
	m["model"], _ = json.Marshal(model)
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}

// apiUsage is the usage of one call as the families report it: OpenAI and compatible APIs (with the cost
// OpenRouter adds), Anthropic, and Google.
type apiUsage struct {
	Prompt     int64    `json:"prompt_tokens"`
	Completion int64    `json:"completion_tokens"`
	Cost       *float64 `json:"cost"`
	Details    struct {
		Cached  int64 `json:"cached_tokens"`
		Written int64 `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
	Input         int64 `json:"input_tokens"`
	Output        int64 `json:"output_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens"`
	PromptCount   int64 `json:"promptTokenCount"`
	Candidates    int64 `json:"candidatesTokenCount"`
	Thoughts      int64 `json:"thoughtsTokenCount"`
	CachedContent int64 `json:"cachedContentTokenCount"`
}

func (u *apiUsage) merge(o *apiUsage) {
	for _, f := range [][2]*int64{{&u.Prompt, &o.Prompt}, {&u.Completion, &o.Completion}, {&u.Details.Cached, &o.Details.Cached},
		{&u.Details.Written, &o.Details.Written}, {&u.Input, &o.Input}, {&u.Output, &o.Output}, {&u.CacheRead, &o.CacheRead},
		{&u.CacheCreation, &o.CacheCreation}, {&u.PromptCount, &o.PromptCount}, {&u.Candidates, &o.Candidates},
		{&u.Thoughts, &o.Thoughts}, {&u.CachedContent, &o.CachedContent}} {
		*f[0] = max(*f[0], *f[1])
	}
	if o.Cost != nil {
		u.Cost = o.Cost
	}
}

func (u *apiUsage) usage(model string, d time.Duration) provider.Usage {
	out := provider.Usage{Model: model, Duration: d, CostUSD: u.Cost}
	switch {
	case u.Prompt+u.Completion > 0:
		out.Input, out.Output, out.CacheRead, out.CacheCreation = u.Prompt, u.Completion, u.Details.Cached, u.Details.Written
	case u.Input+u.Output > 0:
		out.Input, out.Output, out.CacheRead, out.CacheCreation = u.Input, u.Output, u.CacheRead, u.CacheCreation
	default:
		out.Input, out.Output, out.CacheRead = u.PromptCount, u.Candidates+u.Thoughts, u.CachedContent
	}
	return out
}

// meter passes a model answer through and finds its usage: in the body, or in the events of a stream.
type meter struct {
	body  io.ReadCloser
	line  []byte
	whole []byte
	model string
	u     *apiUsage
	done  func(model string, u *apiUsage)
	shut  bool
}

func (m *meter) Read(p []byte) (int, error) {
	n, err := m.body.Read(p)
	for _, b := range p[:n] {
		if b == '\n' {
			m.scan(m.line)
			m.line = m.line[:0]
		} else if len(m.line) < 1<<20 {
			m.line = append(m.line, b)
		}
	}
	if len(m.whole) < 4<<20 {
		m.whole = append(m.whole, p[:n]...)
	}
	return n, err
}

func (m *meter) scan(line []byte) {
	line = bytes.TrimSpace(bytes.TrimPrefix(bytes.TrimSpace(line), []byte("data:")))
	var v struct {
		Model    string    `json:"model"`
		Version  string    `json:"modelVersion"`
		Usage    *apiUsage `json:"usage"`
		Metadata *apiUsage `json:"usageMetadata"`
		Message  *struct {
			Model string    `json:"model"`
			Usage *apiUsage `json:"usage"`
		} `json:"message"`
	}
	if len(line) == 0 || line[0] != '{' || json.Unmarshal(line, &v) != nil {
		return
	}
	for _, name := range []string{v.Model, v.Version} {
		if name != "" {
			m.model = name
		}
	}
	found := []*apiUsage{v.Usage, v.Metadata}
	if v.Message != nil {
		if v.Message.Model != "" {
			m.model = v.Message.Model
		}
		found = append(found, v.Message.Usage)
	}
	for _, u := range found {
		if u == nil {
			continue
		}
		if m.u == nil {
			m.u = &apiUsage{}
		}
		m.u.merge(u)
	}
}

func (m *meter) Close() error {
	if !m.shut {
		m.shut = true
		m.scan(m.line)
		if m.u == nil {
			m.scan(m.whole)
		}
		m.done(m.model, m.u)
	}
	return m.body.Close()
}

// proxyModel serves a script's call of a model: the rules of deck.model_request decide whether it goes now or
// waits for the owner, frozen as that command; one that goes is streamed through the hops.
func (g *Gateway) proxyModel(w http.ResponseWriter, r *http.Request, act registry.Actor, connection, path string, body []byte) {
	if g.cfg.Models == nil {
		http.Error(w, "deck: no models", http.StatusNotFound)
		return
	}
	ask, err := AskModel(connection, r.Method, path, r.Header, body)
	if err != nil {
		http.Error(w, err.Error(), registry.Status(err))
		return
	}
	asked := ask.Model
	if strings.HasPrefix(asked, "@") && !RolePath(r.Method, path) {
		http.Error(w, "deck: "+asked+" makes a POST of a model call, such as chat/completions or audio/speech; "+r.Method+" "+path+" goes through a connection the manifest names", http.StatusForbidden)
		return
	}
	hops, err := g.cfg.Models(r.Context(), act, ask)
	if err != nil {
		http.Error(w, err.Error(), registry.Status(err))
		return
	}
	c, err := g.reg.Lookup(r.Context(), act.Workspace, ModelRequest)
	if err != nil {
		http.Error(w, err.Error(), registry.Status(err))
		return
	}
	in := map[string]string{"connection": connection, "path": path}
	if fromBody, _ := AskModel(connection, r.Method, path, nil, body); asked != "" && fromBody.Model != asked {
		in["model"] = asked
	}
	if r.Method != http.MethodPost {
		in["method"] = r.Method
	}
	if len(body) > 0 {
		in["body"] = string(body)
	}
	input, _ := json.Marshal(in)
	now, err := g.gate(r.Context(), act, c, act.ProjectID, input)
	if err != nil {
		g.journal(r.Context(), act, act.ProjectID, ModelRequest, c.Class, "rejected", Canonical(input), err.Error())
		http.Error(w, err.Error(), registry.Status(err))
		return
	}
	if !now {
		if len(body) > 0 && !json.Valid(body) {
			http.Error(w, "deck: this call of a model waits for the owner, and only a JSON body can wait; call it again once the owner enabled the project", http.StatusTooManyRequests)
			return
		}
		out, err := g.hold(r.Context(), act, c, act.ProjectID, input)
		if err != nil {
			http.Error(w, err.Error(), registry.Status(err))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	ServeHops(w, r, path, body, strings.HasPrefix(asked, "@"), hops)
}
