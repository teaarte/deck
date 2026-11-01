package provider

import (
	"net/http"
	"net/http/httputil"
	"net/url"
)

// MCP is deck's MCP server of a turn at the door of the turn's box: what the program sends to the path of at goes on
// to at, the gateway on deck's own port, which the box does not reach.
func MCP(at *url.URL) http.Handler {
	proxy := &httputil.ReverseProxy{FlushInterval: -1, Rewrite: func(pr *httputil.ProxyRequest) {
		pr.SetURL(&url.URL{Scheme: at.Scheme, Host: at.Host})
		pr.Out.URL.Path, pr.Out.URL.RawPath = at.Path, ""
	}}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != at.Path {
			http.Error(w, "deck: not found", http.StatusNotFound)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}
