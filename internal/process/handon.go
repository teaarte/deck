package process

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"deck2/internal/ask"
	"deck2/internal/fault"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// handOn closes a process its model refused and goes on with its work, told what it did, on the next model of the
// assignment; false when there is none. A trial and a reply of the chat, which picks its own model, stay.
func (r *Runner) handOn(ctx context.Context, t turn, pv provider.Provider, f *fault.F) bool {
	if !r.handsOn(ctx, t) {
		return false
	}
	next := r.deps.Next(ctx, t.project, t.cmd, pv.Name())
	if next == "" {
		return false
	}
	return r.handOnTo(ctx, t, pv.Name(), next, f)
}

// handsOn says the work of a process may go on in another: not a trial, nor a reply of the chat.
func (r *Runner) handsOn(ctx context.Context, t turn) bool {
	if t.cmd.Name == t.m.EventCommand(manifest.OnMessage) {
		return false
	}
	p, err := r.st.Q.GetProcess(ctx, t.process)
	return err == nil && p.Trial == 0
}

// handOnTo closes a process that ran on was and goes on with its work on next: the continuation starts, the process
// closes and its messages pass on in one transaction, so no fall leaves both running; false when it does not start.
func (r *Runner) handOnTo(ctx context.Context, t turn, was, next string, f *fault.F) bool {
	p, err := r.st.Q.GetProcess(ctx, t.process)
	if err != nil {
		return false
	}
	input := map[string]any{}
	_ = json.Unmarshal([]byte(p.Input), &input)
	heard, err := r.st.Q.ListRemarks(ctx, p.ID)
	if err != nil {
		return false
	}
	note := r.HandOnNote(ctx, p, was, f.Text)
	if p.FinishedAt.Valid {
		return false
	}
	// The continuation takes the retry key, so a call with the key finds it from the start.
	c := &closing{process: p.ID, f: f, heard: heard}
	id, err := r.Start(ctx, t.project, t.m, t.cmd.Name, input, Origin{Provider: next, Follows: p.ID, Note: note,
		Parent: p.ParentID.Int64, Key: p.RetryKey, closes: c})
	if err != nil && c.owned {
		r.disown(p.ID)
	}
	if errors.Is(err, errNotRunning) {
		// The process was closed or is closing otherwise, so nothing started.
		return true
	}
	if err != nil {
		slog.Error("hand a process on to the next model", "process", p.ID, "provider", next, "err", err)
		return false
	}
	r.finished(ctx, p.ID, 1, false, nil)
	if c.moved {
		_ = r.Deliver(context.WithoutCancel(ctx), id)
	}
	r.warnOf(ctx, p.ID, fmt.Sprintf("%s did not finish %s, %s goes on with it: %s", was, cmp.Or(t.cmd.Title, t.cmd.Name), next, f.Text),
		health.Provider(was))
	return true
}

// closing is a process a start closes in its own transaction as it hands its work on, with the messages it heard;
// moved says some of the rest went on to the continuation.
type closing struct {
	process int64
	f       *fault.F
	heard   []db.ProcessRemark
	moved   bool
	// owned says the start took the closing of the process to itself, so no stop closes it meanwhile.
	owned bool
}

// closeIn closes the process c hands on from and passes its messages on to the continuation to; errNotRunning rolls
// the start back when the process left running meanwhile.
func (r *Runner) closeIn(ctx context.Context, q *db.Queries, c *closing, to int64) error {
	n, _, err := r.finishIn(ctx, q, c.process, Fail{F: c.f})
	if err != nil {
		return err
	}
	if n == 0 {
		return errNotRunning
	}
	c.moved, err = r.passOnIn(ctx, q, c.process, to, c.heard)
	return err
}

