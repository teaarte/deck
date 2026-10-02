package process

import (
	"context"
	"encoding/json"
	"log/slog"

	"deck2/internal/store/db"
)

func (r *Runner) slotsOf(ctx context.Context, workspace int64) int {
	if r.cfg.AgentSlots == nil {
		return 0
	}
	return r.cfg.AgentSlots(ctx, workspace)
}

// takeSlot holds one of the agent slots of the workspace; zero slots is no limit.
func (r *Runner) takeSlot(ctx context.Context, workspace int64) bool {
	limit := r.slotsOf(ctx, workspace)
	r.mu.Lock()
	defer r.mu.Unlock()
	if limit > 0 && r.slots[workspace] >= limit {
		return false
	}
	r.slots[workspace]++
	return true
}

// releaseSlot gives the slot of a finished turn to the process that waited for one longest.
func (r *Runner) releaseSlot(ctx context.Context, workspace int64) {
	r.mu.Lock()
	r.slots[workspace]--
	r.mu.Unlock()
	r.FreeSlots(ctx, workspace)
}

// waitSlot holds a turn that found every slot taken; its ceiling starts with the turn.
func (r *Runner) waitSlot(ctx context.Context, t turn, workspace int64) {
	if r.stopFirst(ctx, t.process) {
		return
	}
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
	ps, err := r.st.Q.ListWaitingForSlot(ctx, workspace)
	if err != nil {
		slog.Error("processes waiting for a slot", "workspace", workspace, "err", err)
		return
	}
	for _, p := range ps {
		if !r.takeSlot(ctx, workspace) {
			return
		}
		ok, err := r.claim(ctx, p.ID, StateWaitingSlot)
		if err != nil || !ok {
			r.mu.Lock()
			r.slots[workspace]--
			r.mu.Unlock()
			continue
		}
		t, err := r.heldTurn(ctx, p)
		if err != nil {
			r.mu.Lock()
			r.slots[workspace]--
			r.mu.Unlock()
			r.finish(ctx, p.ID, StateError, "", false, err.Error())
			continue
		}
		t.slot, t.workspace = true, workspace
		r.bus.Publish("processes")
		if !r.spawn(t) {
			r.mu.Lock()
			r.slots[workspace]--
			r.mu.Unlock()
			_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
				_, err := q.ClaimProcess(ctx, db.ClaimProcessParams{ToState: StateWaitingSlot, ID: p.ID, FromState: StateRunning})
				return err
			})
		}
	}
}
