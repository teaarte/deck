package process

import (
	"context"
	"sync"
)

func (r *Runner) slotsOf(ctx context.Context, workspace int64) int {
	if r.cfg.AgentSlots == nil {
		return 0
	}
	return r.cfg.AgentSlots(ctx, workspace)
}

// slot is the hold of a turn on one of the agent slots of a workspace; it goes back once, however often it is
// released, and then the process that waited for one longest gets it.
type slot struct {
	r         *Runner
	workspace int64
	once      sync.Once
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
	s.once.Do(func() {
		s.r.mu.Lock()
		s.r.slots[s.workspace]--
		s.r.mu.Unlock()
		s.r.Wake(ctx)
	})
}
