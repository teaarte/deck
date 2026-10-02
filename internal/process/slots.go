package process

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"deck2/internal/store/db"
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
		s.r.FreeSlots(ctx, s.workspace)
	})
}

// waitSlot holds a turn that found every slot taken; its ceiling starts with the turn.
func (r *Runner) waitSlot(ctx context.Context, t turn, workspace int64) {
	w, _ := json.Marshal(waiting{Message: t.message, told: t.asks})
	var n int64
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		n, err = q.WaitForSlot(ctx, db.WaitForSlotParams{Message: string(w), Fresh: b2i(!t.resume), ID: t.process})
		return err
	})
	if n > 0 {
		r.bus.Publish("processes")
		r.FreeSlots(ctx, workspace)
	}
}

// FreeSlots runs the processes that wait for a slot, oldest first, while the workspace has slots; each turn
// starts holding the slot it was handed.
func (r *Runner) FreeSlots(ctx context.Context, workspace int64) {
	r.mu.Lock()
	stopped := r.stopped
	r.mu.Unlock()
	if stopped {
		return
	}
	ps, err := r.st.Q.ListWaitingForSlot(ctx, workspace)
	if err != nil {
		slog.Error("processes waiting for a slot", "workspace", workspace, "err", err)
		return
	}
	for _, p := range ps {
		s := r.takeSlot(ctx, workspace)
		if s == nil {
			return
		}
		ok, err := r.claim(ctx, p.ID, StateWaitingSlot)
		if err != nil || !ok {
			s.release(ctx)
			continue
		}
		t, err := r.heldTurn(ctx, p)
		if err != nil {
			s.release(ctx)
			r.finish(ctx, p.ID, StateError, "", false, err.Error())
			continue
		}
		t.slot = s
		r.bus.Publish("processes")
		if !r.spawn(t) {
			_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
				_, err := q.ClaimProcess(ctx, db.ClaimProcessParams{ToState: StateWaitingSlot, ID: p.ID, FromState: StateRunning})
				return err
			})
		}
	}
}
