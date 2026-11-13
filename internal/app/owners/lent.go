package owners

import (
	"strings"
	"sync"
	"time"

	"deck2/internal/nonce"
	"deck2/internal/registry"
)

// lentPrefix starts a token deck lends its own browser for a while: it acts as the caller of the loan and only reads.
const lentPrefix = "deck_lent_"

type lent struct {
	act   registry.Actor
	until time.Time
}

// loans are the tokens lent now; they live in memory only.
type loans struct {
	mu sync.Mutex
	of map[string]lent
}

// Lend is a token that acts as act for ttl and only reads, for deck's own browser to open a page with.
func (s *Service) Lend(act registry.Actor, ttl time.Duration) string {
	act.Looks, act.ReadOnly, act.Passkey = true, true, time.Time{}
	return s.lend(lent{act: act, until: s.Now().Add(ttl)})
}

func (s *Service) lend(l lent) string {
	s.loans.mu.Lock()
	defer s.loans.mu.Unlock()
	if s.loans.of == nil {
		s.loans.of = map[string]lent{}
	}
	now := s.Now()
	for k, x := range s.loans.of {
		if now.After(x.until) {
			delete(s.loans.of, k)
		}
	}
	raw := lentPrefix + nonce.Hex(24)
	s.loans.of[raw] = l
	return raw
}

// Return ends a lent token before its time.
func (s *Service) Return(raw string) {
	s.loans.mu.Lock()
	defer s.loans.mu.Unlock()
	delete(s.loans.of, raw)
}

// IsLent says raw has the form of a lent token, live or not.
func (s *Service) IsLent(raw string) bool { return strings.HasPrefix(raw, lentPrefix) }

// Lent is the actor a lent token acts as while it lasts.
func (s *Service) Lent(raw string) (registry.Actor, bool) {
	if !strings.HasPrefix(raw, lentPrefix) {
		return registry.Actor{}, false
	}
	s.loans.mu.Lock()
	defer s.loans.mu.Unlock()
	l, ok := s.loans.of[raw]
	if !ok || s.Now().After(l.until) {
		return registry.Actor{}, false
	}
	return l.act, true
}
