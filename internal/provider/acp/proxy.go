package acp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"deck2/internal/provider"
	"deck2/internal/untrusted"
)

// entry is deck at the door of the agent: the deck MCP server of the turn and deck's proxy of the model API.
type entry struct {
	mcp     *url.URL
	api     http.Handler
	modelAt string
	now     func() time.Time

	mu sync.Mutex
	// last is the status and Retry-After of the last answer of the model API, with the words of one that refused.
	last       int
	retryAfter string
	refusal    string
	// plain is set once the model API refused the reasoning an agent hands back in the history.
	plain bool
}

func (e *entry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == e.mcp.Path:
		(&httputil.ReverseProxy{FlushInterval: -1, Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: e.mcp.Scheme, Host: e.mcp.Host})
			pr.Out.URL.Path, pr.Out.URL.RawPath = e.mcp.Path, ""
		}}).ServeHTTP(w, r)
	case strings.HasPrefix(r.URL.Path, e.modelAt+"/"):
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if !turnCall(body) {
			r.Body = io.NopCloser(bytes.NewReader(body))
			http.StripPrefix(e.modelAt, e.api).ServeHTTP(w, r)
			return
		}
		w = &status{ResponseWriter: w, e: e}
		e.mu.Lock()
		plain := e.plain
		e.mu.Unlock()
		if plain {
			body = withoutReasoning(body)
		}
		send := func(w http.ResponseWriter, body []byte) {
			r := r.Clone(r.Context())
			r.Body, r.ContentLength = io.NopCloser(bytes.NewReader(body)), int64(len(body))
			http.StripPrefix(e.modelAt, e.api).ServeHTTP(w, r)
		}
		h := &held{w: w, header: http.Header{}}
		send(h, body)
		if h.code == http.StatusBadRequest && !plain && refusesReasoning(h.buf.Bytes()) {
			e.mu.Lock()
			e.plain = true
			e.mu.Unlock()
			send(w, withoutReasoning(body))
			return
		}
		h.release()
	default:
		http.Error(w, "deck: not found", http.StatusNotFound)
	}
}

// turnCall reports whether a call of the model is one of the turn, which carries the tools of the agent, and not one
// on its side such as the title of a session; only the turn's answers tell a limit or a refused key.
func turnCall(body []byte) bool {
	var v struct {
		Tools []json.RawMessage `json:"tools"`
	}
	return json.Unmarshal(body, &v) == nil && len(v.Tools) > 0
}

// status keeps the status and Retry-After of an answer of the model API on its way to the agent.
type status struct {
	http.ResponseWriter
	e *entry
}

func (s *status) WriteHeader(code int) {
	s.e.mu.Lock()
	s.e.last, s.e.retryAfter, s.e.refusal = code, s.Header().Get("Retry-After"), ""
	s.e.mu.Unlock()
	s.ResponseWriter.WriteHeader(code)
}

func (s *status) Write(b []byte) (int, error) {
	s.e.mu.Lock()
	if s.e.last >= 400 && len(s.e.refusal) < 2000 {
		s.e.refusal += string(b)
	}
	s.e.mu.Unlock()
	return s.ResponseWriter.Write(b)
}

func (s *status) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// refused tells a failed turn by the last answer of the model API: a usage limit (429) pauses, a refused key
// needs a login, another refusal is an error of the turn with the words of the API.
func (e *entry) refused(res provider.Result) provider.Result {
	e.mu.Lock()
	status, after, words := e.last, e.retryAfter, e.refusal
	e.mu.Unlock()
	if !provider.Refused(&res, status, after, e.now()) && status >= 400 {
		res.Refusal = fmt.Sprintf("%d %s", status, untrusted.Line(words, 500))
		res.IsError, res.Text = true, "acp: the model API refused a call of the turn: "+res.Refusal
	}
	return res
}

// echoed are the fields of reasoning an agent hands back in the messages of its history, which no OpenAI-compatible
// API needs and a strict one refuses.
var echoed = []string{"reasoning_content", "reasoning", "reasoning_details"}

// refusesReasoning reports whether the model API refused a call for a field of reasoning in its messages.
func refusesReasoning(answer []byte) bool {
	return slices.ContainsFunc(echoed, func(f string) bool { return bytes.Contains(answer, []byte(f)) })
}

// withoutReasoning is a call of the model with no field of reasoning in its messages.
func withoutReasoning(body []byte) []byte {
	var call map[string]json.RawMessage
	var msgs []map[string]json.RawMessage
	if json.Unmarshal(body, &call) != nil || json.Unmarshal(call["messages"], &msgs) != nil {
		return body
	}
	for _, m := range msgs {
		for _, f := range echoed {
			delete(m, f)
		}
	}
	call["messages"], _ = json.Marshal(msgs)
	out, err := json.Marshal(call)
	if err != nil {
		return body
	}
	return out
}

// held is an answer of the model API kept back while it may be a bad request deck sends again.
type held struct {
	w      http.ResponseWriter
	header http.Header
	code   int
	buf    bytes.Buffer
}

func (h *held) Header() http.Header { return h.header }

func (h *held) WriteHeader(code int) {
	if h.code != 0 {
		return
	}
	h.code = code
	if code != http.StatusBadRequest {
		h.pass()
	}
}

func (h *held) Write(b []byte) (int, error) {
	if h.code == 0 {
		h.WriteHeader(http.StatusOK)
	}
	if h.code == http.StatusBadRequest {
		return h.buf.Write(b)
	}
	return h.w.Write(b)
}

func (h *held) Flush() {
	if f, ok := h.w.(http.Flusher); ok && h.code != http.StatusBadRequest {
		f.Flush()
	}
}

func (h *held) pass() {
	for k, v := range h.header {
		h.w.Header()[k] = v
	}
	h.w.WriteHeader(h.code)
}

// release hands a kept bad request on to the agent.
func (h *held) release() {
	if h.code != http.StatusBadRequest {
		return
	}
	h.pass()
	_, _ = h.w.Write(h.buf.Bytes())
}
