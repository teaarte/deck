package egress

import (
	"context"
	"net"
	"net/http"
	"time"
)

// Direct is the transport of the services the owner named himself, the connections of models: no proxy, a bounded
// dial and wait for the head of an answer, a body as long as it streams.
var Direct = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
	TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 5 * time.Minute, IdleConnTimeout: time.Minute,
	ForceAttemptHTTP2: true}

// Outbound dials the addresses Pin gave a request and no other, on a connection of its own, since a kept one would
// serve the next request whatever it was pinned to.
var Outbound = &http.Transport{Proxy: nil, DialContext: dialPinned, DisableKeepAlives: true, TLSHandshakeTimeout: 10 * time.Second,
	ResponseHeaderTimeout: 5 * time.Minute}

// Client is a client whose requests, redirects too, go where destination lets them by domains, at the addresses a
// name resolved to once; a timeout of 0 lets an answer stream as long as it lasts.
func Client(domains []string, timeout time.Duration) *http.Client {
	return &http.Client{Transport: pinning(func(*http.Request) []string { return domains }), Timeout: timeout, CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 5 {
			return http.ErrUseLastResponse
		}
		return nil
	}}
}

// pinning pins each request to the domains it gives before Outbound carries it.
type pinning func(*http.Request) []string

func (p pinning) RoundTrip(req *http.Request) (*http.Response, error) {
	pinned, err := Pin(req, p(req))
	if err != nil {
		if req.Body != nil {
			_ = req.Body.Close()
		}
		return nil, err
	}
	return Outbound.RoundTrip(pinned)
}

// Pinned is the client of an HTTP tool, whose request Pin bound: it follows a redirect on its host and no other. Push
// is the client of a push to a device, at a public address.
var (
	Pinned = &http.Client{Transport: Outbound, Timeout: 5 * time.Minute, CheckRedirect: sameHost}
	Push   = &http.Client{Transport: pinning(func(r *http.Request) []string { return With(Anywhere, r.URL.String()) }),
		Timeout: 15 * time.Second, CheckRedirect: noRedirect}
)

// pageClient gets one page pinned and fileClient one big file; Fetch checks each redirect itself.
var (
	pageClient = &http.Client{Transport: Outbound, Timeout: time.Minute, CheckRedirect: noRedirect}
	fileClient = &http.Client{Transport: Outbound, Timeout: 30 * time.Minute, CheckRedirect: noRedirect}
)

func noRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

func sameHost(req *http.Request, via []*http.Request) error {
	if len(via) >= 5 || req.URL.Host != via[0].URL.Host || req.URL.Scheme != via[0].URL.Scheme {
		return http.ErrUseLastResponse
	}
	return nil
}

// dialPinned dials the addresses a request was pinned to, in turn, or the address it names.
func dialPinned(ctx context.Context, network, addr string) (net.Conn, error) {
	addrs, _ := ctx.Value(pinnedKey{}).([]string)
	if len(addrs) == 0 {
		addrs = []string{addr}
	}
	var err error
	for _, a := range addrs {
		var c net.Conn
		if c, err = (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, a); err == nil {
			return c, nil
		}
	}
	return nil, err
}
