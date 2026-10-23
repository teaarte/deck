package process

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"deck2/internal/ask"
	"deck2/internal/fault"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/store/db"
)

// handOn closes a process its model refused and goes on with its work, told what it did, on the next model of the
// assignment; false when there is none. A trial and a reply of the chat, which picks its own model, stay.
func (r *Runner) handOn(ctx context.Context, t turn, pv provider.Provider, f *fault.F) bool {
	if t.cmd.Name == t.m.EventCommand(manifest.OnMessage) {
		return false
	}
	p, err := r.st.Q.GetProcess(ctx, t.process)
	if err != nil || p.Trial == 1 {
		return false
	}
	next := r.deps.Next(ctx, t.project, t.cmd, pv.Name())
	if next == "" {
		return false
	}
	input := map[string]any{}
	_ = json.Unmarshal([]byte(p.Input), &input)
	heard, err := r.st.Q.ListRemarks(ctx, p.ID)
	if err != nil {
		return false
	}
	note := r.HandOnNote(ctx, p, pv.Name(), f.Text)
	if p.FinishedAt.Valid {
		return false
	}
	// The continuation starts first with the retry key, so a failed start leaves the process to pause or fail as with
	// no next model, and a call with the key finds the continuation from the start.
	id, err := r.Start(ctx, t.project, t.m, t.cmd.Name, input, Origin{Provider: next, Follows: p.ID, Note: note,
		Parent: p.ParentID.Int64, Key: p.RetryKey})
	if err != nil {
		slog.Error("hand a process on to the next model", "process", p.ID, "provider", next, "err", err)
		return false
	}
	r.finish(ctx, t.process, StateError, "", false, f)
	if p, err = r.st.Q.GetProcess(ctx, t.process); err != nil || p.State != StateError || p.ErrorType != f.Type {
		_ = r.Cancel(ctx, id, "deck: the process it would continue was closed otherwise", false)
		return true
	}
	r.passOn(ctx, p.ID, id, heard)
	r.warnOf(ctx, p.ID, fmt.Sprintf("%s did not finish %s, %s goes on with it: %s", pv.Name(), cmp.Or(t.cmd.Title, t.cmd.Name), next, f.Text),
		health.Provider(pv.Name()))
	return true
}

// HandOnNote tells the process that goes on with the work of p what p did; was is the model p ran on and why
// what stopped it.
func (r *Runner) HandOnNote(ctx context.Context, p db.Process, was, why string) string {
	return fmt.Sprintf("This continues process #%d of the same command with the same input; it ran on %s, which stopped: %s.\n"+
		"What it did stays: files it changed, records it wrote, the processes it started. Check what is already done, "+
		"do not redo it, and go on from where it stopped.\n\n%s", p.ID, was, why, r.told(ctx, p))
}

// passOn gives the process that continues from a closed one the messages the closed one did not take: those its
// retelling carries count as delivered, those that came after it go to the continuation.
func (r *Runner) passOn(ctx context.Context, from, to int64, told []db.ProcessRemark) {
	carried := map[int64]bool{}
	for _, m := range told {
		carried[m.ID] = true
	}
	moved := false
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		waiting, err := q.ListUndeliveredRemarks(ctx, from)
		if err != nil {
			return err
		}
		for _, m := range waiting {
			if !carried[m.ID] {
				if m.Author == RemarkByDeck {
					_, err = q.CreateDeckRemark(ctx, db.CreateDeckRemarkParams{ProcessID: to, Text: m.Text, CreatedAt: m.CreatedAt})
				} else {
					_, err = ask.Remark(ctx, q, to, m.Text, m.CreatedAt)
				}
				if err != nil {
					return err
				}
				moved = true
			}
			if err := q.MarkRemarkDelivered(ctx, m.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		slog.Error("pass the messages of a process on", "process", from, "to", to, "err", err)
	}
	if moved {
		_ = r.Deliver(context.WithoutCancel(ctx), to)
	}
}
