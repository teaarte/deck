package api

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"net"
	"net/netip"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

// Gateway names among the proxies the default gateway of deck's network: in a container, the address the host's own
// connections to a published port come from, as those of tailscale serve.
const Gateway = "gateway"

// proxies are the proxies besides loopback deck takes X-Forwarded-For from, by address, network or name, as Caddy of
// deck's compose; a name is looked up again after a minute, beside the requests, which go by the addresses before.
type proxies struct {
	now     func() time.Time
	routes  string
	names   []string
	fixed   []netip.Prefix
	mu      sync.Mutex
	at      time.Time
	looking bool
	found   []netip.Addr
	last    map[string][]netip.Addr
}

func newProxies(list []string, now func() time.Time, routes string) *proxies {
	p := &proxies{now: now, routes: routes, last: map[string][]netip.Addr{}}
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

// look finds the addresses of the names; a name not found keeps the addresses it last had.
func (p *proxies) look(ctx context.Context) []netip.Addr {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	now := map[string][]netip.Addr{}
	for _, name := range p.names {
		if name == Gateway {
			if ip, err := GatewayOf(p.routes); err == nil {
				now[name] = []netip.Addr{ip}
			}
			continue
		}
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

// GatewayOf is the IPv4 gateway of the default route in a routing table of Linux, as /proc/net/route.
func GatewayOf(routes string) (netip.Addr, error) {
	b, err := os.ReadFile(routes)
	if err != nil {
		return netip.Addr{}, err
	}
	for _, line := range strings.Split(string(b), "\n")[1:] {
		f := strings.Fields(line)
		if len(f) < 8 || f[1] != "00000000" || f[7] != "00000000" {
			continue
		}
		raw, err := hex.DecodeString(f[2])
		if err != nil || len(raw) != 4 {
			continue
		}
		var ip [4]byte
		binary.BigEndian.PutUint32(ip[:], binary.LittleEndian.Uint32(raw))
		if a := netip.AddrFrom4(ip); !a.IsUnspecified() {
			return a, nil
		}
	}
	return netip.Addr{}, errors.New("no default gateway in " + routes)
}
