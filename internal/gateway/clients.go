package gateway

import (
	"context"
	"net"
	"net/http"
	"time"
)

// Direct is the transport of deck's own calls of a service the owner named: no proxy, a bounded dial and wait for
// the head of an answer, a body as long as it streams.
var Direct = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
	TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 5 * time.Minute, IdleConnTimeout: time.Minute,
	ForceAttemptHTTP2: true}

// Outbound dials only the addresses Pin gave a request, on a connection of its own, since a kept one would serve the
// next request whatever it was pinned to.
var Outbound = &http.Transport{Proxy: nil, DialContext: dialPinned, DisableKeepAlives: true, TLSHandshakeTimeout: 10 * time.Second,
	ResponseHeaderTimeout: 5 * time.Minute}

// The clients of deck: Streams of a long answer such as the stream of a remote MCP server, Short of a call that ends
// soon, Push of a push to a device, Pinned of an HTTP tool, which follows a redirect only on its host.
var (
	Streams = &http.Client{Transport: Direct}
	Short   = &http.Client{Transport: Direct, Timeout: time.Minute}
	Push    = &http.Client{Transport: Direct, Timeout: 15 * time.Second, CheckRedirect: noRedirect}
	Pinned  = &http.Client{Transport: Outbound, Timeout: 5 * time.Minute, CheckRedirect: sameHost}
)

// modelClient calls the API of a model; pageClient gets one page pinned, and Fetch checks each redirect itself.
var (
	modelClient = &http.Client{Transport: Direct, CheckRedirect: noRedirect}
	pageClient  = &http.Client{Transport: Outbound, Timeout: time.Minute, CheckRedirect: noRedirect}
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
