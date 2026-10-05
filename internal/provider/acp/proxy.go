package acp

import (
	"context"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"deck2/internal/gateway"
	"deck2/internal/provider"
)

// entry is all the network the agent has: the deck MCP server of the turn, the model API with the key put in by
// deck, and a proxy to the web that is closed in a project without network.
type entry struct {
	ctx     context.Context
	self    string
	mcp     *url.URL
	model   gateway.ModelTarget
	modelAt string
	// proxyKey is the password of the web proxy, given to the agent in its proxy address.
	proxyKey string
	name     string
	domains  []string
	onUsage  func(provider.Usage)
	// price fills in the cost of a call the API did not price; over says why the day is past its limit and until when.
	price func(*provider.Usage)
	over  func() (string, time.Time)
	now   func() time.Time

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
		e.callModel(w, r)
	default:
		http.Error(w, "deck: not found", http.StatusNotFound)
	}
}

// callModel passes a call of the agent to its model API with deck's key, unless the day is past the limit of the
// connection: then the agent hears a usage limit until the day is over.
func (e *entry) callModel(w http.ResponseWriter, r *http.Request) {
	if e.over != nil {
		if why, until := e.over(); why != "" {
			after := strconv.Itoa(max(1, int(until.Sub(e.now()).Seconds())))
			e.mu.Lock()
			e.last, e.retryAfter = http.StatusTooManyRequests, after
			e.mu.Unlock()
			w.Header().Set("Retry-After", after)
			http.Error(w, why, http.StatusTooManyRequests)
			return
		}
	}
	body, err := gateway.ReadBody(w, r)
	if err != nil {
		return
	}
	start := time.Now()
	resp, err := gateway.SendModel(r.Context(), e.model, r.Method, strings.TrimPrefix(r.URL.Path, e.modelAt), r.URL.RawQuery, r.Header, body)
	if err != nil {
		http.Error(w, "deck: the model API: "+err.Error(), http.StatusBadGateway)
		return
	}
	e.mu.Lock()
	e.last, e.retryAfter = resp.StatusCode, resp.Header.Get("Retry-After")
	e.mu.Unlock()
	gateway.Relay(w, resp, start, func(u *provider.Usage) {
		if u == nil || e.onUsage == nil {
			return
		}
		u.RequestModel = e.name
		if e.price != nil {
			e.price(u)
		}
		e.onUsage(*u)
	})
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
