package process

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"deck2/internal/store/db"
)

// waiting is the turn a held process runs once what it waits for is free.
type waiting struct {
	Message string `json:"message,omitempty"`
	told
}

// holdIn keeps a turn that cannot start yet in a waiting state, with what its resume carries; its ceiling starts
// with the turn. It reports false when the process is no longer running.
func holdIn(ctx context.Context, q *db.Queries, t turn, state string) (bool, error) {
	w, _ := json.Marshal(waiting{Message: t.message, told: t.asks})
	n, err := q.HoldProcess(ctx, db.HoldProcessParams{State: state, Message: string(w), Fresh: b2i(!t.resume), ID: t.process})
	return n > 0, err
}

// hold keeps a turn waiting for its provider or a slot and wakes the waiters, since what it waits for may be
// free already.
func (r *Runner) hold(ctx context.Context, t turn, state string) {
	var held bool
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		held, err = holdIn(ctx, q, t, state)
		return err
	})
	if held {
		r.bus.Publish("processes")
		r.Wake(ctx)
	}
}

// Wake starts the held turns whose provider is ready, whose workspace has a slot or whose folder is free, oldest
// first; each is checked before it is claimed, so a turn that cannot start stays as it is.
func (r *Runner) Wake(ctx context.Context) {
	r.mu.Lock()
	stopped := r.stopped
	r.mu.Unlock()
	if stopped {
		return
	}
	held, err := r.st.Q.ListHeld(ctx)
	if err != nil {
		slog.Error("held processes", "err", err)
		return
	}
	folders := map[string]bool{}
	for _, h := range held {
		p := h.Process
		var s *slot
		switch p.State {
		case StateWaitingProvider:
			if !r.providers.Ready(ctx, p.Provider) {
				continue
			}
		case StateWaitingSlot:
			if s = r.takeSlot(ctx, h.WorkspaceID); s == nil {
				continue
			}
		case StateWaitingFolder:
			if folders[p.Folder] || !r.folderFree(ctx, p.Folder) {
				continue
			}
			folders[p.Folder] = true
		}
		if ok, err := r.claim(ctx, p.ID, p.State); err != nil || !ok {
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
				_, err := q.ClaimProcess(ctx, db.ClaimProcessParams{ToState: p.State, ID: p.ID, FromState: StateRunning})
				return err
			})
		}
	}
}

func (r *Runner) folderFree(ctx context.Context, folder string) bool {
	n, err := r.st.Q.FolderBusy(ctx, folder)
	return err == nil && n == 0
}

// heldTurn is the turn a held process runs now, with the answers that reached it meanwhile; the caller has
// claimed it back to running.
func (r *Runner) heldTurn(ctx context.Context, p db.Process) (turn, error) {
	var w waiting
	_ = json.Unmarshal([]byte(p.WaitMessage), &w)
	t, err := r.turnOf(ctx, p)
	if err != nil {
		return turn{}, err
	}
	t.asks = w.told
	if p.WaitFresh == 0 {
		t.resume, t.message = true, w.Message
		pending, _ := r.untold(ctx, p.ID)
		if more := pending.except(w.told, ""); !more.empty() {
			t.message = strings.TrimSpace(t.message + "\n" + more.message())
			t.asks = t.asks.add(more.ids())
		}
	}
	return t, nil
}
