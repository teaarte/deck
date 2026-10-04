package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"slices"
	"strings"
	"time"
)

var outbound = &http.Transport{Proxy: nil, DialContext: dialPinned, ResponseHeaderTimeout: 5 * time.Minute}

type pinnedKey struct{}

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

// Reachable reports whether a page at host:port is in the domains: a host with its subdomains on any port, an
// exact host:port, an IP, a network such as 192.168.1.0/24, or "*" for any host. A name reaches only public
// addresses; a listed name that does not resolve passes, as nothing loads from it.
func Reachable(domains []string, hostport string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := destination(ctx, domains, hostport)
	return err == nil || errors.Is(err, errUnresolved) && listed(domains, hostport)
}

var errUnresolved = errors.New("does not resolve")

// destination resolves host:port once and gives the addresses to dial, when the domains let it go there.
func destination(ctx context.Context, domains []string, hostport string) ([]string, error) {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return nil, err
	}
	if !listed(domains, hostport) && !wildcard(domains) {
		return nil, fmt.Errorf("%s is not in the domains of this project", hostport)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !Public(ip) && !listed(domains, hostport) {
			return nil, fmt.Errorf("%s is not a public address; local services are reached by naming their IP, network or IP:port", host)
		}
		return []string{hostport}, nil
	}
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return nil, fmt.Errorf("%s %w", host, errUnresolved)
	}
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		addr := net.JoinHostPort(ip.Unmap().String(), port)
		if !Public(ip) && !listed(domains, addr) {
			return nil, fmt.Errorf("%s resolves to %s, not a public address; local services are reached by naming their IP, network or IP:port", host, ip.Unmap())
		}
		out = append(out, addr)
	}
	return out, nil
}

func wildcard(domains []string) bool {
	return slices.ContainsFunc(domains, func(d string) bool { return strings.TrimSpace(d) == "*" })
}

// listed reports whether host:port is named in the domains other than by "*".
func listed(domains []string, hostport string) bool {
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		return false
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	ip, ipErr := netip.ParseAddr(host)
	for _, d := range domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "*" {
			continue
		}
		if pfx, err := netip.ParsePrefix(d); err == nil {
			if ipErr == nil && pfx.Contains(ip) {
				return true
			}
			continue
		}
		if h, p, err := net.SplitHostPort(d); err == nil {
			if strings.Trim(h, "[]") == host && p == port {
				return true
			}
			continue
		}
		d = strings.Trim(d, "[]")
		if host == d || ipErr != nil && strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// Public reports whether an address is on the internet: not loopback, the local network or a tailnet.
func Public(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsGlobalUnicast() && !ip.IsPrivate() && !shared.Contains(ip)
}

// shared is the carrier-grade NAT range, which tailnets use.
var shared = netip.MustParsePrefix("100.64.0.0/10")

// Egress carries a proxy request of a confined program to a host in the domains, dialled at the addresses it
// resolved to once; tunnels close when ctx ends.
func Egress(ctx context.Context, w http.ResponseWriter, r *http.Request, domains []string) {
	target := r.Host
	if r.Method != http.MethodConnect {
		port := r.URL.Port()
		if port == "" {
			port = map[string]string{"http": "80", "https": "443"}[r.URL.Scheme]
		}
		target = net.JoinHostPort(r.URL.Hostname(), port)
	}
	pinned, err := destination(r.Context(), domains, target)
	if err != nil {
		http.Error(w, "deck: "+err.Error(), http.StatusForbidden)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), pinnedKey{}, pinned))
	if r.Method == http.MethodConnect {
		tunnel(ctx, w, r, target)
		return
	}
	forward.ServeHTTP(w, r)
}

// forward carries a plain HTTP proxy request as it is, without hop-by-hop headers either way.
var forward = &httputil.ReverseProxy{Transport: outbound, Rewrite: func(*httputil.ProxyRequest) {},
	ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, "deck: "+err.Error(), http.StatusBadGateway)
	}}

func tunnel(ctx context.Context, w http.ResponseWriter, r *http.Request, target string) {
	up, err := dialPinned(r.Context(), "tcp", target)
	if err != nil {
		http.Error(w, "deck: "+err.Error(), http.StatusBadGateway)
		return
	}
	defer up.Close()
	hj, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "deck: cannot tunnel", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	c, buf, err := hj.Hijack()
	if err != nil {
		return
	}
	defer c.Close()
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, buf); done <- struct{}{} }()
	go func() { _, _ = io.Copy(c, up); done <- struct{}{} }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}
