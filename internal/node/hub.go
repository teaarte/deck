// Package node is a machine that serves deck what only it reaches, such as the models on its own loopback: deck's
// side holds the one connection each node opened to it, the node's side serves the calls deck sends over it.
package node

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"deck2/internal/egress"
)

// Upgrade is the protocol a node asks its connection to deck to become: HTTP/2, deck the client.
const Upgrade = "deck-node"

// Headers between deck and a node: the address of its machine a call goes to, the version of the node, the agents it
// runs, and the port of a call it refused as one its owner does not allow.
const (
	TargetHeader  = "Deck-Node-Target"
	VersionHeader = "Deck-Node-Version"
	AgentsHeader  = "Deck-Node-Agents"
	DeniedHeader  = "Deck-Node-Denied"
)

// AgentPath begins the calls of the turns of agents a node runs.
const AgentPath = "/.deck/agent/"

// probePath is the call that asks a node which addresses of its machine answer.
const probePath = "/.deck/probe"

// Hub holds the connections of the nodes that joined deck, one a node.
type Hub struct {
	now     func() time.Time
	changed func(device int64, online bool)
	mu      sync.Mutex
	links   map[int64]*link
	// left is when each node that was connected went, for as long as deck runs.
	left   map[int64]time.Time
	closed bool
}

// link is a node connected: deck's client over its connection, since when, by which address of deck.
type link struct {
	cc      *http.ClientConn
	since   time.Time
	address string
	version string
	agents  []string
	gone    chan struct{}
	once    sync.Once
}

// went marks the link gone; end also closes its connection, whose close calls went again from its hook.
func (l *link) went() { l.once.Do(func() { close(l.gone) }) }

func (l *link) end() {
	l.went()
	_ = l.cc.Close()
}

// NewHub holds connections of nodes; changed hears each node that comes or goes, after it did.
func NewHub(now func() time.Time, changed func(device int64, online bool)) *Hub {
	return &Hub{now: now, changed: changed, links: map[int64]*link{}, left: map[int64]time.Time{}}
}

// State is whether a node is connected and since when it is or is not; zero when it was not since deck started.
type State struct {
	Online  bool
	Since   time.Time
	Address string
	Version string
	// Agents are the agents the node runs for deck, as it said when it connected.
	Agents []string
}

// State is the state of the connection of a node.
func (h *Hub) State(device int64) State {
	h.mu.Lock()
	defer h.mu.Unlock()
	if l := h.links[device]; l != nil {
		return State{Online: true, Since: l.since, Address: l.address, Version: l.version, Agents: l.agents}
	}
	return State{Since: h.left[device]}
}

// Serve takes the connection of a node that asked to upgrade it and holds it until it breaks, deck stops, or ctx
// ends, as when the node is revoked; a connection of the same node before it ends.
func (h *Hub) Serve(ctx context.Context, w http.ResponseWriter, r *http.Request, device int64, address string) {
	if !strings.EqualFold(r.Header.Get("Upgrade"), Upgrade) {
		http.Error(w, "deck: a node connects with Upgrade: "+Upgrade, http.StatusUpgradeRequired)
		return
	}
	conn, rw, err := http.NewResponseController(w).Hijack()
	if err != nil {
		http.Error(w, "deck: this connection cannot carry a node: "+err.Error(), http.StatusInternalServerError)
		return
	}
	_ = conn.SetDeadline(time.Time{})
	if _, err := io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: "+Upgrade+"\r\n\r\n"); err != nil {
		_ = conn.Close()
		return
	}
	cc, err := egress.NodeConn(ctx, &buffered{Conn: conn, r: rw.Reader})
	if err != nil {
		_ = conn.Close()
		return
	}
	l := &link{cc: cc, since: h.now(), address: address, version: r.Header.Get(VersionHeader), gone: make(chan struct{})}
	for _, a := range strings.Split(r.Header.Get(AgentsHeader), ",") {
		if a = strings.TrimSpace(a); agentName.MatchString(a) {
			l.agents = append(l.agents, a)
		}
	}
	cc.SetStateHook(func(cc *http.ClientConn) {
		if cc.Err() != nil {
			l.went()
		}
	})
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		l.end()
		return
	}
	old := h.links[device]
	h.links[device] = l
	h.mu.Unlock()
	if old != nil {
		old.end()
	}
	h.changed(device, true)
	select {
	case <-l.gone:
	case <-ctx.Done():
		l.end()
	}
	h.mu.Lock()
	gone := h.links[device] == l
	if gone {
		delete(h.links, device)
		h.left[device] = h.now()
	}
	h.mu.Unlock()
	if gone {
		h.changed(device, false)
	}
}

