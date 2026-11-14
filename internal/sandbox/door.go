package sandbox

import (
	"context"
	"crypto/subtle"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"deck2/internal/egress"
	"deck2/internal/nonce"
)

// Door is the one way out of a box: a proxy with a key of its own to its domains, and deck's side at its address.
type Door struct {
	ln        net.Listener
	dirs      []string
	self, key string
	srv       *http.Server
	cut       context.CancelFunc
	once      sync.Once
	mu        sync.Mutex
	refused   []string
	// reach is told each host a request of the program reached through the door.
	reach func(host string)
}

// Reacher is deck's side of a door that is told each host a program of the box reached.
type Reacher interface{ Reach(host string) }

// open opens a door in the folder of doors: a request with the key goes through egress to domains, reach hearing its
// host, none without domains; deck serves a path under the key; a request without the key gets 407.
func (s *Sandbox) open(domains []string, deck http.Handler, reach func(host string)) (*Door, error) {
	if err := os.MkdirAll(s.home.Doors(), 0o700); err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp(s.home.Doors(), "")
	if err != nil {
		return nil, err
	}
	ln, short, err := s.listen(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return nil, err
	}
	life, cut := context.WithCancel(context.Background())
	d := &Door{ln: ln, dirs: []string{dir, short}, self: s.addr(ln), key: nonce.Hex(16), cut: cut, reach: reach}
	d.srv = &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect && (!r.URL.IsAbs() || r.URL.Host == d.self) {
			if r, ok := d.under(w, r); ok {
				deck.ServeHTTP(w, r)
			}
			return
		}
		if !d.authorized(w, r) {
			return
		}
		if len(domains) == 0 {
			d.refuse(r.Host + " is not a host this module holds the network of; it declares it under network in rights, and the owner grants it")
			http.Error(w, "deck: "+r.Host+" is not reachable from this program", http.StatusForbidden)
			return
		}
		if err := egress.Proxy(life, w, r, domains, d.reach); err != nil {
			d.refuse(err.Error())
		}
	})}
	go func() { _ = d.srv.Serve(ln) }()
	return d, nil
}

// URL is the address the boxed program reaches deck at, the key its first part.
func (d *Door) URL() string { return "http://" + d.self + "/" + d.key }

// Env sends the web of the program through the door, deck reached directly; it goes last, over the program's own.
func (d *Door) Env() []string { return Proxy("http://deck:"+d.key+"@"+d.self, d.self) }

// Close ends the door and the tunnels through it; a second call, or one without a door, does nothing.
func (d *Door) Close() {
	if d == nil {
		return
	}
	d.once.Do(func() {
		d.cut()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if d.srv.Shutdown(ctx) != nil {
			_ = d.srv.Close()
		}
		for _, dir := range d.dirs {
			if dir != "" {
				_ = os.RemoveAll(dir)
			}
		}
	})
}

// refuse keeps a refusal once, the first few.
func (d *Door) refuse(why string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if len(d.refused) < 3 && !slices.Contains(d.refused, why) {
		d.refused = append(d.refused, why)
	}
}

// Refused are the reasons the door turned the program's requests away, the first few.
func (d *Door) Refused() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.refused)
}

// under is a request to deck with the key taken off its path; without the key it answers 407.
func (d *Door) under(w http.ResponseWriter, r *http.Request) (*http.Request, bool) {
	seg, rest, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"), "/")
	if subtle.ConstantTimeCompare([]byte(seg), []byte(d.key)) != 1 {
		deny(w)
		return nil, false
	}
	r = r.Clone(r.Context())
	r.URL.Path = "/" + rest
	if r.URL.RawPath != "" {
		r.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, "/"+d.key)
	}
	return r, true
}

// authorized reports whether a proxy request carries the key as its password, and answers 407 when not.
func (d *Door) authorized(w http.ResponseWriter, r *http.Request) bool {
	_, pass, _ := (&http.Request{Header: http.Header{"Authorization": r.Header.Values("Proxy-Authorization")}}).BasicAuth()
	if subtle.ConstantTimeCompare([]byte(pass), []byte(d.key)) == 1 {
		return true
	}
	deny(w)
	return false
}

func deny(w http.ResponseWriter) {
	w.Header().Set("Proxy-Authenticate", `Basic realm="deck"`)
	http.Error(w, "deck: the door needs the key of a running program", http.StatusProxyAuthRequired)
}
