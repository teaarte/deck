package harness

import (
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
)

// secure is the https address of a test deck: TLS before deck and before its second origin, as tailscale serve or
// Caddy put it; a browser of a test takes its certificate as it is.
type secure struct {
	once       sync.Once
	main, side *httptest.Server
}

func (s *secure) close() {
	if s != nil && s.main != nil {
		s.main.CloseClientConnections()
		s.main.Close()
		s.side.Close()
	}
}

// Https is the https address of deck at localhost, where a browser pairs with a passkey and signs in by a cookie;
// deck takes it as an origin of its own on the first call.
func (e *Env) Https() string {
	e.T.Helper()
	if e.secure == nil {
		e.secure = &secure{}
	}
	e.secure.once.Do(func() {
		e.secure.main, e.secure.side = front(e.URL), front(e.Side)
		e.App.Auth.Origins().Add(local(e.secure.main), local(e.secure.side))
	})
	return local(e.secure.main)
}

// front is a TLS server before the server at base; deck sees the Host the browser named. A browser's first refusal
// of the certificate of the test is no news.
func front(base string) *httptest.Server {
	to, _ := url.Parse(base)
	ts := httptest.NewUnstartedServer(&httputil.ReverseProxy{Rewrite: func(r *httputil.ProxyRequest) {
		r.SetURL(to)
		r.Out.Host = r.In.Host
	}, FlushInterval: -1})
	ts.Config.ErrorLog = log.New(io.Discard, "", 0)
	ts.StartTLS()
	return ts
}

// local is the address of a server of the test by the name localhost.
func local(ts *httptest.Server) string { return strings.Replace(ts.URL, "127.0.0.1", "localhost", 1) }

// secureHost signs a request for the https address of deck: its Host and the Origin of its page.
func (e *Env) secureHost(r *http.Request) {
	u, _ := url.Parse(e.Https())
	r.Host = u.Host
	if r.Header.Get("Origin") == "" {
		r.Header.Set("Origin", e.Https())
	}
	r.Header.Set("Sec-Fetch-Site", "same-origin")
}
