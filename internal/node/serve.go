package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"deck2/internal/egress"
)

// Pauses of a node between tries to reach deck: the first, the longest, and the one after deck refused its key.
const (
	firstPause   = time.Second
	longestPause = time.Minute
	refusedPause = 5 * time.Minute
)

// ErrRefused is deck refusing the key of a node: the node was revoked, or its key is of another deck.
var ErrRefused = errors.New("deck refused the key of this node: pair it again with deck pair --node on deck's machine")

// Joined is a node joined to a deck: deck's address, the key of the node, and the version of the node it tells.
type Joined struct {
	Deck    string `json:"deck"`
	Key     string `json:"key"`
	Device  int64  `json:"device"`
	Version string `json:"-"`
}

// Run keeps the node connected to deck with its key until ctx ends: it connects again after a pause that grows
// while deck is not there, and serves deck's calls to the services of its machine.
func Run(ctx context.Context, j Joined) {
	pause := firstPause
	for ctx.Err() == nil {
		connected, err := Connect(ctx, j)
		if ctx.Err() != nil {
			return
		}
		if connected {
			pause = firstPause
		}
		wait := pause
		if errors.Is(err, ErrRefused) {
			wait = refusedPause
		}
		slog.Warn("the node is not connected to deck", "deck", j.Deck, "err", err, "again in", wait)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		pause = min(2*pause, longestPause)
	}
}

// Connect opens the connection of the node to deck and serves deck's calls over it until it breaks; connected says
// deck took it.
func Connect(ctx context.Context, j Joined) (connected bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(j.Deck, "/")+"/auth/node", nil)
	if err != nil {
		return false, err
	}
	req.Header.Set("Authorization", "Bearer "+j.Key)
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", Upgrade)
	req.Header.Set(VersionHeader, j.Version)
	resp, err := egress.ToDeck.RoundTrip(req)
	if err != nil {
		return false, err
	}
	if resp.StatusCode != http.StatusSwitchingProtocols {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			return false, fmt.Errorf("%w (%s)", ErrRefused, strings.TrimSpace(string(b)))
		}
		return false, fmt.Errorf("deck answered %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	rw, ok := resp.Body.(io.ReadWriteCloser)
	if !ok {
		_ = resp.Body.Close()
		return false, errors.New("deck's answer carries no connection")
	}
	slog.Info("the node is connected to deck", "deck", j.Deck)
	conn := &upgraded{ReadWriteCloser: rw, gone: make(chan struct{})}
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	srv := &http.Server{Handler: Handler(), Protocols: &p, HTTP2: egress.NodeHTTP2}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(&single{conn: conn}) }()
	select {
	case <-conn.gone:
	case <-ctx.Done():
	}
	_ = srv.Close()
	<-served
	return true, errors.New("the connection to deck ended")
}

// Handler serves deck's calls on a node: a call to an address of its machine, or the probe of the addresses that
// answer; the node reaches nothing beyond its own loopback.
func Handler() http.Handler {
	proxy := &httputil.ReverseProxy{Transport: egress.Loopback, FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			target, _ := url.Parse(pr.In.Header.Get(TargetHeader))
			pr.SetURL(target)
			pr.Out.Header.Del(TargetHeader)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "deck node: "+err.Error(), http.StatusBadGateway)
		}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get(TargetHeader)
		if target == "" && r.Method == http.MethodPost && r.URL.Path == probePath {
			answerProbe(w, r)
			return
		}
		u, err := url.Parse(target)
		if target == "" || err != nil || u.Scheme != "http" && u.Scheme != "https" || !egress.IsLoopback(u.Hostname()) || u.Path != "" {
			http.Error(w, "deck node: a call names an address of this machine, such as http://127.0.0.1:8178", http.StatusBadRequest)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

// answerProbe says which of the addresses deck asks about take a connection on this machine.
func answerProbe(w http.ResponseWriter, r *http.Request) {
	var in probe
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&in); err != nil {
		http.Error(w, "deck node: "+err.Error(), http.StatusBadRequest)
		return
	}
	out := probe{Up: []string{}}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for _, a := range in.Addresses {
		wg.Go(func() {
			ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
			defer cancel()
			if c, err := egress.DialLoopback(ctx, a); err == nil {
				_ = c.Close()
				mu.Lock()
				out.Up = append(out.Up, a)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

// upgraded is the connection to deck after its upgrade; gone closes once the server of the node lets it go.
type upgraded struct {
	io.ReadWriteCloser
	gone chan struct{}
	once sync.Once
}

func (u *upgraded) Close() error {
	u.once.Do(func() { close(u.gone) })
	return u.ReadWriteCloser.Close()
}

func (u *upgraded) LocalAddr() net.Addr              { return nodeAddr{} }
func (u *upgraded) RemoteAddr() net.Addr             { return nodeAddr{} }
func (u *upgraded) SetDeadline(time.Time) error      { return nil }
func (u *upgraded) SetReadDeadline(time.Time) error  { return nil }
func (u *upgraded) SetWriteDeadline(time.Time) error { return nil }

type nodeAddr struct{}

func (nodeAddr) Network() string { return "tcp" }
func (nodeAddr) String() string  { return "deck" }

// single gives the server of the node its one connection, then waits until the server closes.
type single struct {
	conn *upgraded
	once sync.Once
}

func (s *single) Accept() (net.Conn, error) {
	var c net.Conn
	s.once.Do(func() { c = s.conn })
	if c != nil {
		return c, nil
	}
	<-s.conn.gone
	return nil, net.ErrClosed
}

func (s *single) Close() error   { return s.conn.Close() }
func (s *single) Addr() net.Addr { return nodeAddr{} }
