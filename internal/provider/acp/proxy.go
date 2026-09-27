package acp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
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
	model   *url.URL
	modelAt string
	key     string
	name    string
	domains []string
	onUsage func(provider.Usage)
	now     func() time.Time

	mu sync.Mutex
	// last is the status and Retry-After of the last answer of the model API.
	last       int
	retryAfter string
}

func (e *entry) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodConnect || r.URL.IsAbs() && r.URL.Host != e.self:
		e.egress(w, r)
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

func (e *entry) callModel(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	(&httputil.ReverseProxy{FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(e.model)
			pr.Out.URL.Path = strings.TrimSuffix(e.model.Path, "/") + strings.TrimPrefix(r.URL.Path, e.modelAt)
			pr.Out.URL.RawPath = ""
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Accept-Encoding")
			if e.key != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+e.key)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			e.mu.Lock()
			e.last, e.retryAfter = resp.StatusCode, resp.Header.Get("Retry-After")
			e.mu.Unlock()
			resp.Body = &meter{body: resp.Body, done: func(model string, u *usage) {
				if u != nil && e.onUsage != nil {
					e.onUsage(u.of(model, e.name, time.Since(start)))
				}
			}}
			return nil
		}}).ServeHTTP(w, r)
}

// refused tells a failed turn by the last answer of the model API: a usage limit (429) pauses, a refused key
// needs a login, as in deck's own tool loop.
func (e *entry) refused(res provider.Result) provider.Result {
	e.mu.Lock()
	status, after := e.last, e.retryAfter
	e.mu.Unlock()
	switch status {
	case http.StatusTooManyRequests:
		res.RateLimited = true
		if s, err := strconv.Atoi(after); err == nil && s > 0 {
			res.ResetsAt = e.now().Add(time.Duration(s) * time.Second).Unix()
		} else if t, err := http.ParseTime(after); err == nil {
			res.ResetsAt = t.Unix()
		}
	case http.StatusUnauthorized, http.StatusForbidden:
		res.NeedsLogin = true
	}
	return res
}

// egress carries the agent to a public address only: its web is the internet, not deck, other sessions on this
// machine or the owner's network. The address is dialled as resolved, so a second lookup cannot change it.
func (e *entry) egress(w http.ResponseWriter, r *http.Request) {
	target := r.Host
	if r.Method != http.MethodConnect {
		target = r.URL.Host
	}
	host, port, err := net.SplitHostPort(target)
	if err != nil {
		host, port = target, map[string]string{"http": "80", "https": "443"}[r.URL.Scheme]
	}
	ips, err := net.DefaultResolver.LookupNetIP(r.Context(), "ip", host)
	if err != nil || len(ips) == 0 || len(e.domains) == 0 {
		http.Error(w, "deck: "+host+" is not reachable from this agent", http.StatusForbidden)
		return
	}
	for _, ip := range ips {
		if ip = ip.Unmap(); !ip.IsGlobalUnicast() || ip.IsPrivate() || shared.Contains(ip) {
			http.Error(w, "deck: "+host+" is not a public address; agents reach deck and local services through deck tools", http.StatusForbidden)
			return
		}
	}
	dial := net.JoinHostPort(ips[0].Unmap().String(), port)
	if r.Method == http.MethodConnect {
		r.Host = dial
	} else {
		r.URL.Host = dial
	}
	gateway.Egress(e.ctx, w, r, e.domains)
}

// shared is the carrier-grade NAT range, which tailnets use.
var shared = netip.MustParsePrefix("100.64.0.0/10")

// usage is the token count of one chat completion as OpenAI-compatible APIs report it; deck's own loop reads the same.
type usage struct {
	Prompt     int64 `json:"prompt_tokens"`
	Completion int64 `json:"completion_tokens"`
	Details    struct {
		Cached  int64 `json:"cached_tokens"`
		Written int64 `json:"cache_write_tokens"`
	} `json:"prompt_tokens_details"`
}

func (u *usage) of(model, asked string, d time.Duration) provider.Usage {
	return provider.Usage{Model: model, RequestModel: asked, Input: u.Prompt, Output: u.Completion,
		CacheRead: u.Details.Cached, CacheCreation: u.Details.Written, Duration: d}
}

// meter passes a model response through and finds its usage: in the body, or in the last event of a stream.
type meter struct {
	body  io.ReadCloser
	line  []byte
	whole []byte
	model string
	u     *usage
	done  func(model string, u *usage)
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
		Model string `json:"model"`
		Usage *usage `json:"usage"`
	}
	if json.Unmarshal(line, &v) != nil {
		return
	}
	if v.Model != "" {
		m.model = v.Model
	}
	if v.Usage != nil {
		m.u = v.Usage
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
