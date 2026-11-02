package api

import (
	_ "embed"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
)

// sandboxPage is the outer frame of a custom block: the block's HTML goes to its inner frame.
//
//go:embed sandbox.html
var sandboxPage string

// sandboxCSP keeps a custom block off the network: scripts and styles inline or from the second origin, named, since
// WebKit does not take 'self' for the opaque origin of the block's frame.
const sandboxCSP = "default-src 'none'; script-src 'self' %s 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; " +
	"font-src data:; media-src data: blob:; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors %s"

// Pair makes port sandbox the second origin of the client served on port main of the same host; every pair is made
// before deck serves, as the handlers read them unlocked.
func (s *Server) Pair(main, sandbox int) {
	if s.pairs == nil {
		s.pairs = map[int]int{}
	}
	s.pairs[main] = sandbox
}

// Sandbox serves the second origin: the proxy page of custom blocks and the MCP Apps library for them, nothing else.
func (s *Server) Sandbox() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sandbox.html", func(w http.ResponseWriter, r *http.Request) {
		name, port := s.hostPort(r)
		main := 0
		for m, sb := range s.pairs {
			if sb == port {
				main = m
			}
		}
		if main == 0 {
			http.NotFound(w, r)
			return
		}
		self, origin := s.origin(r, name, port), s.origin(r, name, main)
		w.Header().Set("Content-Security-Policy", fmt.Sprintf(sandboxCSP, self, origin))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprint(w, strings.Replace(sandboxPage, "{{HOST}}", origin, 1))
	})
	mux.HandleFunc("GET /ext-apps.js", func(w http.ResponseWriter, r *http.Request) {
		b, err := fs.ReadFile(s.web, "ext-apps.js")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("Access-Control-Allow-Origin", "*")
		_, _ = w.Write(b)
	})
	return mux
}

// framesOnly keeps the frames of the client on its origins and blobs (a PDF in the viewer of the browser), so no
// frame moves to a page on the network, and its images and media on deck, so markdown cannot fetch them from one.
func (s *Server) framesOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		src := "'self'"
		if name, port := s.hostPort(r); s.pairs[port] != 0 && named(name) {
			src += " " + s.origin(r, name, s.pairs[port])
		}
		w.Header().Set("Content-Security-Policy", "frame-src "+src+" blob:; img-src 'self' data: blob:; media-src 'self' data: blob:; frame-ancestors 'self'")
		next.ServeHTTP(w, r)
	})
}

// custom sends the frame of a custom block to the second origin paired with the port the client came on.
func (s *Server) custom(w http.ResponseWriter, r *http.Request) {
	name, port := s.hostPort(r)
	sb, ok := s.pairs[port]
	if !ok {
		refuse(w, r, "deck: custom blocks need the second port", http.StatusNotFound)
		return
	}
	if !named(name) {
		refuse(w, r, "deck: custom blocks need an address a CSP names, such as localhost or an IPv4 one", http.StatusNotFound)
		return
	}
	http.Redirect(w, r, s.origin(r, name, sb)+"/sandbox.html", http.StatusFound)
}

// local sends a client that came by an address of every interface ([::], 0.0.0.0) or by the IPv6 loopback to
// localhost: a CSP names no IPv6 address, and the second origin of custom blocks must be named.
func (s *Server) local(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name, port := s.hostPort(r)
		ip, err := netip.ParseAddr(strings.Trim(name, "[]"))
		if err != nil || !ip.IsUnspecified() && !(ip.Is6() && ip.IsLoopback()) || r.Method != http.MethodGet && r.Method != http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		http.Redirect(w, r, s.origin(r, "localhost", port)+r.URL.RequestURI(), http.StatusFound)
	})
}

// named says a CSP can name a host: a name or an IPv4 address, not an IPv6 one.
func named(host string) bool {
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return err != nil || ip.Is4()
}

// Proxied says deck is reached through a proxy of HTTPS, such as Caddy of deck's compose, which names the scheme
// of a request in X-Forwarded-Proto; called before deck serves.
func (s *Server) Proxied() { s.proxied = true }

// secure says the client came over HTTPS, to deck or to its proxy.
func (s *Server) secure(r *http.Request) bool {
	return r.TLS != nil || s.proxied && r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *Server) hostPort(r *http.Request) (string, int) {
	name, p, err := net.SplitHostPort(r.Host)
	if err != nil {
		name, p = r.Host, "80"
		if s.secure(r) {
			p = "443"
		}
	}
	port, _ := strconv.Atoi(p)
	return name, port
}

func (s *Server) origin(r *http.Request, name string, port int) string {
	scheme, std := "http", 80
	if s.secure(r) {
		scheme, std = "https", 443
	}
	if port == std {
		return scheme + "://" + name
	}
	return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(name, strconv.Itoa(port)))
}
