package gateway

import (
	"context"
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

var outbound = &http.Transport{Proxy: nil, DialContext: (&net.Dialer{Timeout: 10 * time.Second}).DialContext,
	ResponseHeaderTimeout: 5 * time.Minute}

// Reachable reports whether host:port is in the domains: a host with its subdomains on any port, an exact
// host:port, an IP, a network such as 192.168.1.0/24, or "*" for any host that has no local address.
func Reachable(domains []string, hostport string) bool {
	if listed(domains, hostport) {
		return true
	}
	if !wildcard(domains) {
		return false
	}
	host, _, _ := net.SplitHostPort(hostport)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ips, _ := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	return !slices.ContainsFunc(ips, func(ip netip.Addr) bool { return !Public(ip) })
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

// Egress carries a proxy request of a confined program to a host in the domains; tunnels close when ctx ends.
// A host reached only by "*" must resolve to public addresses and is dialled as resolved.
func Egress(ctx context.Context, w http.ResponseWriter, r *http.Request, domains []string) {
	target := r.Host
	if r.Method != http.MethodConnect {
		port := r.URL.Port()
		if port == "" {
			port = map[string]string{"http": "80", "https": "443"}[r.URL.Scheme]
		}
		target = net.JoinHostPort(r.URL.Hostname(), port)
	}
	if !listed(domains, target) {
		if !wildcard(domains) {
			http.Error(w, "deck: "+target+" is not in the domains of this project", http.StatusForbidden)
			return
		}
		pinned, err := public(r.Context(), target)
		if err != nil {
			http.Error(w, "deck: "+err.Error(), http.StatusForbidden)
			return
		}
		target = pinned
		if r.Method != http.MethodConnect {
			r.URL.Host = pinned
		}
	}
	if r.Method == http.MethodConnect {
		tunnel(ctx, w, target)
		return
	}
	forward.ServeHTTP(w, r)
}

// public resolves host:port once and gives the address to dial, when every address of the host is public.
func public(ctx context.Context, hostport string) (string, error) {
	host, port, _ := net.SplitHostPort(hostport)
	ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
	if err != nil || len(ips) == 0 {
		return "", fmt.Errorf("%s does not resolve", host)
	}
	for _, ip := range ips {
		if !Public(ip) {
			return "", fmt.Errorf("%s is not a public address; local services are reached by naming their IP, network or host:port", host)
		}
	}
	return net.JoinHostPort(ips[0].Unmap().String(), port), nil
}

// forward carries a plain HTTP proxy request as it is, without hop-by-hop headers either way.
var forward = &httputil.ReverseProxy{Transport: outbound, Rewrite: func(*httputil.ProxyRequest) {},
	ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
		http.Error(w, "deck: "+err.Error(), http.StatusBadGateway)
	}}

func tunnel(ctx context.Context, w http.ResponseWriter, target string) {
	up, err := net.DialTimeout("tcp", target, 10*time.Second)
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
