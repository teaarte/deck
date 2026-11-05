package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// Times of the connection of a node: a ping when it has been quiet this long, and the wait for its answer.
const (
	NodePing     = 10 * time.Second
	NodePingWait = 5 * time.Second
)

// NodeHTTP2 is how each end of the connection of a node pings the other, so a side gone without a word ends it.
var NodeHTTP2 = &http.HTTP2Config{SendPingTimeout: NodePing, PingTimeout: NodePingWait}

// ToDeck is the transport of a node to the deck it joins, at the address the owner gave it: HTTP/1.1 only, as its
// connection upgrades to deck's calls.
var ToDeck = func() *http.Transport {
	var p http.Protocols
	p.SetHTTP1(true)
	return &http.Transport{Proxy: nil, Protocols: &p, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: NodePing}).DialContext,
		TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: time.Minute, IdleConnTimeout: time.Minute}
}()

// NodeConn is deck's side of the connection a node opened: HTTP/2 without TLS over it, deck the client, the node
// serving the calls deck sends.
func NodeConn(ctx context.Context, conn net.Conn) (*http.ClientConn, error) {
	var p http.Protocols
	p.SetUnencryptedHTTP2(true)
	given := false
	t := &http.Transport{Proxy: nil, Protocols: &p, HTTP2: NodeHTTP2, ResponseHeaderTimeout: 5 * time.Minute,
		DialContext: func(context.Context, string, string) (net.Conn, error) {
			if given {
				return nil, errors.New("deck: the connection of a node is used already")
			}
			given = true
			return conn, nil
		}}
	return t.NewClientConn(ctx, "http", "node:80")
}

// Loopback is the transport of a node to the services of its own machine: it dials only loopback addresses, as a
// node serves deck those and nothing of its network.
var Loopback = &http.Transport{Proxy: nil, DialContext: dialLoopback, ResponseHeaderTimeout: 5 * time.Minute,
	IdleConnTimeout: time.Minute, TLSHandshakeTimeout: 10 * time.Second}

// DialLoopback dials host:port only when the host is a loopback address or resolves to one.
func DialLoopback(ctx context.Context, addr string) (net.Conn, error) {
	return dialLoopback(ctx, "tcp", addr)
}

func dialLoopback(ctx context.Context, network, addr string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if !ip.Unmap().IsLoopback() {
			return nil, fmt.Errorf("deck: a node reaches only the services of its own machine, and %s is %s", host, ip.Unmap())
		}
	}
	var last error = fmt.Errorf("deck: %s resolves to no address", host)
	for _, ip := range ips {
		c, err := (&net.Dialer{Timeout: 10 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.Unmap().String(), port))
		if err == nil {
			return c, nil
		}
		last = err
	}
	return nil, last
}

// IsLoopback says a host names the machine itself: localhost or a loopback address.
func IsLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip, err := netip.ParseAddr(host)
	return err == nil && ip.Unmap().IsLoopback()
}
