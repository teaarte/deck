package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"deck2/internal/ask"
	"deck2/internal/fault"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// waiting is the turn a held process runs once what it waits for is free.
type waiting struct {
	Message string `json:"message,omitempty"`
	told
}

// wait is why a process waits, on what, until when (zero: no term of its own) and what its silence comes to.
type wait struct {
	reason, on, silence string
	until               time.Time
}

// leaveRunning moves a running process to waiting, with what its next turn carries; its ceiling stops in the same
// write, so no way out of running can forget it.
func (r *Runner) leaveRunning(ctx context.Context, q *db.Queries, processID int64, w wait, next waiting, fresh bool) (int64, error) {
	raw := ""
	if next.Message != "" || len(next.Asks) > 0 || len(next.Remarks) > 0 {
		b, _ := json.Marshal(next)
		raw = string(b)
	}
	var until sql.NullInt64
	if !w.until.IsZero() {
		until = sql.NullInt64{Int64: w.until.UnixMilli(), Valid: true}
	}
	return q.LeaveRunning(ctx, db.LeaveRunningParams{Message: raw, Fresh: store.Bool(fresh), Reason: w.reason, OnWhat: w.on,
		Silence: w.silence, Until: until, Now: r.now().UnixMilli(), ID: processID})
}

// holdIn keeps a turn that cannot start yet waiting, with what its resume carries. It reports false when the process
// is no longer running.
func (r *Runner) holdIn(ctx context.Context, q *db.Queries, t turn, w wait) (bool, error) {
	n, err := r.leaveRunning(ctx, q, t.process, w, waiting{Message: t.message, told: t.asks}, !t.resume)
	return n > 0, err
}

// hold keeps a turn waiting for its provider or a slot and wakes the waiters, since what it waits for may be
// free already.
func (r *Runner) hold(ctx context.Context, t turn, w wait) {
	var held bool
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		held, err = r.holdIn(ctx, q, t, w)
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

// waker wakes a process that waits for a reason of its own: the key of what it waits for, whether that is free (it may
// take a slot), whether one waiter of a key goes a pass, and what a waiter of a busy key does meanwhile.
type waker struct {
	key  func(h db.ListHeldRow) string
	free func(r *Runner, ctx context.Context, h db.ListHeldRow) (bool, *slot)
	one  bool
	busy func(r *Runner, ctx context.Context, h db.ListHeldRow)
}

// wakers are the reasons deck wakes a process of itself; the owner's answer or login wakes the others.
func wakers() map[string]waker {
	return map[string]waker{
		WaitLimit: {
			key: func(h db.ListHeldRow) string { return fmt.Sprint("limit:", h.Process.ID) },
			free: func(r *Runner, ctx context.Context, h db.ListHeldRow) (bool, *slot) {
				return !h.Process.WaitUntilMs.Valid || h.Process.WaitUntilMs.Int64 <= r.now().UnixMilli(), nil
			}},
		WaitProvider: {
			key: func(h db.ListHeldRow) string { return "provider:" + h.Process.Provider },
			free: func(r *Runner, ctx context.Context, h db.ListHeldRow) (bool, *slot) {
				return r.deps.Goes(ctx, h.WorkspaceID, h.Process.Provider), nil
			},
			busy: func(r *Runner, ctx context.Context, h db.ListHeldRow) { r.reassigned(ctx, h.WorkspaceID, h.Process) }},
		WaitRight: {
			key: func(h db.ListHeldRow) string { return "right:" + h.Process.WaitOn },
			free: func(r *Runner, ctx context.Context, h db.ListHeldRow) (bool, *slot) {
				id, _ := strconv.ParseInt(h.Process.WaitOn, 10, 64)
				a, err := r.st.Q.GetAsk(ctx, id)
				return err != nil || a.State != ask.StateOpen, nil
			}},
		WaitSlot: {
			key: func(h db.ListHeldRow) string { return fmt.Sprint("slot:", h.WorkspaceID) },
			free: func(r *Runner, ctx context.Context, h db.ListHeldRow) (bool, *slot) {
				s := r.takeSlot(ctx, h.WorkspaceID)
				return s != nil, s
			}},
		WaitRun: {
			key: func(h db.ListHeldRow) string {
				return fmt.Sprint("run:", h.Process.ProjectID, ".", h.Process.Command, ":", h.Process.Trial)
			},
			free: func(r *Runner, ctx context.Context, h db.ListHeldRow) (bool, *slot) {
				return r.runFree(ctx, h.Process), nil
			},
			one: true},
		WaitFolder: {
			key: func(h db.ListHeldRow) string { return "folder:" + lockOf(h.Process.Folder, h.Process.Provider) },
			free: func(r *Runner, ctx context.Context, h db.ListHeldRow) (bool, *slot) {
				return r.folderFree(ctx, lockOf(h.Process.Folder, h.Process.Provider)), nil
			},
			one: true},
	}
}

func (r *Runner) wake(ctx context.Context) {
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
	busy, ws := map[string]bool{}, wakers()
	for _, h := range held {
		p := h.Process
		w, ok := ws[p.WaitReason]
		if !ok {
			continue
		}
		key := w.key(h)
		free, s := false, (*slot)(nil)
		if !busy[key] {
			free, s = w.free(r, ctx, h)
		}
		if !free || w.one {
			busy[key] = true
		}
		if !free {
			if w.busy != nil {
				w.busy(r, ctx, h)
			}
			continue
		}
		if ok, err := r.claim(ctx, p.ID, p.WaitReason); err != nil || !ok {
			s.release(ctx)
			continue
		}
		if p.WaitReason == WaitLimit {
			_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return unpause(ctx, q, r.now(), p.Provider) })
		}
		t, err := r.heldTurn(ctx, p)
		if err != nil {
			s.release(ctx)
			r.finish(ctx, p.ID, StateError, "", false, fault.Of(err))
			continue
		}
		t.slot = s
		if !r.spawn(t) {
			r.unclaim(ctx, p.ID)
		}
	}
}

