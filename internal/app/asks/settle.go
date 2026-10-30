package asks

import (
	"context"
	"log/slog"
	"slices"

	"deck2/internal/events"
	"deck2/internal/gateway"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// quiet are the topics no subject of a frozen call follows; the rest may change one.
var quiet = []string{"processes", "usage", "health", "feed", "chat", "collection", "files", "calls"}

// Follow settles the calls waiting for the owner after any change their subjects may follow, in the background.
func (s *Service) Follow(bus *events.Bus) {
	topics := slices.DeleteFunc(store.Topics(), func(t string) bool { return slices.Contains(quiet, t) })
	bus.Follow(s.Go, topics, func(ctx context.Context, _ int64, _ []string) { s.Settle(ctx) })
}

// Settle closes the calls waiting for the owner that deck need not ask about any more: one whose change is in force
// already as done, one whose subject changed since as out of date; each caller hears it.
func (s *Service) Settle(ctx context.Context) {
	open, err := s.St.Q.ListOpenCallAsks(ctx)
	for _, a := range open {
		if _, e := s.settle(ctx, a, false); e != nil {
			err = e
		}
	}
	if err != nil && ctx.Err() == nil {
		slog.Error("settle asks", "err", err)
	}
}

// settle closes one call waiting for the owner when deck need not ask about it any more, and says what it became;
// answering probes what only a call out tells.
func (s *Service) settle(ctx context.Context, a db.Ask, answering bool) (int, error) {
	switch got := s.Gateway.Freshness(ctx, a, answering); got {
	case gateway.InForce:
		closed, err := s.Gateway.Done(ctx, a)
		if closed {
			s.LateAnswer(ctx, a)
			return got, err
		}
		return gateway.AsAsked, err
	case gateway.Changed:
		closed, err := s.Gateway.Stale(ctx, a)
		if closed {
			return got, err
		}
		return gateway.AsAsked, err
	}
	return gateway.AsAsked, nil
}
