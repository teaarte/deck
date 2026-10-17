package process

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strconv"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// joinText is what a running agent process reads of another call of its command for its record.
func joinText(cmd *manifest.Command, in []byte, o Origin) string {
	by := ""
	if o.Parent != 0 {
		by = fmt.Sprintf(" by process #%d", o.Parent)
	}
	return fmt.Sprintf("deck: %s was called again for record %d while you work%s. Take this call into this run: do what "+
		"its input adds to your task, then give one result for both.\n%s", cmd.Name, RecordOf(string(in)), by, inputData(string(in)))
}

// join gives a running agent process the message of another call for its record, kept as remark: in its turn when
// the provider takes it, else right after the turn; the files of the call come with it.
func (r *Runner) join(ctx context.Context, processID, remark int64, text string, cmd *manifest.Command, input map[string]any) {
	var files []int64
	for _, name := range fileFields(cmd) {
		for _, v := range values(input[name]) {
			if id, err := strconv.ParseInt(fmt.Sprint(v), 10, 64); err == nil {
				files = append(files, id)
			}
		}
	}
	bg := context.WithoutCancel(ctx)
	if len(files) > 0 {
		if line, err := r.Give(ctx, processID, files); err == nil {
			text += "\n" + line
			_ = r.st.Tx(bg, func(ctx context.Context, q *db.Queries) error {
				return q.SetRemarkText(ctx, db.SetRemarkTextParams{Text: text, ID: remark})
			})
		}
	}
	r.mu.Lock()
	stopped := r.stopped
	if !stopped {
		r.wg.Add(1)
	}
	r.mu.Unlock()
	if stopped {
		return
	}
	go func() {
		defer r.wg.Done()
		if r.Say(bg, processID, text) == Refused {
			_ = r.Deliver(bg, processID)
			return
		}
		err := r.st.Tx(bg, func(ctx context.Context, q *db.Queries) error { return q.MarkRemarkDelivered(ctx, remark) })
		if err != nil {
			slog.Error("join a call to its running process", "process", processID, "err", err)
		}
	}()
}

// again runs once more a script, tool or model command whose process took calls for its record while it ran, with
// the input of the last of them; a process stopped runs nothing more.
func (r *Runner) again(ctx context.Context, processID int64) {
	p, err := r.st.Q.GetProcess(ctx, processID)
	if err != nil || p.Again == "" || p.State == StateStopped {
		return
	}
	if fs, err := r.st.Q.ListFollowers(ctx, store.ID(p.ID)); err != nil || len(fs) > 0 {
		return
	}
	proj, err := r.st.Q.GetProject(ctx, p.ProjectID)
	if err != nil {
		return
	}
	m, err := docs.Manifest(ctx, r.st.Q, proj)
	if err != nil {
		slog.Error("run a command again for its record", "process", p.ID, "err", err)
		return
	}
	var input map[string]any
	_ = json.Unmarshal([]byte(p.Again), &input)
	if _, err := r.Start(ctx, p.ProjectID, m, p.Command, input, Origin{Follows: p.ID, Parent: p.ParentID.Int64, Trial: p.Trial == 1, again: true}); err != nil {
		r.warn(ctx, p.ID, fmt.Sprintf("deck did not run %s again for the calls that came while process #%d ran: %v", p.Command, p.ID, err))
	}
}
