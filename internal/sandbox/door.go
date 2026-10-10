package sandbox

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"os"
	"sync"
	"time"

	"deck2/internal/nonce"
)

// Web carries a proxy request of a confined program to a host in domains; its tunnels end when ctx ends.
type Web func(ctx context.Context, w http.ResponseWriter, r *http.Request, domains []string)

// Door is the one way out of a confined program: a proxy with a key of its own to its domains, and deck's own
// handler at its address.
type Door struct {
	ln        net.Listener
	dir       string
	self, key string
	srv       *http.Server
	cut       context.CancelFunc
	once      sync.Once
}

// Open opens a door of sb: a request to another host that carries the key goes to web with domains, none goes
// without domains; deck serves the rest, and nil answers 404.
func Open(sb Sandbox, web Web, domains []string, deck http.Handler) (*Door, error) {
	dir, err := os.MkdirTemp("", "deck-")
	if err != nil {
		return nil, err
	}
	ln, err := sb.Listen(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	if deck == nil {
		deck = http.NotFoundHandler()
	}
	life, cut := context.WithCancel(context.Background())
	d := &Door{ln: ln, dir: dir, self: sb.Addr(ln), key: nonce.Hex(16), cut: cut}
	d.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect && (!r.URL.IsAbs() || r.URL.Host == d.self) {
			deck.ServeHTTP(w, r)
			return
		}
		if !d.authorized(w, r) {
			return
		}
		if web == nil || len(domains) == 0 {
			http.Error(w, "deck: "+r.Host+" is not reachable from this program", http.StatusForbidden)
			return
		}
		web(life, w, r, domains)
	})}
	go func() { _ = d.srv.Serve(ln) }()
	return d, nil
}

// URL is the address the confined program reaches deck at.
func (d *Door) URL() string { return "http://" + d.self }

// Env sends the web of the program through the door, deck reached directly; it goes last, over the program's own.
func (d *Door) Env() []string { return Proxy("http://deck:"+d.key+"@"+d.self, d.self) }

// Close ends the door and the tunnels through it; a second call does nothing.
func (d *Door) Close() {
	d.once.Do(func() {
		d.cut()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if d.srv.Shutdown(ctx) != nil {
			_ = d.srv.Close()
		}
		_ = os.RemoveAll(d.dir)
	})
}

// authorized reports whether a proxy request carries the key as its password, and answers 407 when not.
func (d *Door) authorized(w http.ResponseWriter, r *http.Request) bool {
	_, pass, _ := (&http.Request{Header: http.Header{"Authorization": r.Header.Values("Proxy-Authorization")}}).BasicAuth()
	if subtle.ConstantTimeCompare([]byte(pass), []byte(d.key)) == 1 {
		return true
	}
	w.Header().Set("Proxy-Authenticate", `Basic realm="deck"`)
	http.Error(w, "deck: the proxy needs the key of a running program", http.StatusProxyAuthRequired)
	return false
}
