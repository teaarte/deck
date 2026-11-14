package process

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"deck2/internal/fault"
	"deck2/internal/untrusted"

	"deck2/internal/boxes"
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
	u := &run{turn: t}
	fail := func(f *fault.F) { r.settle(bg, u, "", f) }
	proj, err := r.st.Q.GetProject(bg, t.project)
	if err != nil {
		fail(fault.Of(err))
		return
	}
	granted, err := docs.Grants(bg, r.st.Q, proj.ID)
	if err != nil {
		fail(fault.Of(err))
		return
	}
	src, err := r.scriptSource(bg, t.project, t.m, t.cmd)
	if err != nil {
		fail(fault.New(fault.ScriptFailed, err.Error()))
		return
	}
	r.deps.Servers(bg, t.project)
	_ = r.deps.Programs(bg, t.project)
	u, f := r.begin(bg, t, proj, &gateway.Session{ProcessID: id, ProjectID: t.project, Workspace: proj.WorkspaceID, Trial: trial, Run: KindScript})
	if f != nil {
		u = &run{turn: t}
		fail(f)
		return
	}
	defer u.close(r)
	input, inDir, err := r.inputFiles(bg, t.cmd, t.project, id, t.input, false)
	if err != nil {
		fail(fault.New(fault.InputInvalid, err.Error()))
		return
	}
	place, err := r.deps.ScriptsAt(bg, t.project)
	if err != nil {
		fail(fault.OfCall(err))
		return
	}
	job := script.Job{Project: t.project, Script: t.m.Script(t.cmd.Executor.Script), Source: src, Draft: trial, Dir: r.runDir(id),
		Stdin: []byte(input), Box: boxes.For(sandbox.KindScript, t.m, granted, false, place.Folders), Token: u.tok, Read: []string{inDir},
		OnLine:  func(line string) { r.event(bg, id, u.stream, provider.Event{Kind: provider.EventStderr, Text: line}) },
		OnStart: func(pgid int) { r.setGroup(bg, id, pgid) },
	}
	r.know(bg, id, job.Box.Hosts, "", false)
	var out []byte
	if place.On != nil {
		// The token of the script stays on deck: its side puts it in the calls the node brings by the node's key.
		gw := r.gw.Handler(u.tok)
		side := http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			req.Header.Set("Authorization", "Bearer "+u.tok)
			gw.ServeHTTP(w, req)
		})
		out, err = place.On.Run(ctx, job, side, func(e provider.Event) { r.event(bg, id, u.stream, e) })
	} else {
		side, end := r.scripts.Sandbox.Side(r.gw.Handler(u.tok))
		job.Box.Mark, job.Box.Deck = r.scripts.Sandbox.Mark(sandbox.KindScript, fmt.Sprint(id)), side
		out, err = r.scripts.Run(ctx, job)
		end()
	}
	r.setGroup(bg, id, 0)
	if r.cutRun(bg, ctx, u) {
		return
	}
	if err != nil {
		calls, last := r.gw.Refusals(u.tok)
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
	r.settle(bg, u, result, nil)
}

// onNode says t is a script that runs on a node, on a copy of the data of its project: runs of its command there take
// turns, so none writes over what one beside it changed.
func (r *Runner) onNode(ctx context.Context, t turn) bool {
	if t.cmd.Executor.Type != KindScript {
		return false
	}
	place, err := r.deps.ScriptsAt(ctx, t.project)
	return err == nil && place.On != nil && !r.callsItself(ctx, t.process)
}

// callsItself says a run was called, directly or not, by a run of its own command, which waits for it.
func (r *Runner) callsItself(ctx context.Context, id int64) bool {
	p, err := r.st.Q.GetProcess(ctx, id)
	for at := p; err == nil && at.ParentID.Valid; {
		if at, err = r.st.Q.GetProcess(ctx, at.ParentID.Int64); err == nil && at.Command == p.Command && at.ProjectID == p.ProjectID {
			return true
		}
	}
	return false
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
		return store.AddDeckNote(ctx, q, store.DeckNote{CreatedAt: store.Time(r.now()), ProjectID: p.ProjectID, Text: text, Cause: cause})
	})
}
