package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"deck2/internal/provider"
	"deck2/internal/registry"
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
	// Connection names the target; Model is the name the API knows, which replaces an @role the caller asked for.
	Connection string
	Model      string
	// Over says why the day of its connection or project is past its limit, Wait how long until that day ends;
	// deck skips such a hop.
	Over string
	Wait time.Duration
	// Local is a connection on the owner's hardware; Free is a model that costs nothing.
	Local, Free bool
	// Done keeps the call: the status the API answered and what it used, nil when it did not say.
	Done func(ctx context.Context, status int, u *provider.Usage)
}

// ModelRoute finds where a call of a model goes for an actor: the connection named, and the model of the body;
// an @role names the models assigned to it, the fallbacks after the first.
type ModelRoute func(ctx context.Context, act registry.Actor, connection, model string) ([]ModelHop, error)

// ModelRequest is the command a call of a model through the proxy is frozen as when it waits for the owner.
const ModelRequest = registry.Builtin + ".model_request"

var modelClient = &http.Client{
	Transport: &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
		ResponseHeaderTimeout: 5 * time.Minute, ForceAttemptHTTP2: true},
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// dropped are headers of the caller deck does not pass on: its own keys, cookies and how the body is packed.
var dropped = []string{"Authorization", "X-Api-Key", "X-Goog-Api-Key", "Cookie", "Proxy-Authorization", "Accept-Encoding",
	"Connection", "Keep-Alive", "Te", "Trailer", "Transfer-Encoding", "Upgrade", "Content-Length", "Host"}

// SendModel makes one request of path under the API of t with deck's key in place of any the caller sent.
func SendModel(ctx context.Context, t ModelTarget, method, path, query string, header http.Header, body []byte) (*http.Response, error) {
	u, err := url.Parse(t.URL)
	if err != nil {
		return nil, err
	}
	if path = strings.TrimPrefix(path, "/"); path != "" {
		u.Path = strings.TrimSuffix(u.Path, "/") + "/" + path
		u.RawPath = ""
	}
	q, _ := url.ParseQuery(query)
	q.Del("key")
	for k, vs := range u.Query() {
		q[k] = vs
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	for k, vs := range header {
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

// Metered passes an answer of a model API through and calls done with the usage it reports once it is read.
func Metered(body io.ReadCloser, start time.Time, done func(*provider.Usage)) io.ReadCloser {
	return &meter{body: body, done: func(model string, u *apiUsage) {
		if u == nil {
			done(nil)
			return
		}
		pu := u.usage(model, time.Since(start))
		done(&pu)
	}}
}

// ServeHops serves one call of a model through the hops in order, the next after a refusal or a day past its limit;
// replace puts the model of each hop in the body. Past every limit it is a 429 until the first day ends.
func ServeHops(w http.ResponseWriter, r *http.Request, path string, body []byte, replace bool, hops []ModelHop) {
	var last string
	var wait time.Duration
	for i, h := range hops {
		if h.Over != "" {
			last = h.Over
			if wait == 0 || h.Wait < wait {
				wait = h.Wait
			}
			continue
		}
		b := body
		if replace {
			b = WithModel(body, h.Model)
		}
		start := time.Now()
		resp, err := SendModel(r.Context(), h.Target, r.Method, path, r.URL.RawQuery, r.Header, b)
		if err != nil {
			last = fmt.Sprintf("deck: %s: %v", h.Connection, err)
			h.Done(r.Context(), 0, nil)
			if Unsent(err) && i < len(hops)-1 {
				continue
			}
			http.Error(w, last, http.StatusBadGateway)
			return
		}
		if Fallible(resp.StatusCode) && i < len(hops)-1 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
			_ = resp.Body.Close()
			last = fmt.Sprintf("deck: %s answered %s: %s", h.Connection, resp.Status, strings.TrimSpace(string(raw)))
			h.Done(r.Context(), resp.StatusCode, nil)
			continue
		}
		ctx := context.WithoutCancel(r.Context())
		Relay(w, resp, start, func(u *provider.Usage) { h.Done(ctx, resp.StatusCode, u) })
		return
	}
	if wait > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(max(1, int(wait.Seconds()))))
	}
	http.Error(w, last, http.StatusTooManyRequests)
}

// Relay passes an answer of a model API to the caller as it comes and calls done with its usage once it is read.
func Relay(w http.ResponseWriter, resp *http.Response, start time.Time, done func(*provider.Usage)) {
	body := Metered(resp.Body, start, done)
	defer body.Close()
	for k, vs := range resp.Header {
		if k != "Content-Length" && k != "Connection" && k != "Transfer-Encoding" {
			w.Header()[k] = vs
		}
	}
	w.WriteHeader(resp.StatusCode)
	flush(w, body)
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

// AskedModel is the model a JSON body asks for, empty when the body is not JSON or names none.
func AskedModel(body []byte) string {
	var v struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &v)
	return v.Model
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
	asked := AskedModel(body)
	if strings.HasPrefix(asked, "@") && !RolePath(r.Method, path) {
		http.Error(w, "deck: "+asked+" makes a POST of a model call, such as chat/completions or audio/speech; "+r.Method+" "+path+" goes through a connection the manifest names", http.StatusForbidden)
		return
	}
	hops, err := g.cfg.Models(r.Context(), act, connection, asked)
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
			http.Error(w, "deck: this call of a model waits for the owner, and only a JSON body can wait; call it again once the owner raised the limit", http.StatusTooManyRequests)
			return
		}
		out, err := g.hold(r.Context(), act, c, act.ProjectID, input)
		if err != nil {
			http.Error(w, err.Error(), registry.Status(err))
			return
		}
		if held, ok := out.(Waiting); ok && len(hops) > 0 && hops[0].Over != "" {
			held.Text = hops[0].Over + ". " + held.Text
			out = held
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(out)
		return
	}
	ServeHops(w, r, path, body, strings.HasPrefix(asked, "@"), hops)
}
