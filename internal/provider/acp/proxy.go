package acp

import (
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
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
	past       bool
	refusal    string
	// answered hears each answer of the model API, a refusal too.
	answered func()
}

func (e *entry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == e.mcp.Path:
		(&httputil.ReverseProxy{FlushInterval: -1, Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(&url.URL{Scheme: e.mcp.Scheme, Host: e.mcp.Host})
			pr.Out.URL.Path, pr.Out.URL.RawPath = e.mcp.Path, ""
		}}).ServeHTTP(w, r)
	case strings.HasPrefix(r.URL.Path, e.modelAt+"/"):
		http.StripPrefix(e.modelAt, e.api).ServeHTTP(w, r)
	default:
		http.Error(w, "deck: not found", http.StatusNotFound)
	}
}

// heard keeps what the model API answered a call of the turn.
func (e *entry) heard(a provider.Answer) {
	if e.answered != nil {
		e.answered()
	}
	e.mu.Lock()
	e.last, e.retryAfter, e.past, e.refusal = a.Status, a.RetryAfter, a.Past, a.Words
	e.mu.Unlock()
}

// refused tells a failed turn by the last answer of the model API: a usage limit (429) pauses, a refused key
// needs a login, another refusal keeps the words of the API.
func (e *entry) refused(res provider.Result) provider.Result {
	e.mu.Lock()
	status, after, words, past := e.last, e.retryAfter, e.refusal, e.past
	e.mu.Unlock()
	if past {
		res.Outcome, res.Past = provider.Failed, untrusted.Line(words, 500)
		return res
	}
	if !provider.Refused(&res, status, after, e.now()) && status >= 400 {
		res.Refusal = fmt.Sprintf("%d %s", status, untrusted.Line(words, 500))
	}
	return res
}
