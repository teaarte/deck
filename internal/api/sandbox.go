package api

import (
	_ "embed"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"strings"
)

// sandboxPage is the outer frame of a custom block: the block's HTML goes to its inner frame.
//
//go:embed sandbox.html
var sandboxPage string

// sandboxCSP keeps a custom block off the network: scripts and styles inline or from the second origin itself.
const sandboxCSP = "default-src 'none'; script-src 'self' 'unsafe-inline'; style-src 'unsafe-inline'; img-src data: blob:; " +
	"font-src data:; media-src data: blob:; connect-src 'none'; form-action 'none'; base-uri 'none'; frame-ancestors %s"

// Pair makes port sandbox the second origin of the client served on port main of the same host.
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
		name, port := hostPort(r)
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
		origin := origin(r, name, main)
		w.Header().Set("Content-Security-Policy", fmt.Sprintf(sandboxCSP, origin))
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
		_, _ = w.Write(b)
	})
	return mux
}

// framesOnly keeps the frames of the client on its own origin and its second one, so a custom block cannot move
// its frame to a page on the network, and its images and media on deck, so markdown cannot fetch them from one.
func (s *Server) framesOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		src := "'self'"
		if name, port := hostPort(r); s.pairs[port] != 0 {
			src += " " + origin(r, name, s.pairs[port])
		}
		w.Header().Set("Content-Security-Policy", "frame-src "+src+"; img-src 'self' data: blob:; media-src 'self' data: blob:; frame-ancestors 'self'")
		next.ServeHTTP(w, r)
	})
}

// custom sends the frame of a custom block to the second origin paired with the port the client came on.
func (s *Server) custom(w http.ResponseWriter, r *http.Request) {
	name, port := hostPort(r)
	sb, ok := s.pairs[port]
	if !ok {
		http.Error(w, "deck: custom blocks need the second port", http.StatusNotFound)
		return
	}
	http.Redirect(w, r, origin(r, name, sb)+"/sandbox.html", http.StatusFound)
}

func hostPort(r *http.Request) (string, int) {
	name, p, err := net.SplitHostPort(r.Host)
	if err != nil {
		name, p = r.Host, "80"
		if r.TLS != nil {
			p = "443"
		}
	}
	port, _ := strconv.Atoi(p)
	return name, port
}

func origin(r *http.Request, name string, port int) string {
	scheme, std := "http", 80
	if r.TLS != nil {
		scheme, std = "https", 443
	}
	if port == std {
		return scheme + "://" + name
	}
	return fmt.Sprintf("%s://%s", scheme, net.JoinHostPort(name, strconv.Itoa(port)))
}
