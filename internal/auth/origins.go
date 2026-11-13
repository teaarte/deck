package auth

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"

	"deck2/internal/home"
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

// Passkeys says a browser of the owner signs in at the origin with a passkey and a cookie: over https by a name, or
// at deck.localhost on this machine. On http://localhost a cookie would reach all servers of the host.
func (o Origin) Passkeys() bool {
	return o.Secure() && net.ParseIP(o.RPID()) == nil || o.Here()
}

// Here says the origin is deck's name on this machine, which browsers take to loopback and hold a secure context.
func (o Origin) Here() bool {
	return strings.HasPrefix(o.Main, "http://") && strings.EqualFold(o.RPID(), HereName)
}

// HereName is the name of deck on the machine it runs on; a cookie of it reaches no other server of localhost.
const HereName = "deck.localhost"

// Here are deck's origins on this machine at a port, its name first, and localhost and 127.0.0.1, which lead to it.
func Here(port int) []Origin {
	var out []Origin
	for _, name := range []string{HereName, "localhost", "127.0.0.1"} {
		out = append(out, Origin{fmt.Sprintf("http://%s:%d", name, port), fmt.Sprintf("http://%s:%d", name, port+1)})
	}
	return out
}

// Origins are the addresses of deck; the tailnet adds its own once deck is on it.
type Origins struct {
	mu   sync.RWMutex
	list []Origin
	// file keeps the list for deck pair, which prints links while deck runs or not.
	file string
}

// Keep writes the origins into file now and after each one added.
func (o *Origins) Keep(file string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.file = file
	return o.write()
}

// write runs under o.mu.
func (o *Origins) write() error {
	if o.file == "" {
		return nil
	}
	b, _ := json.Marshal(o.list)
	return home.WriteFile(o.file, b)
}

// ReadOrigins are the origins a deck of the home kept, none when it has not run.
func ReadOrigins(file string) []Origin {
	var list []Origin
	if b, err := os.ReadFile(file); err == nil {
		_ = json.Unmarshal(b, &list)
	}
	return list
}

// NewOrigins are the origins given, the first the one deck names in its log and links.
func NewOrigins(list ...Origin) *Origins { return &Origins{list: list} }

// Add adds an origin unless it is there.
func (o *Origins) Add(main, side string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !slices.ContainsFunc(o.list, func(x Origin) bool { return x.Main == main }) {
		o.list = append(o.list, Origin{main, side})
		if err := o.write(); err != nil {
			slog.Error("keep the origins", "err", err)
		}
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

// Links are the addresses that pair a device by a code, at the origins where a browser makes a passkey, the https
// ones first, as another device opens those; the code is in the fragment, which no server and no log sees.
func Links(origins []Origin, code string) []string {
	var out []string
	for _, secure := range []bool{true, false} {
		for _, o := range origins {
			if o.Passkeys() && o.Secure() == secure {
				out = append(out, strings.TrimRight(o.Main, "/")+"/#pair="+url.QueryEscape(code))
			}
		}
	}
	return out
}

// Joins are the commands that pair a node by a code and start it as a service of its machine: at the https origins,
// and at 127.0.0.1 for a node on deck's own machine, so a key crosses no network over plain http.
func Joins(origins []Origin, code string) []string {
	var out []string
	for _, secure := range []bool{true, false} {
		for _, o := range origins {
			u, err := url.Parse(o.Main)
			if err != nil || o.Secure() != secure || !secure && u.Hostname() != "127.0.0.1" {
				continue
			}
			out = append(out, "deck -join "+strings.TrimRight(o.Main, "/")+" "+code+" --install")
		}
	}
	return out
}
