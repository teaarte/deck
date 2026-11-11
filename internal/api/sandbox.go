package api

import (
	_ "embed"
	"fmt"
	"io/fs"
	"net/http"
	"net/netip"
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

// Sandbox serves the second origin: the proxy page of custom blocks and the MCP Apps library for them, nothing else.
// It reads no cookie and passes none on.
func (s *Server) Sandbox() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /sandbox.html", func(w http.ResponseWriter, r *http.Request) {
		o, ok := s.origins.OfSide(r.Host)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Security-Policy", fmt.Sprintf(sandboxCSP, o.Side, o.Main))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		fmt.Fprint(w, strings.Replace(sandboxPage, "{{HOST}}", o.Main, 1))
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
	// The browser of deck's shots sends its token with every request, so a block's frame asks before it loads the
	// library.
	mux.HandleFunc("OPTIONS /ext-apps.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "authorization")
		w.WriteHeader(http.StatusNoContent)
	})
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Header.Del("Cookie")
		w.Header().Set("Referrer-Policy", "no-referrer")
		mux.ServeHTTP(w, r)
	})
}

// framesOnly keeps the frames of the client on its origins and blobs (a PDF in the viewer of the browser), its images
// and media on deck, and its scripts to deck's files (WebAssembly for the PDF worker), no inline or injected one.
func (s *Server) framesOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		src := "'self'"
		if o := originOf(r); o.Side != "" && named(o.RPID()) {
			src += " " + o.Side
		}
		w.Header().Set("Content-Security-Policy", "script-src 'self' 'wasm-unsafe-eval'; object-src 'none'; base-uri 'none'; "+
			"frame-src "+src+" blob:; img-src 'self' data: blob:; media-src 'self' data: blob:; frame-ancestors 'self'")
		next.ServeHTTP(w, r)
	})
}

// custom sends the frame of a custom block to the second origin of the origin the client came to.
func (s *Server) custom(w http.ResponseWriter, r *http.Request) {
	o := originOf(r)
	if o.Side == "" {
		refuse(w, r, "deck: custom blocks need the second port", http.StatusNotFound)
		return
	}
	if !named(o.RPID()) {
		refuse(w, r, "deck: custom blocks need an address a CSP names, such as localhost or an IPv4 one", http.StatusNotFound)
		return
	}
	http.Redirect(w, r, o.Side+"/sandbox.html", http.StatusFound)
}

// named says a CSP can name a host: a name or an IPv4 address, not an IPv6 one.
func named(host string) bool {
	ip, err := netip.ParseAddr(strings.Trim(host, "[]"))
	return err != nil || ip.Is4()
}
