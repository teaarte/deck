// Package egress is the one way of deck to the web: a name resolves once, a request dials only the addresses it
// resolved to, and a name reaches only public ones.
package egress

import (
	"cmp"
	"context"
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
	"sync"
	"time"

	"deck2/internal/fault"
)

type pinnedKey struct{}

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
		return nil, fmt.Errorf("%s is not a host this module holds the network of; it declares it under network in rights, and the owner grants it", hostport)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		if !Public(ip) && !listed(domains, hostport) && !standing(hostport) {
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
		_, dIP := netip.ParseAddr(d)
		if host == d || ipErr != nil && dIP != nil && strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// Public reports whether an address is on the internet: in no special-purpose range, and for IPv6 in the global
// unicast space 2000::/3; an IPv6 address that carries an IPv4 one is public when that one is.
func Public(ip netip.Addr) bool {
	ip = ip.Unmap()
	if v4, ok := embedded(ip); ok {
		return Public(v4)
	}
	if !ip.IsValid() || ip.Is6() && !global6.Contains(ip) {
		return false
	}
	return !slices.ContainsFunc(special, func(p netip.Prefix) bool { return p.Contains(ip) })
}

var (
	nat64   = netip.MustParsePrefix("64:ff9b::/96")
	sixTo4  = netip.MustParsePrefix("2002::/16")
	global6 = netip.MustParsePrefix("2000::/3")
)

// special are the ranges of no public address, from the IANA IPv4 and IPv6 Special-Purpose Address Registries
// (iana.org/assignments/iana-ipv4-special-registry, iana-ipv6-special-registry, 2025) and multicast.
var special = prefixes(
	"0.0.0.0/8", "10.0.0.0/8", "100.64.0.0/10", "127.0.0.0/8", "169.254.0.0/16", "172.16.0.0/12", "192.0.0.0/24",
	"192.0.2.0/24", "192.88.99.0/24", "192.168.0.0/16", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24",
	"224.0.0.0/4", "240.0.0.0/4",
	"::/8", "64:ff9b:1::/48", "100::/64", "2001::/23", "2001:db8::/32", "3fff::/20", "5f00::/16", "fc00::/7",
	"fe80::/10", "fec0::/10", "ff00::/8",
)

func prefixes(ps ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ps))
	for i, p := range ps {
		out[i] = netip.MustParsePrefix(p)
	}
	return out
}

// embedded is the IPv4 address a NAT64 or 6to4 address reaches.
func embedded(ip netip.Addr) (netip.Addr, bool) {
	b := ip.As16()
	switch {
	case !ip.Is6():
		return netip.Addr{}, false
	case nat64.Contains(ip):
		return netip.AddrFrom4([4]byte(b[12:16])), true
	case sixTo4.Contains(ip):
		return netip.AddrFrom4([4]byte(b[2:6])), true
	}
	return netip.Addr{}, false
}

// Local reports whether a place of domains reaches beyond the internet: an address or network that is not public,
// such as 10.0.0.0/8, or one that holds such addresses, such as 0.0.0.0/0.
func Local(domain string) bool {
	d := strings.Trim(strings.ToLower(strings.TrimSpace(domain)), "[]")
	if h, _, err := net.SplitHostPort(strings.TrimSpace(domain)); err == nil {
		d = strings.Trim(h, "[]")
	}
	if ip, err := netip.ParseAddr(d); err == nil {
		return !Public(ip)
	}
	pfx, err := netip.ParsePrefix(d)
	if err != nil {
		return d == "localhost" || strings.HasSuffix(d, ".localhost")
	}
	pfx = pfx.Masked()
	return !Public(pfx.Addr()) || slices.ContainsFunc(special, pfx.Overlaps) || pfx.Overlaps(nat64) || pfx.Overlaps(sixTo4)
}

// With is domains and the host of a URL a manifest names, such as of an HTTP tool or an MCP server: a name, or an
// address that is public; any other address the domains name themselves.
func With(domains []string, page string) []string {
	u, err := url.Parse(page)
	if err != nil || u.Hostname() == "" {
		return domains
	}
	ip, err := netip.ParseAddr(u.Hostname())
	switch {
	case err != nil || Public(ip):
		return append([]string{u.Hostname()}, domains...)
	case standing(net.JoinHostPort(ip.String(), port(u))):
		return append([]string{net.JoinHostPort(ip.String(), port(u))}, domains...)
	}
	return domains
}

// port is the port of a URL, by its scheme when it names none.
func port(u *url.URL) string {
	return cmp.Or(u.Port(), map[string]string{"http": "80", "https": "443"}[u.Scheme])
}

// Anywhere are the domains of every public place.
var Anywhere = []string{"*"}

var stand sync.Map

// AsPublic has deck take the address host:port for a public one where a URL names it or a program dials it: a
// test's service on loopback stands for one on the internet.
func AsPublic(hostport string) { stand.Store(hostport, true) }

func standing(hostport string) bool {
	_, ok := stand.Load(hostport)
	return ok
}

// Proxy carries a proxy request of a confined program to a host in the domains, dialled at the addresses it
// resolved to once; tunnels close when ctx ends. The error is why it refused: 502 for a name that does not resolve.
func Proxy(ctx context.Context, w http.ResponseWriter, r *http.Request, domains []string) error {
	target := r.Host
	if r.Method != http.MethodConnect {
		target = net.JoinHostPort(r.URL.Hostname(), port(r.URL))
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

// Pin is req bound to the addresses its host resolved to once, when the domains let it go there; Outbound dials only
// them, a redirect of the same host too. A name that does not resolve is 502, a place the domains refuse 403.
func Pin(req *http.Request, domains []string) (*http.Request, error) {
	pinned, err := destination(req.Context(), domains, net.JoinHostPort(req.URL.Hostname(), port(req.URL)))
	if errors.Is(err, errUnresolved) {
		return nil, fault.New(fault.Network, err.Error()+"; the network or DNS of the machine may be down")
	}
	if err != nil {
		return nil, fault.New(fault.RightsDenied, err.Error())
	}
	return req.WithContext(context.WithValue(req.Context(), pinnedKey{}, pinned)), nil
}

// Fetch gets a page in the domains the way Egress carries one, at the addresses its name resolved to once; the
// place a redirect names is checked the same way.
func Fetch(ctx context.Context, domains []string, page string) (*http.Response, error) {
	return fetch(ctx, pageClient, "text/html,text/plain,application/json;q=0.9,*/*;q=0.5", domains, page)
}

// Download gets a big file the way Fetch gets a page, with a ceiling of its own.
func Download(ctx context.Context, domains []string, page string) (*http.Response, error) {
	return fetch(ctx, fileClient, "*/*", domains, page)
}

func fetch(ctx context.Context, client *http.Client, accept string, domains []string, page string) (*http.Response, error) {
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
		req.Header.Set("Accept", accept)
		resp, err := client.Do(req)
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

// Unsent reports whether a failed request never reached the other side: only a failed dial or name lookup proves
// that; a timeout or a cut answer may have been billed.
func Unsent(err error) bool {
	var op *net.OpError
	var dns *net.DNSError
	return errors.As(err, &op) && op.Op == "dial" || errors.As(err, &dns)
}
