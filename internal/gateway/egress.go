package gateway

import (
	"context"
	"deck2/internal/registry"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"time"
)

// Outbound dials only the addresses Pin gave a request, on a connection of its own, since a kept one would serve the
// next request whatever it was pinned to.
var Outbound = &http.Transport{Proxy: nil, DialContext: dialPinned, DisableKeepAlives: true, ResponseHeaderTimeout: 5 * time.Minute}

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

// Reachable reports whether a page at host:port is in the domains (host with subdomains, host:port, IP, network,
// "*"); a name reaches only public addresses, and a listed name that does not resolve passes: nothing loads.
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
		return nil, fmt.Errorf("%s is not in the domains of this project; its manifest names it in domains.read and the owner grants it", hostport)
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
// resolved to once; tunnels close when ctx ends. The error is why it refused: 502 for a name that does not resolve.
func Egress(ctx context.Context, w http.ResponseWriter, r *http.Request, domains []string) error {
	target := r.Host
	if r.Method != http.MethodConnect {
		port := r.URL.Port()
		if port == "" {
			port = map[string]string{"http": "80", "https": "443"}[r.URL.Scheme]
		}
		target = net.JoinHostPort(r.URL.Hostname(), port)
	}
	pinned, err := destination(r.Context(), domains, target)
	if errors.Is(err, errUnresolved) {
		err = fmt.Errorf("%w; the network or DNS of the machine may be down", err)
		http.Error(w, "deck: "+err.Error(), http.StatusBadGateway)
		return err
	}
	if err != nil {
		http.Error(w, "deck: "+err.Error(), http.StatusForbidden)
		return err
	}
	r = r.WithContext(context.WithValue(r.Context(), pinnedKey{}, pinned))
	if r.Method == http.MethodConnect {
		tunnel(ctx, w, r, target)
		return nil
	}
	forward.ServeHTTP(w, r)
	return nil
}

// pageClient gets one page at the addresses its request was pinned to; Fetch checks each redirect itself.
var pageClient = &http.Client{Transport: Outbound, Timeout: time.Minute,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

// Pin is req bound to the addresses its host resolved to once, when the domains let it go there; Outbound dials only
// them, a redirect of the same host too. A name that does not resolve is 502, a place the domains refuse 403.
func Pin(req *http.Request, domains []string) (*http.Request, error) {
	port := req.URL.Port()
	if port == "" {
		port = map[string]string{"http": "80", "https": "443"}[req.URL.Scheme]
	}
	pinned, err := destination(req.Context(), domains, net.JoinHostPort(req.URL.Hostname(), port))
	if errors.Is(err, errUnresolved) {
		return nil, &registry.Error{Status: http.StatusBadGateway, Text: err.Error() + "; the network or DNS of the machine may be down"}
	}
	if err != nil {
		return nil, &registry.Error{Status: http.StatusForbidden, Text: err.Error()}
	}
	return req.WithContext(context.WithValue(req.Context(), pinnedKey{}, pinned)), nil
}

// Fetch gets a page in the domains the way Egress carries one, at the addresses its name resolved to once; the
// place a redirect names is checked the same way.
func Fetch(ctx context.Context, domains []string, page string) (*http.Response, error) {
	for range 6 {
		u, err := url.Parse(page)
		if err != nil || u.Scheme != "http" && u.Scheme != "https" || u.Hostname() == "" {
			return nil, fmt.Errorf("%q is not an http or https address", page)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
		if err != nil {
			return nil, err
		}
		if req, err = Pin(req, domains); err != nil {
			return nil, err
		}
		req.Header.Set("Accept", "text/html,text/plain,application/json;q=0.9,*/*;q=0.5")
		resp, err := pageClient.Do(req)
		if err != nil {
			return nil, err
		}
		next, err := resp.Location()
		if resp.StatusCode < 300 || resp.StatusCode > 399 || err != nil {
			return resp, nil
		}
		_ = resp.Body.Close()
		page = next.String()
	}
	return nil, errors.New("too many redirects")
}

// forward carries a plain HTTP proxy request as it is, without hop-by-hop headers either way.
var forward = &httputil.ReverseProxy{Transport: Outbound, Rewrite: func(*httputil.ProxyRequest) {},
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
