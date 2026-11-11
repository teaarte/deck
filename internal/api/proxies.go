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

// proxies are the proxies deck takes X-Forwarded-For from, by address, network or name, as Caddy of deck's compose;
// a name is looked up again after a minute, beside the requests, which go by the addresses before.
type proxies struct {
	now     func() time.Time
	names   []string
	fixed   []netip.Prefix
	mu      sync.Mutex
	at      time.Time
	looking bool
	found   []netip.Addr
	last    map[string][]netip.Addr
}

// gateway is the name the compose of before gave the gateway of deck's network, which deck takes as no proxy: any
// container of the host comes from it.
const gateway = "gateway"

// newProxies are the proxies of a list; gateway says the list names the gateway, which is left out.
func newProxies(list []string, now func() time.Time) (p *proxies, gw bool) {
	p = &proxies{now: now, last: map[string][]netip.Addr{}}
	for _, x := range list {
		if x = strings.TrimSpace(x); x == "" || x == gateway {
			gw = gw || x == gateway
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
	return p, gw
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

// look finds the addresses of the names; a name not found keeps the addresses it last had.
func (p *proxies) look(ctx context.Context) []netip.Addr {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	now := map[string][]netip.Addr{}
	for _, name := range p.names {
		ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", name)
		if err != nil {
			continue
		}
		for _, x := range ips {
			now[name] = append(now[name], x.Unmap())
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.looking, p.at = false, p.now()
	var found []netip.Addr
	for _, name := range p.names {
		if ips, ok := now[name]; ok {
			p.last[name] = ips
		}
		found = append(found, p.last[name]...)
	}
	p.found = found
	return found
}
