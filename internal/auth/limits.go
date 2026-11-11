package auth

import (
	"net/netip"
	"sync"
	"time"
)

// Limiter counts the tries of an address in a window; past its number the address waits for the window to end.
type Limiter struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	now    func() time.Time
	of     map[string]*tries
}

type tries struct {
	from time.Time
	n    int
}

func NewLimiter(n int, window time.Duration, now func() time.Time) *Limiter {
	return &Limiter{n: n, window: window, now: now, of: map[string]*tries{}}
}

// most is how many addresses a limiter counts; past it a new address takes the place of the one counted longest.
const most = 10000

// Allow counts a try of addr and says whether it is within the limit.
func (l *Limiter) Allow(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.at(addr)
	t.n++
	return t.n <= l.n
}

// at is the count of addr in its window now.
func (l *Limiter) at(addr string) *tries {
	now := l.now()
	k := keyOf(addr)
	t := l.of[k]
	if t != nil && now.Sub(t.from) > l.window {
		t.from, t.n = now, 0
	}
	if t == nil {
		if len(l.of) >= most {
			l.drop(now)
		}
		t = &tries{from: now}
		l.of[k] = t
	}
	return t
}

// drop forgets the counts of windows past, and the one counted longest when none has passed.
func (l *Limiter) drop(now time.Time) {
	var oldest string
	for k, t := range l.of {
		if now.Sub(t.from) > l.window {
			delete(l.of, k)
		} else if oldest == "" || t.from.Before(l.of[oldest].from) {
			oldest = k
		}
	}
	if len(l.of) >= most {
		delete(l.of, oldest)
	}
}

// keyOf is what an address counts as: an IPv6 address by its /64, which one holder has whole.
func keyOf(addr string) string {
	ip, err := netip.ParseAddr(addr)
	if err != nil {
		return addr
	}
	ip = ip.WithZone("").Unmap()
	if ip.Is4() {
		return ip.String()
	}
	p, _ := ip.Prefix(64)
	return p.String()
}