// Close ends the connection of every node, as deck stops.
func (h *Hub) Close() {
	h.mu.Lock()
	h.closed = true
	links := make([]*link, 0, len(h.links))
	for _, l := range h.links {
		links = append(links, l)
	}
	h.mu.Unlock()
	for _, l := range links {
		l.end()
	}
}

// buffered reads what the server of deck read ahead of the upgrade before the connection itself.
type buffered struct {
	net.Conn
	r *bufio.Reader
}

func (b *buffered) Read(p []byte) (int, error) {
	if b.r.Buffered() > 0 {
		return b.r.Read(p)
	}
	return b.Conn.Read(p)
}

// Offline is the failure of a call of a node that is not connected: deck sent nothing, so the next model takes it.
func Offline(name string) error {
	return &net.OpError{Op: "dial", Net: "node", Err: fmt.Errorf("the node %s is not connected to deck", name)}
}

// Denied is a call the node refused, as its owner does not allow its port: no model got it, so the next one takes it.
func Denied(name, port string) error {
	return &net.OpError{Op: "dial", Net: "node", Err: fmt.Errorf("port %s is not allowed on the node %s: on its machine add it with deck -join <deck's address> --install --allow %s", port, name, port)}
}

// Transport carries calls over the connection of a node to the address of its machine they name; name says the
// node in a failure.
func (h *Hub) Transport(device int64, name string) http.RoundTripper {
	return &through{h: h, device: device, name: name}
}

type through struct {
	h      *Hub
	device int64
	name   string
}

func (t *through) RoundTrip(req *http.Request) (*http.Response, error) {
	t.h.mu.Lock()
	l := t.h.links[t.device]
	t.h.mu.Unlock()
	if l == nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, Offline(t.name)
	}
	out := req.Clone(req.Context())
	out.Header.Set(TargetHeader, req.URL.Scheme+"://"+req.URL.Host)
	out.URL = &url.URL{Scheme: "http", Host: "node", Path: req.URL.Path, RawPath: req.URL.RawPath, RawQuery: req.URL.RawQuery}
	out.Host = "node"
	resp, err := l.cc.RoundTrip(out)
	if err != nil && req.Context().Err() == nil {
		// The connection broke before the answer, as when the machine of the node sleeps: the next model takes it.
		return nil, errors.Join(Offline(t.name), err)
	}
	if err == nil && resp.StatusCode == http.StatusForbidden && resp.Header.Get(DeniedHeader) != "" {
		_ = resp.Body.Close()
		t.h.changed(t.device, true)
		return nil, Denied(t.name, resp.Header.Get(DeniedHeader))
	}
	return resp, err
}

// Probed are the addresses of the machine of a node that answer, and those on ports its owner does not allow.
type Probed struct{ Up, Denied []string }

// Probe asks a node which of the addresses of its machine answer.
func (h *Hub) Probe(ctx context.Context, device int64, addresses []string) (Probed, error) {
	h.mu.Lock()
	l := h.links[device]
	h.mu.Unlock()
	if l == nil {
		return Probed{}, errors.New("deck: the node is not connected")
	}
	b, _ := json.Marshal(probe{Addresses: addresses})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://node"+probePath, strings.NewReader(string(b)))
	if err != nil {
		return Probed{}, err
	}
	resp, err := l.cc.RoundTrip(req)
	if err != nil {
		return Probed{}, err
	}
	defer resp.Body.Close()
	var out probe
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil {
		return Probed{}, err
	}
	return Probed{Up: out.Up, Denied: out.Denied}, nil
}

// probe is the call of Probe and its answer.
type probe struct {
	Addresses []string `json:"addresses,omitempty"`
	Up        []string `json:"up"`
	Denied    []string `json:"denied,omitempty"`
}
