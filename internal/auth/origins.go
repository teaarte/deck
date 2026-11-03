package auth

import (
	"net"
	"net/url"
	"slices"
	"strings"
	"sync"
)

// Origin is an address the owner opens deck at, and the second origin of its custom blocks there.
type Origin struct {
	Main string `json:"main"`
	Side string `json:"side"`
}

// Secure says the browser reaches the origin over https, where a cookie may be __Host-.
func (o Origin) Secure() bool { return strings.HasPrefix(o.Main, "https://") }

// RPID is the name passkeys of the origin belong to: its host without the port.
func (o Origin) RPID() string {
	u, err := url.Parse(o.Main)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// Passkeys says a browser makes passkeys at the origin: https, or http on localhost; not by an IP address.
func (o Origin) Passkeys() bool {
	return net.ParseIP(o.RPID()) == nil && (o.Secure() || o.RPID() == "localhost")
}

// Origins are the addresses of deck; the tailnet adds its own once deck is on it.
type Origins struct {
	mu   sync.RWMutex
	list []Origin
}

// NewOrigins are the origins given, the first the one deck names in its log and links.
func NewOrigins(list ...Origin) *Origins { return &Origins{list: list} }

// Add adds an origin unless it is there.
func (o *Origins) Add(main, side string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !slices.ContainsFunc(o.list, func(x Origin) bool { return x.Main == main }) {
		o.list = append(o.list, Origin{main, side})
	}
}

// List are the origins, the first one first.
func (o *Origins) List() []Origin {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return slices.Clone(o.list)
}

// Of is the origin whose main address a request came to by its Host.
func (o *Origins) Of(host string) (Origin, bool) {
	return o.find(host, func(x Origin) string { return x.Main })
}

// OfSide is the origin whose second origin a request came to by its Host.
func (o *Origins) OfSide(host string) (Origin, bool) {
	return o.find(host, func(x Origin) string { return x.Side })
}

// Named is the origin of an Origin header.
func (o *Origins) Named(origin string) (Origin, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, x := range o.list {
		if x.Main == origin {
			return x, true
		}
	}
	return Origin{}, false
}

func (o *Origins) find(host string, of func(Origin) string) (Origin, bool) {
	o.mu.RLock()
	defer o.mu.RUnlock()
	for _, x := range o.list {
		if u, err := url.Parse(of(x)); err == nil && of(x) != "" && sameHost(u, host) {
			return x, true
		}
	}
	return Origin{}, false
}

// sameHost says a Host header names the host of u, its default port written or not.
func sameHost(u *url.URL, host string) bool {
	if strings.EqualFold(u.Host, host) {
		return true
	}
	std := map[string]string{"http": "80", "https": "443"}[u.Scheme]
	return u.Port() == "" && strings.EqualFold(net.JoinHostPort(u.Hostname(), std), host)
}
