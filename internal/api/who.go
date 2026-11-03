package api

import (
	"context"
	"net"
	"net/http"
	"net/netip"
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

// Who is the one place that says whom a call comes from: a lent, launcher's or automation token in the header, else
// a browser's cookie over https, a write with it only from deck's origin and a read from its page.
func (s *Server) Who(r *http.Request) (registry.Actor, error) {
	if raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
		if act, ok := s.app.Owners.Lent(raw); ok {
			return act, nil
		}
		if p, ok := s.app.Auth.Session(r.Context(), raw); ok && p.Kind == auth.Launcher && local(r) {
			return p.Actor(), nil
		}
		ip := s.clientIP(r)
		if s.app.Auth.Misses.Over(ip) {
			return registry.Actor{}, errTries
		}
		act, err := s.app.Owners.Automation(r.Context(), raw)
		if err != nil {
			s.app.Auth.Misses.Allow(ip)
			return registry.Actor{}, s.unsigned(r)
		}
		return act, nil
	}
	o := originOf(r)
	c, err := r.Cookie(cookieName)
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
		return registry.Unsigned("a browser signs in to deck only over https, and deck has no https address yet: open it in the window of the launcher, or see Health for how to give deck one")
	}
	return registry.Unsigned("a browser signs in to deck only over https: open %s, or the window of the launcher", strings.Join(at, " or "))
}

// local says a request comes from this machine to deck's plain address on it, as the launcher's window does; not
// through a proxy here, such as tailscale serve, which comes from loopback to an https address.
func local(r *http.Request) bool {
	name, _ := hostPort(r.Host)
	return fromHere(r) && !originOf(r).Secure() && (name == "localhost" || strings.HasPrefix(name, "127."))
}

// cookieName is the cookie of a browser's session, __Host- so it stays with the one https origin.
const cookieName = "__Host-deck"

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
		if ip, err := netip.ParseAddr(strings.Trim(name, "[]")); err == nil && (ip.IsUnspecified() || ip.Is6() && ip.IsLoopback()) &&
			(r.Method == http.MethodGet || r.Method == http.MethodHead) {
			http.Redirect(w, r, "http://"+net.JoinHostPort("localhost", strconv.Itoa(port))+r.URL.RequestURI(), http.StatusFound)
			return
		}
		o, ok := s.origins.Of(r.Host)
		if !ok {
			s.reached(r, name)
			http.Error(w, "deck: deck answers only at its own addresses: its https address, or localhost in the window of the launcher", http.StatusMisdirectedRequest)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), originKey{}, o)))
	})
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

// clientIP is the address a request comes from: the one a proxy of deck saw, when it came for an https address of
// deck over plain http from this machine or deck's private network, as through Caddy of deck's compose.
func (s *Server) clientIP(r *http.Request) string {
	host, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip, err := netip.ParseAddr(host)
	if err != nil || !(ip.Unmap().IsLoopback() || ip.Unmap().IsPrivate()) || r.TLS != nil || !originOf(r).Secure() {
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
