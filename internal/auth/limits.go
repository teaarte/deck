package auth

import (
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

// Allow counts a try of addr and says whether it is within the limit.
func (l *Limiter) Allow(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	t := l.at(addr)
	t.n++
	return t.n <= l.n
}

// Over says addr used up its tries, without counting one.
func (l *Limiter) Over(addr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.at(addr).n >= l.n
}

// at is the count of addr in the window now; it drops the counts of windows past.
func (l *Limiter) at(addr string) *tries {
	now := l.now()
	for k, t := range l.of {
		if now.Sub(t.from) > l.window {
			delete(l.of, k)
		}
	}
	t := l.of[addr]
	if t == nil {
		t = &tries{from: now}
		l.of[addr] = t
	}
	return t
}
