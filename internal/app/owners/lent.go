package owners

import (
	"strings"
	"sync"
	"time"

	"deck2/internal/nonce"
	"deck2/internal/registry"
)

// lentPrefix starts a token deck lends its own browser for a while, and the cookie its page trades the token for:
// both act as the caller of the loan and only read.
const lentPrefix = "deck_lent_"

type lent struct {
	act   registry.Actor
	until time.Time
	// of is the token a cookie was traded for; empty for a token.
	of string
}

// loans are the tokens and cookies lent now; they live in memory only.
type loans struct {
	mu sync.Mutex
	of map[string]lent
}

// Lend is a token that acts as act for ttl and only reads, for deck's own browser to open a page with.
func (s *Service) Lend(act registry.Actor, ttl time.Duration) string {
	act.Looks, act.ReadOnly = true, true
	return s.lend(lent{act: act, until: s.Now().Add(ttl)})
}

// Trade is a cookie for a lent token: the same caller until the same time; the token's return ends it too.
func (s *Service) Trade(raw string) (string, bool) {
	s.loans.mu.Lock()
	l, ok := s.loans.of[raw]
	s.loans.mu.Unlock()
	if !ok || l.of != "" || s.Now().After(l.until) {
		return "", false
	}
	l.of = raw
	return s.lend(l), true
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

// Return ends a lent token and the cookies traded for it before their time.
func (s *Service) Return(raw string) {
	s.loans.mu.Lock()
	defer s.loans.mu.Unlock()
	for k, l := range s.loans.of {
		if k == raw || l.of == raw {
			delete(s.loans.of, k)
		}
	}
}

// Lent is the actor a lent token or cookie acts as while it lasts.
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
