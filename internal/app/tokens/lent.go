package tokens

import (
	"slices"
	"strings"
	"sync"
	"time"

	"deck2/internal/nonce"
	"deck2/internal/registry"
)

// lentPrefix starts a token deck lends its own browser for a while: it acts as its caller and only reads.
const lentPrefix = "deck_lent_"

// pageReads are the commands a page of the client reads that are not declared reads of deck.
var pageReads = []string{registry.Deck("screen_get"), registry.Deck("menu_get"), registry.Deck("themes_list")}

type lent struct {
	act   registry.Actor
	until time.Time
}

// loans are the tokens lent now, by token; they live in memory only.
type loans struct {
	mu sync.Mutex
	of map[string]lent
}

// Lend is a token that acts as act for ttl and only reads, for deck's own browser to open a page with.
func (s *Service) Lend(act registry.Actor, ttl time.Duration) string {
	s.loans.mu.Lock()
	defer s.loans.mu.Unlock()
	if s.loans.of == nil {
		s.loans.of = map[string]lent{}
	}
	now := time.Now()
	for k, l := range s.loans.of {
		if now.After(l.until) {
			delete(s.loans.of, k)
		}
	}
	raw := lentPrefix + nonce.Hex(24)
	s.loans.of[raw] = lent{act: act, until: now.Add(ttl)}
	return raw
}

// Return ends a lent token before its time.
func (s *Service) Return(raw string) {
	s.loans.mu.Lock()
	defer s.loans.mu.Unlock()
	delete(s.loans.of, raw)
}

// Lent is the actor a lent token acts as while it lasts.
func (s *Service) Lent(raw string) (registry.Actor, bool) {
	if !strings.HasPrefix(raw, lentPrefix) {
		return registry.Actor{}, false
	}
	s.loans.mu.Lock()
	defer s.loans.mu.Unlock()
	l, ok := s.loans.of[raw]
	if !ok || time.Now().After(l.until) {
		return registry.Actor{}, false
	}
	l.act.Looks = true
	return l.act, true
}

// Reads says whether a lent token may call a command: a read of deck or what a page reads.
func Reads(name string) bool {
	return registry.Reads(name) != nil || slices.Contains(pageReads, name)
}
