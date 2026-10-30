package events

import (
	"context"
	"slices"
	"sync"
	"sync/atomic"
)

// Follow calls f beside the commits through run for the changes of topics, merged by workspace: what changes while f
// runs comes in one more call. projects are the projects it changed in, nil for all; workspace zero is every one.
func (b *Bus) Follow(run func(func(context.Context)) bool, topics []string, f func(ctx context.Context, workspace int64, projects []string)) {
	fl := &follower{run: run, f: f, busy: &b.busy, pending: map[int64][]string{}}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.watchers = append(b.watchers, func(e Event) {
		if slices.Contains(topics, e.Topic) {
			fl.add(e)
		}
	})
}

// Settled reports whether every follower has caught up with the events so far.
func (b *Bus) Settled() bool { return b.busy.Load() == 0 }

type follower struct {
	run  func(func(context.Context)) bool
	f    func(context.Context, int64, []string)
	busy *atomic.Int64

	mu      sync.Mutex
	running bool
	// pending are the projects changed in each workspace since the last call; nil is all of them.
	pending map[int64][]string
}

func (fl *follower) add(e Event) {
	fl.mu.Lock()
	ps, ok := fl.pending[e.Workspace]
	switch {
	case e.Project == "":
		fl.pending[e.Workspace] = nil
	case !ok:
		fl.pending[e.Workspace] = []string{e.Project}
	case ps != nil && !slices.Contains(ps, e.Project):
		fl.pending[e.Workspace] = append(ps, e.Project)
	}
	if fl.running {
		fl.mu.Unlock()
		return
	}
	fl.running = true
	fl.busy.Add(1)
	fl.mu.Unlock()
	if !fl.run(fl.loop) {
		fl.mu.Lock()
		fl.running = false
		clear(fl.pending)
		fl.busy.Add(-1)
		fl.mu.Unlock()
	}
}

func (fl *follower) loop(ctx context.Context) {
	for {
		fl.mu.Lock()
		if len(fl.pending) == 0 || ctx.Err() != nil {
			fl.running = false
			clear(fl.pending)
			fl.busy.Add(-1)
			fl.mu.Unlock()
			return
		}
		todo := fl.pending
		fl.pending = map[int64][]string{}
		fl.mu.Unlock()
		for ws, ps := range todo {
			fl.f(ctx, ws, ps)
		}
	}
}
