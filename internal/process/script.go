package process

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"deck2/internal/fault"
	"deck2/internal/untrusted"

	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/provider"
	"deck2/internal/sandbox"
	"deck2/internal/script"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// runScript runs a command script once: input JSON in stdin, result JSON in stdout, files in its results folder.
func (r *Runner) runScript(ctx context.Context, t turn, trial bool) {
	bg := context.WithoutCancel(ctx)
	id := t.process
	dir := r.runDir(id)
	fail := func(err error) {
		r.clearRun(dir)
		r.finish(bg, id, StateError, "", false, fault.Of(err))
		r.failedInARow(bg, t, err.Error())
	}
	proj, err := r.st.Q.GetProject(bg, t.project)
	if err != nil {
		fail(err)
		return
	}
	granted, err := docs.InForce(bg, r.st.Q, proj, t.m)
	if err != nil {
		fail(err)
		return
	}
	src, err := r.scriptSource(bg, t.project, t.m, t.cmd)
	if err != nil {
		fail(fault.New(fault.ScriptFailed, err.Error()))
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fail(err)
		return
	}
	stream, err := os.OpenFile(r.cfg.Home.Stream(id), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fail(err)
		return
	}
	defer stream.Close()
	r.deps.Servers(bg, t.project)
	_ = r.deps.Programs(bg, t.project)
	tok := r.gw.Grant(&gateway.Session{ProcessID: id, ProjectID: t.project, Workspace: proj.WorkspaceID, Trial: trial, Run: KindScript})
	defer r.gw.Unregister(tok)
	door, err := sandbox.Open(r.scripts.Sandbox, gateway.Egress, granted.Domains.Read, r.gw.ScriptHandler(tok))
	if err != nil {
		fail(err)
		return
	}
	defer door.Close()
	_ = r.startCeiling(bg, id)
	input, inDir, err := r.inputFiles(bg, t.cmd, t.project, id, t.input, false)
	if err != nil {
		fail(fault.New(fault.InputInvalid, err.Error()))
		return
	}
	folders, err := r.st.Q.ListProjectFolders(bg, t.project)
	if err != nil {
		fail(err)
		return
	}
	read := []string{inDir}
	for _, f := range folders {
		read = append(read, f.Path)
	}
	job := script.Job{Project: t.project, Script: t.m.Script(t.cmd.Executor.Script), Source: src, Dir: dir,
		Stdin: []byte(input), Door: door, Token: tok, Read: read,
		OnLine:  func(line string) { r.event(bg, id, stream, provider.Event{Kind: provider.EventStderr, Text: line}) },
		OnStart: func(pgid int) { r.setGroup(bg, id, pgid) },
	}
	out, err := r.scripts.Run(ctx, job)
	r.setGroup(bg, id, 0)
	if r.cut(bg, ctx, id) {
		if r.ctx.Err() == nil {
			r.clearRun(dir)
		}
		return
	}
	if err != nil {
		calls, last := r.gw.Refusals(tok)
		fail(refused(calls, last, fault.Of(err)))
		return
	}
	result := string(bytes.TrimSpace(out))
	if result == "" && len(t.cmd.Result) > 0 {
		fail(fault.New(fault.ResultInvalid, fmt.Sprintf("script %s printed no result JSON", job.Script.Name)))
		return
	}
	if result != "" && !json.Valid([]byte(result)) {
		fail(fault.New(fault.ResultInvalid, fmt.Sprintf("script %s printed output that is not JSON: %s", job.Script.Name, untrusted.Cut(result, 300))))
		return
	}
	if verr := validate(t.cmd, json.RawMessage(result)); verr != nil {
		r.clearRun(dir)
		r.finish(bg, id, StateDone, result, true, nil)
		r.warn(bg, id, fmt.Sprintf("The result of %s.%s does not match its schema, so nothing acts on it: %v", proj.Name, t.cmd.Name, verr))
		return
	}
	door.Close()
	kept := filepath.Join(dir, "kept")
	if err := os.Rename(job.Results(), kept); err != nil {
		fail(err)
		return
	}
	if result, err = r.keepFiles(bg, t.project, id, kept, result); err != nil {
		fail(err)
		return
	}
	r.clearRun(dir)
	r.finish(bg, id, StateDone, result, false, nil)
}

// refused adds to the failure of a script deck's error answers to its calls; a failure it did not name takes the
// type of the last one when that was its last call and a script may give that type.
func refused(calls []gateway.Refusal, last bool, f *fault.F) *fault.F {
	if len(calls) == 0 {
		return f
	}
	lines := make([]string, len(calls))
	for i, c := range calls {
		lines[i] = untrusted.Cut(fmt.Sprintf("%s: %d %s", c.Call, c.Status, c.Text), 300)
	}
	out := *f
	c := calls[len(calls)-1]
	t := fault.OfStatus(c.Status, c.Model)
	if last && out.Type == fault.ScriptFailed && fault.FromScript(t) == t {
		out.Type = t
	}
	out.Text += "; deck refused its calls: " + strings.Join(lines, "; ")
	out.Details = "deck refused:\n" + strings.Join(lines, "\n") + "\n\n" + out.Details
	return &out
}

// warn tells the owner in the feed about something of a process deck refused or gave up on.
func (r *Runner) warn(ctx context.Context, processID int64, text string) {
	r.warnOf(ctx, processID, text, "")
}

// warnOf is warn of a cause, the subject of a row of health: the unread notes of one cause of a day are one note.
func (r *Runner) warnOf(ctx context.Context, processID int64, text, cause string) {
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		p, err := q.GetProcess(ctx, processID)
		if err != nil {
			return err
		}
		return q.AddDeckNote(ctx, db.AddDeckNoteParams{CreatedAt: store.Time(r.now()), ProjectID: p.ProjectID, Text: text, Cause: cause})
	})
}

// failedInARow warns once when a command script has just failed for the third time in a row.
func (r *Runner) failedInARow(ctx context.Context, t turn, last string) {
	p, err := r.st.Q.GetProcess(ctx, t.process)
	if err != nil {
		return
	}
	states, err := r.st.Q.RecentOutcomes(ctx, db.RecentOutcomesParams{ProjectID: p.ProjectID, Command: p.Command})
	if err != nil || len(states) < 3 || states[0] != StateError || states[1] != StateError || states[2] != StateError ||
		len(states) > 3 && states[3] == StateError {
		return
	}
	proj, _ := r.st.Q.GetProject(ctx, p.ProjectID)
	r.warn(ctx, p.ID, fmt.Sprintf("Script command %s.%s failed 3 times in a row; fix the script or let an agent do the work. Last error: %s",
		proj.Name, p.Command, untrusted.Cut(strings.TrimSpace(last), 500)))
}
