package process

import (
	"context"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"strings"
)

// stopCause cancels a turn that has to end the process as stopped, not as a deck shutdown.
type stopCause struct{ reason string }

func (s stopCause) Error() string { return s.reason }

// Stop ends a process as stopped: a live turn is cancelled and its provider killed, any other is closed at once.
func (r *Runner) Stop(ctx context.Context, processID int64, reason string) error {
	r.mu.Lock()
	lt := r.live[processID]
	if lt != nil {
		r.stopping[processID] = reason
		lt.cancel(stopCause{reason})
	}
	r.mu.Unlock()
	if lt != nil {
		return nil
	}
	var n int64
	var closedCalls []db.Ask
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		if n, err = q.StopParkedProcess(ctx, db.StopParkedProcessParams{Error: reason, FinishedAt: r.stamp(), ID: processID}); err != nil || n == 0 {
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
	r.dropped(ctx, closedCalls)
	r.closed(processID)
	return nil
}

// Rework sends a finished process back to its session with the owner's remark and a full ceiling.
func (r *Runner) Rework(ctx context.Context, processID int64, remark string) error {
	p, err := r.st.Q.GetProcess(ctx, processID)
	if err != nil {
		return err
	}
	m, err := r.projects.ManifestAt(ctx, p.ProjectID, p.ManifestRevision)
	if err != nil {
		return err
	}
	cmd := m.Command(p.Command)
	if cmd == nil {
		return registry.Invalid("deck: command %q no longer exists", p.Command)
	}
	msg := "deck: the owner sent this task back for rework.\nRemark: " + strings.TrimSpace(remark) +
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
	if err := r.resume(ctx, processID, msg, nil, false); err != nil {
		r.finish(ctx, processID, StateError, "", false, "rework: "+err.Error())
		return err
	}
	return nil
}
