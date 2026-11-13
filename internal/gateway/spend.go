package gateway

import (
	"context"
	"sync"

	"deck2/internal/registry"
)

// spend lets one call of a run held to its run_usd go at a time, so each is counted with what the ones before it
// cost: the cost of a call is in before the next is decided.
type spend struct {
	mu   sync.Mutex
	runs map[int64]*run
}

type run struct {
	slot  chan struct{}
	users int
}

// take waits until no other call of the run of process is out and holds the run until give.
func (s *spend) take(ctx context.Context, process int64) (give func(), err error) {
	s.mu.Lock()
	if s.runs == nil {
		s.runs = map[int64]*run{}
	}
	r := s.runs[process]
	if r == nil {
		r = &run{slot: make(chan struct{}, 1)}
		s.runs[process] = r
	}
	r.users++
	s.mu.Unlock()
	leave := func() {
		s.mu.Lock()
		if r.users--; r.users == 0 {
			delete(s.runs, process)
		}
		s.mu.Unlock()
	}
	select {
	case r.slot <- struct{}{}:
		return sync.OnceFunc(func() {
			<-r.slot
			leave()
		}), nil
	case <-ctx.Done():
		leave()
		return nil, ctx.Err()
	}
}

// held decides a call again once it holds its run when the owner's always holds it to run_usd; give lets the next
// call of the run go.
func (g *Gateway) held(ctx context.Context, act registry.Actor, v Verdict, again func() (Verdict, error)) (Verdict, func(), error) {
	if !v.capped {
		return v, func() {}, nil
	}
	give, err := g.spend.take(ctx, act.ProcessID)
	if err != nil {
		return v, func() {}, err
	}
	v, err = again()
	return v, give, err
}
