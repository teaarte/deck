package node

import (
	"context"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"time"

	"deck2/internal/egress"
)

// Back is deck's side of the turns a node runs, at an address of the node's own loopback: a call of
// <URL>/<token>/<path> goes to the side of that turn on deck, by the key of the node.
type Back struct {
	URL string
	srv *http.Server
}

// ServeBack serves deck's side of the turns of the node joined by j on its loopback until Close.
func ServeBack(j Joined) (*Back, error) {
	deck, err := url.Parse(j.Deck)
	if err != nil {
		return nil, err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	proxy := &httputil.ReverseProxy{Transport: egress.FromNode, FlushInterval: -1,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL = &url.URL{Scheme: deck.Scheme, Host: deck.Host, Path: SidePath + strings.TrimPrefix(pr.In.URL.Path, "/"), RawQuery: pr.In.URL.RawQuery}
			pr.Out.Host = deck.Host
			pr.Out.Header.Set("Authorization", "Bearer "+j.Key)
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, "deck node: deck is not reached: "+err.Error(), http.StatusBadGateway)
		}}
	b := &Back{URL: "http://" + ln.Addr().String(), srv: &http.Server{Handler: proxy, ReadHeaderTimeout: 10 * time.Second}}
	go func() { _ = b.srv.Serve(ln) }()
	return b, nil
}

// Close ends deck's side on the node's loopback.
func (b *Back) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if b.srv.Shutdown(ctx) != nil {
		_ = b.srv.Close()
	}
}