// reassigned hands a process that waits for its provider on to a model its assignment puts before it now, one that
// takes turns: the owner assigned another first; behind its own it waits in its session. False when it stays.
func (r *Runner) reassigned(ctx context.Context, workspace int64, p db.Process) bool {
	t, err := r.turnOf(ctx, p)
	if err != nil || !r.handsOn(ctx, t) {
		return false
	}
	next := r.deps.Ahead(ctx, p.ProjectID, t.cmd, p.Provider)
	if next == "" || !r.deps.Goes(ctx, workspace, next) {
		return false
	}
	if ok, err := r.claim(ctx, p.ID, p.WaitReason); err != nil || !ok {
		return false
	}
	f := fault.New(fault.ModelUnavailable, fmt.Sprintf("%s takes no turns, and the assignment gives the work to %s now", p.Provider, next))
	if r.handOnTo(ctx, t, p.Provider, next, f) {
		return true
	}
	r.unclaim(ctx, p.ID)
	return false
}

// handOnOpening opens the note of a process that goes on with the work of another, by its id.
const handOnOpening = "This continues process #%d of the same command"

// HandOnNote tells the process that goes on with the work of p what p did; was is the model p ran on and why
// what stopped it.
func (r *Runner) HandOnNote(ctx context.Context, p db.Process, was, why string) string {
	return fmt.Sprintf(handOnOpening+" with the same input; it ran on %s, which stopped: %s.\n"+
		"What it did stays: files it changed, records it wrote, the processes it started. Check what is already done, "+
		"do not redo it, and go on from where it stopped.\n\n%s", p.ID, was, why, r.told(ctx, p))
}

// passOnIn gives the process that continues from a closed one the messages the closed one did not take: those its
// retelling carries count as delivered, those that came after it go to the continuation; true when some went.
func (r *Runner) passOnIn(ctx context.Context, q *db.Queries, from, to int64, told []db.ProcessRemark) (bool, error) {
	carried := map[int64]bool{}
	for _, m := range told {
		carried[m.ID] = true
	}
	moved := false
	waiting, err := q.ListUndeliveredRemarks(ctx, from)
	if err != nil {
		return false, err
	}
	for _, m := range waiting {
		if !carried[m.ID] {
			if m.Author == RemarkByDeck {
				_, err = q.CreateDeckRemark(ctx, db.CreateDeckRemarkParams{ProcessID: to, Text: m.Text, CreatedAt: m.CreatedAt})
			} else {
				_, err = ask.Remark(ctx, q, to, m.Text, m.CreatedAt)
			}
			if err != nil {
				return false, err
			}
			moved = true
		}
		if err := q.MarkRemarkDelivered(ctx, db.MarkRemarkDeliveredParams{ID: m.ID, Now: store.Time(r.now())}); err != nil {
			return false, err
		}
	}
	return moved, nil
}

// passOn is passOnIn in a transaction of its own, then the continuation hears what went on to it.
func (r *Runner) passOn(ctx context.Context, from, to int64, told []db.ProcessRemark) {
	var moved bool
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		moved, err = r.passOnIn(ctx, q, from, to, told)
		return err
	})
	if err != nil {
		slog.Error("pass the messages of a process on", "process", from, "to", to, "err", err)
	}
	if moved {
		_ = r.Deliver(context.WithoutCancel(ctx), to)
	}
}

// handedOn is the process that went on with the work of p after p last started, 0 for none: a running p beside it is
// what a fall in the middle of a hand-on left before the hand-on was one transaction.
func (r *Runner) handedOn(ctx context.Context, p db.Process) (int64, error) {
	fs, err := r.st.Q.ListFollowers(ctx, store.ID(p.ID))
	if err != nil {
		return 0, err
	}
	for _, f := range fs {
		next, err := r.st.Q.GetProcess(ctx, f.ID)
		if err != nil {
			return 0, err
		}
		after := !p.ReopenedAt.Valid || timeOf(next.StartedAt).After(timeOf(p.ReopenedAt.String))
		if after && strings.HasPrefix(next.StartNote, fmt.Sprintf(handOnOpening, p.ID)) {
			return next.ID, nil
		}
	}
	return 0, nil
}

// timeOf is a stored time, zero when unreadable.
func timeOf(s string) time.Time {
	t, _ := store.ParseTime(s)
	return t
}
