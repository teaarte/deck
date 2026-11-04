package api

import (
	"context"
	"net"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"
)

// proxies are the proxies besides loopback deck takes X-Forwarded-For from, by address, network or name, as Caddy of
// deck's compose; a name is looked up again after a minute, beside the requests, which go by the addresses before.
type proxies struct {
	now     func() time.Time
	names   []string
	fixed   []netip.Prefix
	mu      sync.Mutex
	at      time.Time
	looking bool
	found   []netip.Addr
}

func newProxies(list []string, now func() time.Time) *proxies {
	p := &proxies{now: now}
	for _, x := range list {
		if x = strings.TrimSpace(x); x == "" {
			continue
		}
		if pre, err := netip.ParsePrefix(x); err == nil {
			p.fixed = append(p.fixed, pre.Masked())
		} else if ip, err := netip.ParseAddr(x); err == nil {
			p.fixed = append(p.fixed, netip.PrefixFrom(ip.Unmap(), ip.Unmap().BitLen()))
		} else {
			p.names = append(p.names, x)
		}
	}
	return p
}

// Has says ip is a proxy of deck.
func (p *proxies) Has(ctx context.Context, ip netip.Addr) bool {
	for _, pre := range p.fixed {
		if pre.Contains(ip) {
			return true
		}
	}
	if len(p.names) == 0 {
		return false
	}
	p.mu.Lock()
	stale := !p.looking && p.now().Sub(p.at) > time.Minute
	p.looking = p.looking || stale
	found := p.found
	p.mu.Unlock()
	if stale {
		found = p.look(context.WithoutCancel(ctx))
	}
	return slices.Contains(found, ip)
}

// look finds the addresses of the names; when a name is not found the addresses before stay too.
func (p *proxies) look(ctx context.Context) []netip.Addr {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var found []netip.Addr
	failed := false
	for _, name := range p.names {
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", name)
		failed = failed || err != nil
		for _, x := range ips {
			found = append(found, x.Unmap())
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.looking, p.at = false, p.now()
	if failed {
		found = append(found, p.found...)
	}
	p.found = found
	return found
}
