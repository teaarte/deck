package acp

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"deck2/internal/gateway"
	"deck2/internal/provider"
)

// entry is all the network the agent has: the deck MCP server of the turn, deck's proxy of the model API, and a
// proxy to the web that is closed in a project without network.
type entry struct {
	ctx     context.Context
	self    string
	mcp     *url.URL
	api     http.Handler
	modelAt string
	// proxyKey is the password of the web proxy, given to the agent in its proxy address.
	proxyKey string
	domains  []string
	now      func() time.Time

	mu sync.Mutex
	// last is the status and Retry-After of the last answer of the model API.
	last       int
	retryAfter string
}

func (e *entry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodConnect || r.URL.IsAbs() && r.URL.Host != e.self:
		if gateway.ProxyAuthorized(w, r, e.proxyKey) {
			e.egress(w, r)
		}
	case r.URL.Path == e.mcp.Path:
		(&httputil.ReverseProxy{FlushInterval: -1, Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: e.mcp.Scheme, Host: e.mcp.Host})
			pr.Out.URL.Path, pr.Out.URL.RawPath = e.mcp.Path, ""
		}}).ServeHTTP(w, r)
	case strings.HasPrefix(r.URL.Path, e.modelAt+"/"):
		http.StripPrefix(e.modelAt, e.api).ServeHTTP(&status{ResponseWriter: w, e: e}, r)
	default:
		http.Error(w, "deck: not found", http.StatusNotFound)
	}
}

// status keeps the status and Retry-After of an answer of the model API on its way to the agent.
type status struct {
	http.ResponseWriter
	e *entry
}

func (s *status) WriteHeader(code int) {
	s.e.mu.Lock()
	s.e.last, s.e.retryAfter = code, s.Header().Get("Retry-After")
	s.e.mu.Unlock()
	s.ResponseWriter.WriteHeader(code)
}

func (s *status) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// refused tells a failed turn by the last answer of the model API: a usage limit (429) pauses, a refused key
// needs a login, as in deck's own tool loop.
func (e *entry) refused(res provider.Result) provider.Result {
	e.mu.Lock()
	status, after := e.last, e.retryAfter
	e.mu.Unlock()
	provider.Refused(&res, status, after, e.now())
	return res
}

// egress carries the agent to a public address only: its web is the internet, not deck, other sessions on this
// machine or the owner's network.
func (e *entry) egress(w http.ResponseWriter, r *http.Request) {
	if len(e.domains) == 0 {
		http.Error(w, "deck: "+r.Host+" is not reachable from this agent", http.StatusForbidden)
		return
	}
	gateway.Egress(e.ctx, w, r, e.domains)
}
