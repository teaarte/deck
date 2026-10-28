package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"deck2/internal/fault"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// waiting is the turn a held process runs once what it waits for is free.
type waiting struct {
	Message string `json:"message,omitempty"`
	told
}

// leaveRunning moves a running process to a state where it waits, with what its next turn carries and, on a pause,
// when it tries again; its ceiling stops in the same write, so no way out of running can forget it.
func (r *Runner) leaveRunning(ctx context.Context, q *db.Queries, processID int64, state string, w waiting, fresh bool, until sql.NullInt64) (int64, error) {
	raw := ""
	if w.Message != "" || len(w.Asks) > 0 || len(w.Remarks) > 0 {
		b, _ := json.Marshal(w)
		raw = string(b)
	}
	return q.LeaveRunning(ctx, db.LeaveRunningParams{State: state, Message: raw, Fresh: store.Bool(fresh), Until: until, Now: r.now().UnixMilli(), ID: processID})
}

// holdIn keeps a turn that cannot start yet in a waiting state, with what its resume carries. It reports false
// when the process is no longer running.
func (r *Runner) holdIn(ctx context.Context, q *db.Queries, t turn, state string) (bool, error) {
	n, err := r.leaveRunning(ctx, q, t.process, state, waiting{Message: t.message, told: t.asks}, !t.resume, sql.NullInt64{})
	return n > 0, err
}

// hold keeps a turn waiting for its provider or a slot and wakes the waiters, since what it waits for may be
// free already.
func (r *Runner) hold(ctx context.Context, t turn, state string) {
	var held bool
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		held, err = r.holdIn(ctx, q, t, state)
		return err
	})
	if held {
		r.Wake(ctx)
	}
}

// Wake starts held turns whose provider, slot or folder is free or whose pause is over, oldest first; a Wake during
// another makes that one look again, and a pass does not end with its caller's context.
func (r *Runner) Wake(ctx context.Context) {
	r.mu.Lock()
	if r.waking {
		r.wakeAgain = true
		r.mu.Unlock()
		return
	}
	r.waking = true
	r.mu.Unlock()
	ctx = context.WithoutCancel(ctx)
	for {
		r.wake(ctx)
		r.mu.Lock()
		if !r.wakeAgain {
			r.waking = false
			r.mu.Unlock()
			return
		}
		r.wakeAgain = false
		r.mu.Unlock()
	}
}

func (r *Runner) wake(ctx context.Context) {
	r.mu.Lock()
	stopped := r.stopped
	r.mu.Unlock()
	if stopped {
		return
	}
	held, err := r.st.Q.ListHeld(ctx, r.now().UnixMilli())
	if err != nil {
		slog.Error("held processes", "err", err)
		return
	}
	busy := map[string]bool{}
	for _, h := range held {
		p := h.Process
		var s *slot
		switch p.State {
		case StateWaitingProvider:
			if busy["provider:"+p.Provider] || !r.deps.Goes(ctx, h.WorkspaceID, p.Provider) {
				busy["provider:"+p.Provider] = true
				continue
			}
		case StateWaitingSlot:
			key := fmt.Sprint("slot:", h.WorkspaceID)
			if busy[key] {
				continue
			}
			if s = r.takeSlot(ctx, h.WorkspaceID); s == nil {
				busy[key] = true
				continue
			}
		case StateWaitingRun:
			key := fmt.Sprint("run:", p.ProjectID, ".", p.Command)
			if busy[key] || !r.runFree(ctx, p) {
				busy[key] = true
				continue
			}
			busy[key] = true
		case StateWaitingFolder:
			if busy["folder:"+p.Folder] || !r.folderFree(ctx, p.Folder) {
				busy["folder:"+p.Folder] = true
				continue
			}
			busy["folder:"+p.Folder] = true
		}
		if ok, err := r.claim(ctx, p.ID, p.State); err != nil || !ok {
			s.release(ctx)
			continue
		}
		if p.State == StateRateLimited {
			_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return unpause(ctx, q, p.Provider) })
		}
		t, err := r.heldTurn(ctx, p)
		if err != nil {
			s.release(ctx)
			r.finish(ctx, p.ID, StateError, "", false, fault.Of(err))
			continue
		}
		t.slot = s
		if !r.spawn(t) {
			_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
				_, err := q.ClaimProcess(ctx, db.ClaimProcessParams{ToState: p.State, ID: p.ID, FromState: StateRunning})
				return err
			})
		}
	}
}

func (r *Runner) folderFree(ctx context.Context, folder string) bool {
	_, err := r.st.Q.FolderLockHolder(ctx, folder)
	return errors.Is(err, sql.ErrNoRows)
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
