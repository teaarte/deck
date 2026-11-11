package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"deck2/internal/auth"
	"deck2/internal/registry"
)

// Refusals of who a request comes from.
var (
	errUnsigned = registry.Unsigned("sign in on this device, or call with a token of deck in the header Authorization")
	errOrigin   = registry.Refuse("a call that changes deck comes only from deck's own pages")
	errTries    = registry.Limited("too many tries from this address; wait a few minutes")
)

// Who is the one place that says whom a call comes from: a lent or automation token in the header (a wrong one counts
// against its address), else a browser's cookie where it signs in, a write only from deck's origin, a read its page.
func (s *Server) Who(w http.ResponseWriter, r *http.Request) (registry.Actor, error) {
	if raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		if act, ok := s.app.Owners.Lent(raw); ok {
			return act, nil
		} else if s.app.Owners.IsLent(raw) {
			return registry.Actor{}, s.unsigned(r)
		}
		act, err := s.app.Owners.Automation(r.Context(), raw)
		if err == nil {
			return act, nil
		}
		if !s.app.Auth.Misses.Allow(s.clientIP(r)) {
			return registry.Actor{}, errTries
		}
		return registry.Actor{}, s.unsigned(r)
	}
	o := originOf(r)
	c, err := r.Cookie(cookieOf(o))
	if err != nil || !o.Passkeys() {
		return registry.Actor{}, s.unsigned(r)
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		if r.Header.Get("Origin") != o.Main {
			return registry.Actor{}, errOrigin
		}
	} else if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		return registry.Actor{}, errOrigin
	}
	p, ok := s.app.Auth.Session(r.Context(), c.Value)
	if !ok || p.Kind != auth.Browser {
		return registry.Actor{}, s.unsigned(r)
	}
	if p.Unsettled {
		var mark string
		if p, mark, err = s.app.Auth.Settle(r.Context(), c.Value, client(r)); err != nil {
			return registry.Actor{}, s.unsigned(r)
		}
		keepMark(w, r, mark)
	}
	return p.Actor(), nil
}

// unsigned refuses a call of no one; at an address where no browser signs in it names those where one does.
func (s *Server) unsigned(r *http.Request) error {
	if originOf(r).Passkeys() {
		return errUnsigned
	}
	var at []string
	for _, o := range s.origins.List() {
		if o.Passkeys() {
			at = append(at, o.Main)
		}
	}
	if len(at) == 0 {
		return registry.Unsigned("a browser signs in to deck only over https or at deck.localhost on its machine, and deck has neither here: see Health for how to give deck an https address")
	}
	return registry.Unsigned("a browser signs in to deck at %s", strings.Join(at, " or "))
}

// markOf is the cookie a browser keeps the mark of its device in, beside that of its session.
func markOf(o auth.Origin) string { return cookieOf(o) + "-device" }

// cookieOf is the cookie of a browser's session at an origin: __Host- over https, so it stays with the one origin;
// at deck.localhost one named by the port, as a browser sends it to every port and another deck may listen on one.
func cookieOf(o auth.Origin) string {
	if o.Secure() {
		return "__Host-deck"
	}
	u, _ := url.Parse(o.Main)
	return "deck-" + u.Port()
}

type originKey struct{}

// originOf is the origin of deck a request came to.
func originOf(r *http.Request) auth.Origin {
	o, _ := r.Context().Value(originKey{}).(auth.Origin)
	return o
}

// hosted answers only at deck's own addresses, by the Host of a request, so a name an attacker's DNS points at deck
// gets nothing; [::], 0.0.0.0 and [::1] go to localhost, as a CSP names no IPv6 address.
func (s *Server) hosted(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Referrer-Policy", "no-referrer")
		if strings.HasPrefix(r.URL.Path, "/mcp/") {
			next.ServeHTTP(w, r)
			return
		}
		name, port := hostPort(r.Host)
		if here := s.here(r, name, port); here != "" {
			http.Redirect(w, r, here+r.URL.RequestURI(), http.StatusFound)
			return
		}
		o, ok := s.origins.Of(r.Host)
		if !ok {
			s.reached(r, name)
			http.Error(w, "deck: deck answers only at its own addresses: its https address, or deck.localhost on its machine", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), originKey{}, o)))
	})
}

// here is deck.localhost at the port of a page opened by localhost or a loopback address, where no browser signs in,
// or [::] and 0.0.0.0, which a CSP does not name; empty for the rest and for calls of the API, MCP and signing in.
func (s *Server) here(r *http.Request, name string, port int) string {
	p := r.URL.Path
	if r.Method != http.MethodGet && r.Method != http.MethodHead || port == 0 || p == "/mcp" || strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/auth/") {
		return ""
	}
	ip, err := netip.ParseAddr(strings.Trim(name, "[]"))
	if name != "localhost" && (err != nil || !ip.IsLoopback() && !ip.IsUnspecified()) {
		return ""
	}
	here := "http://" + net.JoinHostPort(auth.HereName, strconv.Itoa(port))
	if _, ok := s.origins.Named(here); !ok {
		return ""
	}
	return here
}

// reached tells deck the first time the owner comes by an address deck does not sign in at, such as plain http from
// another machine.
func (s *Server) reached(r *http.Request, name string) {
	if ip, err := netip.ParseAddr(name); name == "localhost" || err == nil && ip.Unmap().IsLoopback() || !s.plain.CompareAndSwap(false, true) {
		return
	}
	port := 0
	if la, ok := r.Context().Value(http.LocalAddrContextKey).(*net.TCPAddr); ok {
		port = la.Port
	}
	s.app.Plain(name, port)
}

func hostPort(host string) (string, int) {
	name, p, err := net.SplitHostPort(host)
	if err != nil {
		return host, 0
	}
	port, _ := strconv.Atoi(p)
	return name, port
}

// clientIP is the address a request comes from: the one a proxy saw when it came from a proxy deck was told of, as
// Caddy of deck's compose, else its own.
func (s *Server) clientIP(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip, err := netip.ParseAddr(host)
	if err != nil || r.TLS != nil || !s.proxies.Has(r.Context(), ip.Unmap()) {
		return host
	}
	fwd := r.Header.Values("X-Forwarded-For")
	if len(fwd) == 0 {
		return host
	}
	parts := strings.Split(fwd[len(fwd)-1], ",")
	if last := strings.TrimSpace(parts[len(parts)-1]); last != "" {
		return last
	}
	return host
}
