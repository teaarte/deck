package process

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"deck2/internal/fault"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// run is one run of a command by its executor, from begin to settle.
type run struct {
	turn
	proj db.Project
	// agent marks a turn of an agent: its run folder holds its work, so it stays.
	agent  bool
	stream *os.File
	// tok is the gateway session of the run, empty for a call.
	tok string
}

// begin opens a run: its results folder, its log, the gateway session s when given, and its ceiling.
func (r *Runner) begin(ctx context.Context, t turn, proj db.Project, s *gateway.Session) (*run, *fault.F) {
	u := &run{turn: t, proj: proj, agent: t.cmd.Executor.Type != KindScript && t.cmd.Executor.Type != KindTool && t.cmd.Executor.Type != KindModel}
	if err := os.MkdirAll(r.cfg.Home.Results(t.process), 0o700); err != nil {
		return nil, fault.Of(err)
	}
	stream, err := os.OpenFile(r.cfg.Home.Stream(t.process), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fault.Of(err)
	}
	u.stream = stream
	if s != nil {
		if u.tok, err = r.gw.Open(ctx, s); err != nil {
			stream.Close()
			return nil, fault.Of(err)
		}
	}
	if err := r.startCeiling(ctx, t.process); err != nil {
		u.close(r)
		return nil, fault.Wrap("start the ceiling", err)
	}
	return u, nil
}

// close closes the log and the gateway session of a run.
func (u *run) close(r *Runner) {
	u.stream.Close()
	if u.tok != "" {
		r.gw.Close(u.tok)
	}
}

// cut reports whether a stop or deck ending cut the run; a script or call that a stop cut leaves no run folder.
func (r *Runner) cutRun(ctx, runCtx context.Context, u *run) bool {
	if !r.cut(ctx, runCtx, u.process) {
		return false
	}
	if !u.agent && r.ctx.Err() == nil {
		r.clearRun(r.runDir(u.process))
	}
	return true
}

// settle closes a run with what it gave: failure f, or result, done by the schema of its command with the files it
// names kept, or done off its schema, which nothing acts on.
func (r *Runner) settle(ctx context.Context, u *run, result string, f *fault.F) {
	// The run folder of a script or call goes before the process ends, so none sees it ended with its folder there.
	clear := func() {
		if !u.agent {
			r.clearRun(r.runDir(u.process))
		}
	}
	if f != nil {
		clear()
		r.finish(ctx, u.process, StateError, "", false, f)
		if u.cmd.Executor.Type == KindScript {
			r.failedInARow(ctx, u.turn, f.Error())
		}
		return
	}
	if left := r.leftWork(ctx, u, result); left != nil {
		clear()
		r.finish(ctx, u.process, StateError, result, false, left)
		return
	}
	if verr := validate(u.cmd, []byte(result)); verr != nil {
		clear()
		r.finish(ctx, u.process, StateDone, result, true, nil)
		name := u.cmd.Name
		if p, err := r.st.Q.GetProject(ctx, u.project); err == nil {
			name = p.Name + "." + name
		}
		r.warn(ctx, u.process, fmt.Sprintf("The result of %s does not match its schema, so nothing acts on it: %v", name, verr))
		return
	}
	kept, err := r.kept(ctx, u, result)
	clear()
	if err != nil {
		if !u.agent {
			result = ""
		}
		r.finish(ctx, u.process, StateError, result, false, fault.Of(err))
		if u.cmd.Executor.Type == KindScript {
			r.failedInARow(ctx, u.turn, err.Error())
		}
		return
	}
	r.finish(ctx, u.process, StateDone, kept, false, nil)
}

// kept keeps the files of a result by its schema in the results folder and gives the result with their ids; a turn
// of an agent without a schema keeps what it left there, telling the owner what it could not.
func (r *Runner) kept(ctx context.Context, u *run, result string) (string, error) {
	results := r.cfg.Home.Results(u.process)
	if u.agent && len(u.cmd.Result) == 0 {
		if err := r.keepLeft(ctx, u.project, u.process, results); err != nil {
			r.warn(ctx, u.process, "deck kept none of the files the turn left: "+err.Error())
		}
		return result, nil
	}
	if u.cmd.Executor.Type == KindScript {
		// A script's results are moved aside first, so nothing it left running changes them while deck keeps them.
		frozen := filepath.Join(r.runDir(u.process), "kept")
		if err := os.Rename(results, frozen); err != nil {
			return "", err
		}
		results = frozen
	}
	return r.keepFiles(ctx, u.project, u.process, results, result)
}

// leftWork is the failure of an agent turn that ended without a result while processes it started still work, as
// one that says it waits for them; a turn with a result handed in or by its schema ends done.
func (r *Runner) leftWork(ctx context.Context, u *run, result string) *fault.F {
	if !u.agent {
		return nil
	}
	// The master's reply and its answers to failures and incidents start work and end by design.
	for _, on := range []string{manifest.OnMessage, manifest.OnFailure, manifest.OnIncident} {
		if u.cmd.Name == u.m.EventCommand(on) {
			return nil
		}
	}
	if p, err := r.st.Q.GetProcess(ctx, u.process); err != nil || p.SubmittedAt.Valid {
		return nil
	}
	if len(u.cmd.Result) > 0 && validate(u.cmd, []byte(result)) == nil {
		return nil
	}
	kids, err := r.st.Q.ListRunningChildren(ctx, store.ID(u.process))
	if err != nil {
		return nil
	}
	var names []string
	for _, k := range kids {
		if k.State == StateRunning || k.State == StateWaiting && (k.WaitReason == WaitSlot || k.WaitReason == WaitRun) {
			names = append(names, fmt.Sprintf("#%d %s", k.ID, k.Command))
		}
	}
	if len(names) == 0 {
		return nil
	}
	const left = "the turn ended without a result while the processes it started still run: %s; to wait for a process, call process_get with wait"
	return fault.New(fault.AgentLeft, fmt.Sprintf(left, strings.Join(names, ", ")))
}
