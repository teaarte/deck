package process

import (
	"context"
	"strings"
	"time"

	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// cut reports whether a turn was cut off: deck shut down, which leaves the process running to resume, or the
// process is no longer running, as after a stop, so the outcome of the turn has nothing to write.
func (r *Runner) cut(ctx, turnCtx context.Context, processID int64) bool {
	if turnCtx.Err() != nil {
		return true
	}
	p, err := r.st.Q.GetProcess(ctx, processID)
	return err != nil || p.State != StateRunning
}

// stopGrace is how long a rework waits for the turn of a stopped process to exit.
const stopGrace = 5 * time.Second

// Stop ends a process as stopped in the base, then cancels the turn that lives by then, whose outcome finds it no
// longer running; the folder, the hooks and a rework wait until that turn has exited.
func (r *Runner) Stop(ctx context.Context, processID int64, reason string) error {
	return r.end(ctx, processID, StateStopped, reason)
}

// end closes a process that has not finished in state with reason, as Stop does.
func (r *Runner) end(ctx context.Context, processID int64, state, reason string) error {
	var n int64
	var closedCalls []db.Ask
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		if n, err = q.StopParkedProcess(ctx, db.StopParkedProcessParams{State: state, Error: reason, FinishedAt: r.stamp(), ID: processID}); err != nil || n == 0 {
			return err
		}
		closedCalls, err = q.CloseOpenAsksOfProcess(ctx, db.CloseOpenAsksOfProcessParams{ProcessID: processID, CallsToo: 1})
		return err
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return registry.Invalid("deck: process %d has already finished", processID)
	}
	r.mu.Lock()
	lt := r.live[processID]
	delete(r.marked, processID)
	r.mu.Unlock()
	after := func(ctx context.Context) {
		r.dropped(ctx, closedCalls)
		r.closed(ctx, processID)
	}
	if lt == nil {
		r.leave(ctx, processID)
		after(ctx)
		return nil
	}
	lt.cancel(nil)
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		<-lt.done
		after(context.WithoutCancel(r.ctx))
	}()
	return nil
}

// Rework sends a finished process back to its session with the owner's remark and a full ceiling.
func (r *Runner) Rework(ctx context.Context, processID int64, remark string) error {
	r.mu.Lock()
	lt := r.live[processID]
	r.mu.Unlock()
	if lt != nil {
		select {
		case <-lt.done:
		case <-time.After(stopGrace):
			return registry.Conflict("deck: process %d is still stopping; send it back once it has", processID)
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	p, err := r.st.Q.GetProcess(ctx, processID)
	if err != nil {
		return err
	}
	m, err := docs.ManifestAt(ctx, r.st.Q, p.ProjectID, p.ManifestRevision)
	if err != nil {
		return err
	}
	cmd := m.Command(p.Command)
	if cmd == nil {
		return registry.Invalid("deck: command %q no longer exists", p.Command)
	}
	msg := "deck: the owner sent this task back for rework.\n" + untrusted.Value("Remark", strings.TrimSpace(remark), 500) +
		"\nKeep what is right and change what the remark asks for. Save changed documents again under the same" +
		" names, so each change is a new revision, then give the final result again."
	note := strings.TrimSpace(p.StartNote + "\n" + msg)
	var n int64
	err = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if p.RetryKey != "" {
			other, err := q.FindActiveProcess(ctx, db.FindActiveProcessParams{ProjectID: p.ProjectID, Command: p.Command, RetryKey: p.RetryKey, Trial: p.Trial})
			if err == nil && other.ID != p.ID {
				return registry.Invalid("deck: process %d with the same retry key is still going", other.ID)
			}
		}
		n, err = q.ReopenProcess(ctx, db.ReopenProcessParams{CeilingMs: r.ceilingOf(m, cmd).Milliseconds(), StartNote: note, ID: processID})
		return err
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return registry.Invalid("deck: only a finished process with an agent session can go back for rework")
	}
	if err := r.resume(ctx, processID, msg, told{}, false); err != nil {
		r.finish(ctx, processID, StateError, "", false, "rework: "+err.Error())
		return err
	}
	return nil
}