// reached ends the wait a turn came out of once it calls deck's tools: a wait after it gets a term of its own.
func (r *Runner) reached(ctx context.Context, processID int64) {
	_ = r.st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error { return q.EndWait(ctx, processID) })
}

// unclaim puts a process claimed back to running that could not start back to the wait it had.
func (r *Runner) unclaim(ctx context.Context, processID int64) {
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.UnclaimProcess(ctx, processID)
		return err
	})
}

// silence ends the waits whose term passed and whose silence fails them; a wait on the owner ends with the term of
// its ask, a pause tries again as Wake finds its term passed.
func (r *Runner) silence(ctx context.Context, now time.Time) {
	past, err := r.st.Q.ListWaitsPast(ctx, now.UnixMilli())
	if err != nil {
		slog.Error("waits past their term", "err", err)
		return
	}
	for _, p := range past {
		t, until := strings.TrimPrefix(p.WaitSilence, SilenceFail), store.Time(time.UnixMilli(p.WaitUntilMs.Int64))
		text := fmt.Sprintf("deck: the process waited for %s %s until %s, its term", p.WaitReason, p.WaitOn, until)
		switch t {
		case fault.DeckTools:
			text = fmt.Sprintf("deck: the agents of %s kept starting without deck's tools until %s, the term of the wait", p.WaitOn, until)
		case fault.ModelUnavailable:
			text = fmt.Sprintf("deck: %s did not work again by %s, the term of the wait", p.WaitOn, until)
		}
		// Claimed first, so a process woken meanwhile is not ended.
		if ok, err := r.claim(ctx, p.ID, p.WaitReason); err != nil || !ok {
			continue
		}
		r.finish(ctx, p.ID, StateError, "", false, fault.New(t, text))
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
		if more := pending.except(w.told, nil); !more.empty() {
			t.message = strings.TrimSpace(t.message + "\n" + more.message())
			t.asks = t.asks.add(more.ids())
		}
	}
	return t, nil
}
