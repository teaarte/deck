package process

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"

	"deck2/internal/fault"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/untrusted"
)

// retellCap is how much of what a refused process did the next model reads.
const retellCap = 8000

// handOn closes a process its model refused and goes on with its work, told what it did, on the next model of the
// assignment; false when there is none. A trial and a reply of the chat, which picks its own model, stay.
func (r *Runner) handOn(ctx context.Context, t turn, pv provider.Provider, f *fault.F) bool {
	if r.deps.Next == nil || t.cmd.Name == t.m.EventCommand(manifest.OnMessage) {
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
	note := fmt.Sprintf("This continues process #%d of the same command with the same input; it ran on %s, which stopped: %s.\n"+
		"What it did stays: files it changed, records it wrote, the processes it started. Check what is already done, "+
		"do not redo it, and go on from where it stopped.\n\n%s", p.ID, pv.Name(), f.Text, r.retelling(p.ID))
	r.finish(ctx, t.process, StateError, "", false, f)
	if p, err = r.st.Q.GetProcess(ctx, t.process); err != nil || p.State != StateError || p.ErrorType != f.Type {
		return true
	}
	id, err := r.Start(ctx, t.project, t.m, t.cmd.Name, input, Origin{Provider: next, Follows: p.ID, Note: note,
		Parent: p.ParentID.Int64, Key: p.RetryKey})
	if err != nil {
		slog.Error("hand a process on to the next model", "process", p.ID, "provider", next, "err", err)
		r.warn(ctx, p.ID, fmt.Sprintf("%s stopped process #%d of %s (%s), and its next model %s did not start: %v",
			pv.Name(), p.ID, t.cmd.Name, f.Text, next, err))
		return true
	}
	r.warn(ctx, p.ID, fmt.Sprintf("%s stopped process #%d of %s: %s. Process #%d goes on with it on %s.",
		pv.Name(), p.ID, t.cmd.Name, f.Text, id, next))
	return true
}

// retelling is what a process did by its log: what the agent said and the tools it ran, the latest within retellCap.
func (r *Runner) retelling(processID int64) string {
	lines, err := r.Tail(processID, 200, provider.EventMessage, provider.EventTool, provider.EventToolUpdate)
	if err != nil || len(lines) == 0 {
		return "Its log is empty: start the work over."
	}
	var said []string
	for _, l := range lines {
		var e provider.Event
		if json.Unmarshal(l, &e) != nil {
			continue
		}
		switch e.Kind {
		case provider.EventMessage:
			said = append(said, "said: "+untrusted.Flat(e.Text, 600))
		case provider.EventTool:
			said = append(said, "ran "+e.Title+": "+untrusted.Flat(e.Text, 300))
		case provider.EventToolUpdate:
			if e.Status == "failed" {
				said = append(said, "  it failed: "+untrusted.Flat(e.Text, 200))
			}
		}
	}
	text, n := "", 0
	for i := len(said) - 1; i >= 0 && n+len(said[i]) < retellCap; i-- {
		text = said[i] + "\n" + text
		n += len(said[i]) + 1
	}
	return untrusted.Block("The log of what it did, the latest last", strings.TrimSpace(text))
}
