package acp

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"deck2/internal/fault"
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
	// last is the last answer of the model API, with the words of one that refused.
	last provider.Answer
	// answered hears each answer of the model API, a refusal too.
	answered func()
	// reach hears each host the agent reached through the door of its box.
	reach func(host string)
}

// Reach hears a host the agent reached through the door of its box.
func (e *entry) Reach(host string) {
	if e.reach != nil {
		e.reach(host)
	}
}

func (e *entry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.URL.Path == e.mcp.Path:
		provider.MCP(e.mcp).ServeHTTP(w, r)
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
	e.last = a
	e.mu.Unlock()
}

// refused tells a failed turn by the last answer of the model API: a usage limit (429) pauses, a refused key
// needs a login, another refusal keeps the words of the API.
func (e *entry) refused(res provider.Result) provider.Result {
	e.mu.Lock()
	a := e.last
	e.mu.Unlock()
	if a.Past {
		res.Past = untrusted.Flat(a.Words, 500)
		res.Fail(fault.TaskFailed)
		return res
	}
	if !provider.Refused(&res, a, e.now()) && a.Status >= 400 {
		res.Refusal = fmt.Sprintf("%d %s", a.Status, untrusted.Line(a.Words, 500))
	}
	return res
}
