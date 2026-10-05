package process

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"deck2/internal/sandbox"
	"deck2/internal/untrusted"

	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/provider"
	"deck2/internal/script"
	"deck2/internal/store/db"
)

// runScript runs a command script once: input JSON in stdin, result JSON in stdout, files in its results folder.
func (r *Runner) runScript(ctx context.Context, t turn, trial bool) {
	bg := context.WithoutCancel(ctx)
	id := t.process
	dir := r.runDir(id)
	fail := func(err error) {
		r.clearRun(dir)
		r.finish(bg, id, StateError, "", false, err.Error())
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
		fail(err)
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fail(err)
		return
	}
	stream, err := os.OpenFile(filepath.Join(dir, "stream"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		fail(err)
		return
	}
	defer stream.Close()
	r.deps.Servers(bg, t.project)
	tok := r.gw.Grant(&gateway.Session{ProcessID: id, ProjectID: t.project, Workspace: proj.WorkspaceID, Trial: trial})
	defer r.gw.Unregister(tok)
	ln, err := r.scripts.Sandbox.Listen(dir)
	if err != nil {
		fail(err)
		return
	}
	self := r.scripts.Sandbox.Addr(ln)
	runCtx, endRun := context.WithCancel(bg)
	defer endRun()
	srv := &http.Server{Handler: r.gw.ScriptHandler(runCtx, tok, self, granted.Domains.Read)}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()
	url, proxy := "http://"+self, "http://deck:"+tok+"@"+self
	env := []string{"DECK_URL=" + url, "DECK_TOKEN=" + tok, "DECK_MODELS=" + url + "/models", "HTTP_PROXY=" + proxy, "HTTPS_PROXY=" + proxy, "http_proxy=" + proxy, "https_proxy=" + proxy}
	_ = r.st.Tx(bg, func(ctx context.Context, q *db.Queries) error {
		return q.StartCeiling(ctx, db.StartCeilingParams{Now: r.now().UnixMilli(), ID: id})
	})
	input, inDir, err := r.inputFiles(bg, t.cmd, t.project, id, t.input, false)
	if err != nil {
		fail(err)
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
	tag := fmt.Sprintf("r%d", id)
	var closedMu sync.Mutex
	var closed []string
	unwatch, heard := r.scripts.Sandbox.Watch(tag, func(path string) {
		closedMu.Lock()
		seen := slices.Contains(closed, path)
		if !seen {
			closed = append(closed, path)
		}
		closedMu.Unlock()
		if !seen {
			r.event(bg, id, stream, provider.Event{Kind: provider.EventStderr, Text: "deck: the sandbox kept " + path + " closed"})
			r.cfg.Rules.Report(bg, t.project, t.cmd.Name, path)
		}
	})
	defer unwatch()
	select {
	case <-heard:
	case <-time.After(5 * time.Second):
	}
	job := script.Job{Project: t.project, Manifest: t.m, Script: t.m.Script(t.cmd.Executor.Script), Source: src, Dir: dir,
		Stdin: []byte(input), Out: ln, Env: env, Read: read, Tag: tag,
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
		time.Sleep(500 * time.Millisecond)
		closedMu.Lock()
		if len(closed) > 0 {
			err = fmt.Errorf("%w\ndeck: the sandbox kept closed: %s. %s", err, strings.Join(closed, ", "), sandbox.OpenHint)
		}
		closedMu.Unlock()
		fail(err)
		return
	}
	result := string(bytes.TrimSpace(out))
	if result == "" && len(t.cmd.Result) > 0 {
		fail(fmt.Errorf("script %s printed no result JSON", job.Script.Name))
		return
	}
	if result != "" && !json.Valid([]byte(result)) {
		fail(fmt.Errorf("script %s printed output that is not JSON: %s", job.Script.Name, untrusted.Cut(result, 300)))
		return
	}
	if verr := validate(t.cmd, json.RawMessage(result)); verr != nil {
		r.clearRun(dir)
		r.finish(bg, id, StateDone, result, true, "")
		r.warn(bg, id, fmt.Sprintf("The result of %s.%s does not match its schema, so nothing acts on it: %v", proj.Name, t.cmd.Name, verr))
		return
	}
	endRun()
	kept := filepath.Join(dir, "kept")
	if err := os.Rename(job.Results(), kept); err != nil {
		fail(err)
		return
	}
	if err := r.keepFiles(bg, t.project, id, kept, result); err != nil {
		fail(err)
		return
	}
	r.clearRun(dir)
	r.finish(bg, id, StateDone, result, false, "")
}

// warn tells the owner in the feed about something of a process deck refused or gave up on.
func (r *Runner) warn(ctx context.Context, processID int64, text string) {
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		p, err := q.GetProcess(ctx, processID)
		if err != nil {
			return err
		}
		return q.AddDeckNote(ctx, db.AddDeckNoteParams{ProjectID: p.ProjectID, Text: text})
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
