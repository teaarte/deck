package process

import (
	"context"
	"sync"
)

func (r *Runner) slotsOf(ctx context.Context, workspace int64) int {
	return r.cfg.AgentSlots(ctx, workspace)
}

// slot is the hold of a turn on one of the agent slots of a workspace; it goes back once, however often it is
// released, and then the process that waited for one longest gets it.
type slot struct {
	r         *Runner
	workspace int64
	once      sync.Once
	// lent counts the waits of the turn that lend the slot, gone that it went back; both under r.mu.
	lent int
	gone bool
}

// takeSlot holds one of the agent slots of the workspace, nil when they are all taken; zero slots is no limit.
func (r *Runner) takeSlot(ctx context.Context, workspace int64) *slot {
	limit := r.slotsOf(ctx, workspace)
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit > 0 && r.slots[workspace] >= limit {
		return nil
	}
	r.slots[workspace]++
	return &slot{r: r, workspace: workspace}
}

func (s *slot) release(ctx context.Context) {
	if s == nil {
		return
	}
	s.r.mu.Lock()
	freed := s.free()
	s.r.mu.Unlock()
	if freed {
		s.r.Wake(ctx)
	}
}

// free gives the slot back under r.mu without waking the waiting, and reports whether it was held till now.
func (s *slot) free() bool {
	if s == nil {
		return false
	}
	freed := false
	s.once.Do(func() {
		s.gone = true
		if s.lent == 0 {
			s.r.slots[s.workspace]--
		}
		freed = true
	})
	return freed
}

// Lend frees the slot of a running agent turn while it waits for another process, so the processes it waits for
// may take it; the func it returns takes the slot back, past the limit if need be, since the turn goes on at once.
func (r *Runner) Lend(ctx context.Context, processID int64) func() {
	r.mu.Lock()
	lt := r.live[processID]
	if lt == nil || lt.slot == nil || lt.slot.gone {
		r.mu.Unlock()
		return func() {}
	}
	s := lt.slot
	s.lent++
	if s.lent == 1 {
		r.slots[s.workspace]--
	}
	r.mu.Unlock()
	r.Wake(context.WithoutCancel(ctx))
	var once sync.Once
	return func() {
		once.Do(func() {
			r.mu.Lock()
			defer r.mu.Unlock()
			if s.lent--; s.lent == 0 && !s.gone {
				r.slots[s.workspace]++
			}
		})
	}
}
