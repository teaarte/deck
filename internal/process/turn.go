package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"deck2/internal/docs"
	"deck2/internal/fault"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// turn is one provider session run of a process: a fresh start or a resume of its session.
type turn struct {
	process, project int64
	m                *manifest.Manifest
	cmd              *manifest.Command
	input            string
	session          string
	resume           bool
	// message is the resume message; a fresh start builds its prompt from the command and the start note.
	message string
	asks    told
	pv      provider.Provider
	// folder is the project folder the turn works in, or empty for a work folder of its own.
	folder string
	// live is the entry of the turn among the live ones, set when it is spawned.
	live *liveTurn
	// slot is the agent slot the turn holds, handed to it or taken when it starts.
	slot *slot
}

func (r *Runner) run(ctx context.Context, t turn) {
	bg := context.WithoutCancel(ctx)
	defer func() { t.slot.release(bg) }()
	p, err := r.st.Q.GetProcess(bg, t.process)
	if err != nil || p.State != StateRunning {
		return
	}
	trial := p.Trial == 1
	switch t.cmd.Executor.Type {
	case KindScript:
		r.runScript(ctx, t, trial)
	case KindTool:
		r.runTool(ctx, t, trial)
	case KindModel:
		r.runModel(ctx, t, trial)
	default:
		r.runAgent(ctx, &t, p, trial)
	}
}

// agentTurn is what an agent turn runs with once it holds its provider, folder and slot.
type agentTurn struct {
	req     provider.Request
	sub     *gateway.Submit
	proj    db.Project
	tools   []string
	dir     string
	data    string
	listen  func(on bool)
	cleanup func()
}

func (r *Runner) runAgent(ctx context.Context, t *turn, p db.Process, trial bool) {
	bg := context.WithoutCancel(ctx)
	fail := func(f *fault.F) { r.finish(bg, t.process, StateError, "", false, f) }
	a, ok := r.prepare(bg, t, p, trial, fail)
	if !ok {
		return
	}
	defer a.cleanup()
	r.converse(ctx, t, a, fail)
}

// prepare gets a turn its provider, folder and slot, or leaves it waiting for them, then its folders, gateway
// session, log and request; false when the turn does not go on.
func (r *Runner) prepare(ctx context.Context, t *turn, p db.Process, trial bool, fail func(*fault.F)) (*agentTurn, bool) {
	pv, err := r.deps.Named(ctx, t.project, p.Provider)
	if err != nil {
		fail(fault.Of(err))
		return nil, false
	}
	if missing := pv.Caps().Missing(t.cmd.Requires); len(missing) > 0 {
		fail(fault.New(fault.DeckInternal, fmt.Sprintf("deck: provider %s no longer fits %s: it lacks %s", pv.Name(), t.cmd.Name, strings.Join(missing, ", "))))
		return nil, false
	}
	t.pv = pv
	proj, err := r.st.Q.GetProject(ctx, t.project)
	if err != nil {
		fail(fault.Of(err))
		return nil, false
	}
	if !r.deps.Goes(ctx, proj.WorkspaceID, pv.Name()) {
		r.hold(ctx, *t, StateWaitingProvider)
		return nil, false
	}
	t.folder = p.Folder
	if t.folder != "" {
		if real, err := filepath.EvalSymlinks(t.folder); err != nil || real != t.folder {
			fail(fault.New(fault.ProgramMissing, "deck: the project folder "+t.folder+" is gone or moved"))
			return nil, false
		}
		if !r.holdFolder(ctx, *t, t.folder) {
			return nil, false
		}
	}
	if t.slot == nil {
		if t.slot = r.takeSlot(ctx, proj.WorkspaceID); t.slot == nil {
			r.hold(ctx, *t, StateWaitingSlot)
			return nil, false
		}
		r.mu.Lock()
		t.live.slot = t.slot
		r.mu.Unlock()
	}
	a := &agentTurn{proj: proj, dir: r.runDir(t.process), data: r.scripts.DataDir(t.project)}
	work, repo := r.cfg.Home.RunWork(t.process), false
	if t.folder != "" {
		work = t.folder
		_, err := os.Stat(filepath.Join(work, ".git"))
		repo = err == nil
	} else if err := os.MkdirAll(work, 0o700); err != nil {
		fail(fault.Of(err))
		return nil, false
	}
	if err := errors.Join(os.MkdirAll(r.cfg.Home.Results(t.process), 0o700), os.MkdirAll(a.data, 0o700)); err != nil {
		fail(fault.Of(err))
		return nil, false
	}
	r.deps.Servers(ctx, t.project)
	if len(t.cmd.Result) > 0 && !slices.Contains(pv.Caps(), provider.StructuredOutput) {
		a.sub = &gateway.Submit{Schema: t.cmd.Result, Validate: func(raw json.RawMessage) error { return validate(t.cmd, raw) }}
	}
	tok, err := r.gw.Register(ctx, &gateway.Session{ProcessID: t.process, ProjectID: t.project, Workspace: proj.WorkspaceID, Trial: trial, Submit: a.sub,
		Question: pv.QuestionTool(), Wait: callLimit(pv), Master: proj.Builtin == 1, Commands: t.cmd.Executor.Calls,
		Heard: func() { r.answered(t.process) }})
	if err != nil {
		fail(fault.Of(err))
		return nil, false
	}
	a.tools = r.gw.Tools(tok)
	stream, err := os.OpenFile(r.cfg.Home.Stream(t.process), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		r.gw.Unregister(tok)
		fail(fault.Of(err))
		return nil, false
	}
	a.cleanup = func() { stream.Close(); r.gw.Unregister(tok) }
	if err := r.startCeiling(ctx, t.process); err != nil {
		a.cleanup()
		fail(fault.Wrap("start the ceiling", err))
		return nil, false
	}
	noNetwork := t.m.NoNetwork
	var hosts []string
	if now, err := docs.For(ctx, r.st.Q, registry.Actor{}, t.project); err == nil {
		noNetwork = noNetwork || now.NoNetwork
		hosts = manifest.ShellHosts(now)
	}
	if noNetwork {
		hosts = nil
	}
	processID := t.process
	var read []string
	if folders, err := r.st.Q.ListProjectFolders(ctx, t.project); err == nil {
		for _, f := range folders {
			read = append(read, f.Path)
		}
	}
	a.req = provider.Request{
		Cwd: work, Repo: repo, Prompt: t.message, ResultSchema: t.cmd.Result,
		MCPURL: r.cfg.MCPBase + "/mcp/" + tok, AllowedTools: r.gw.AllowedTools(tok), PermissionMCP: gateway.PermissionTool(),
		SessionID: t.session, Resume: t.resume, Hidden: r.cfg.Hidden(), Dirs: []string{a.data, r.cfg.Home.Results(processID)}, Data: a.data, Read: read, NoNetwork: noNetwork, Hosts: hosts,
		OnStart: func(pid int) { r.setGroup(ctx, processID, pid) },
		OnEvent: func(e provider.Event) { r.event(ctx, processID, stream, e) },
		OnUsage: func(u provider.Usage) { r.usage(ctx, proj.WorkspaceID, processID, pv, u) },
	}
	var in chan provider.Message
	if slices.Contains(pv.Caps(), provider.LiveInput) && t.live != nil {
		in = make(chan provider.Message)
		a.req.Live = in
	}
	live := t.live
	a.listen = func(on bool) {
		if in == nil {
			return
		}
		r.mu.Lock()
		if live.input = nil; on {
			live.input = in
		}
		r.mu.Unlock()
	}
	if a.sub != nil {
		a.req.ResultSchema = nil
		if t.resume {
			a.req.Prompt += "\nWhen you are done, hand in the whole final result with the result_submit tool of the deck MCP server, then end your turn.\n"
		}
	}
	if a.req.Skills, err = r.deps.Skills(ctx, t.project, t.cmd.Executor.Skills); err == nil {
		t.input, _, err = r.inputFiles(ctx, t.cmd, t.project, processID, t.input, true)
	}
	if err == nil && !t.resume {
		a.req.Prompt, err = r.firstPrompt(ctx, proj, *t, a.tools, a.data)
		// Messages that came before the first turn, such as another call for its record, go with it.
		if u, uerr := r.untold(ctx, processID); err == nil && uerr == nil {
			if ids := u.except(t.asks, "").ids().Remarks; len(ids) > 0 {
				t.asks.Remarks = append(t.asks.Remarks, ids...)
				a.req.Prompt += r.remarksText(ctx, processID, ids)
			}
		}
	}
	if err != nil {
		a.cleanup()
		fail(fault.Of(err))
		return nil, false
	}
	return a, true
}

// converse runs the provider until the turn has an outcome: done by the schema, parked for the owner, paused on
// a limit, a lost login or an error; a result off the schema is asked for once more.
func (r *Runner) converse(ctx context.Context, t *turn, a *agentTurn, fail func(*fault.F)) {
	bg := context.WithoutCancel(ctx)
	processID, pv, req := t.process, t.pv, a.req
	asks := t.asks
	for retried := false; ; {
		a.listen(true)
		stop := r.watchCall(processID, a.proj.WorkspaceID, pv)
		res, err := pv.Run(ctx, req)
		stop()
		a.listen(false)
		r.setGroup(bg, processID, 0)
		if l := leftModel(ctx); l != nil {
			t.asks = asks
			r.leaveModel(bg, t, l)
			return
		}
		if r.cut(bg, ctx, processID) {
			return
		}
		if err == nil && res.Outcome == provider.SessionMissing {
			// The owner's messages go with the fresh session; the asks stay undelivered, so it parks for them.
			asks = told{Remarks: asks.Remarks}
			req.Resume = false
			if req.Prompt, err = r.firstPrompt(bg, a.proj, *t, a.tools, a.data); err != nil {
				fail(fault.Of(err))
				return
			}
			req.Prompt += r.remarksText(bg, processID, asks.Remarks)
			continue
		}
		if err != nil && res.Outcome == provider.NoTools {
			t.asks, t.resume = asks, req.Resume
			if req.Resume {
				t.message = req.Prompt
			}
			r.lostTools(bg, *t, pv, err)
			return
		}
		r.toolsFound(processID)
		if a.sub != nil && err == nil && res.Outcome != provider.Failed && submitted(a.sub) {
			// A turn that ended on its own with its result in is done, whatever a later call of the model got.
			res.Outcome = provider.Done
		}
		_ = r.st.Tx(bg, func(ctx context.Context, q *db.Queries) error { return asks.mark(ctx, q) })
		asks = told{}
		r.providerHealth(bg, pv, res, err)
		settled := err == nil && res.Outcome == provider.Done
		if a.sub != nil && settled {
			var spent bool
			res.Structured, spent = a.sub.Result()
			retried = retried || spent
		}
		verr := error(nil)
		blocked := settled && r.blocked(bg, processID)
		if settled && !blocked {
			verr = validate(t.cmd, res.Structured)
		}
		switch {
		case (err != nil || res.Outcome != provider.Done) && r.questioned(bg, processID):
			r.park(bg, processID)
		case err != nil:
			fail(fault.Of(err))
			r.deps.Broke(bg, pv.Name())
		case res.Past != "":
			// Deck held the call back, so the model is not at a limit of its own and does not pause.
			f := fault.With(fault.ModelLimit, untrusted.Flat(res.Past, 300), res.Past)
			if !r.handOn(bg, *t, pv, f) {
				r.finish(bg, processID, StateError, "", false, f)
			}
		case res.Outcome == provider.Unavailable:
			f := fault.With(fault.ModelUnavailable, fmt.Sprintf("%s did not answer: %s", pv.Name(), untrusted.Flat(res.Text, 200)), res.Text)
			if !r.handOn(bg, *t, pv, f) {
				r.finish(bg, processID, StateError, res.Text, false, f)
			}
			r.deps.Broke(bg, pv.Name())
		case res.Outcome == provider.Limited:
			if r.handOn(bg, *t, pv, fault.With(fault.ModelLimit, fmt.Sprintf("%s reached its usage limit", pv.Name()), res.Text)) {
				r.limited(bg, pv, res.ResetsAt, res.Text)
			} else {
				r.pause(bg, pv, processID, res.ResetsAt, res.Text)
			}
		case res.Outcome == provider.Login:
			r.needLogin(bg, pv, processID, res.Text)
		case res.Outcome == provider.Failed:
			r.finish(bg, processID, StateError, res.Text, false, fault.With(fault.TaskFailed, fmt.Sprintf("the agent reported an error: %s", untrusted.Flat(res.Text, 200)), res.Text))
			r.deps.Broke(bg, pv.Name())
		case !blocked && verr != nil && res.Refusal != "":
			f := fault.With(fault.ModelUnavailable, fmt.Sprintf("%s refused a call of the turn: %s", pv.Name(), untrusted.Flat(res.Refusal, 200)), res.Refusal)
			if !r.handOn(bg, *t, pv, f) {
				r.finish(bg, processID, StateError, res.Refusal, false, f)
			}
			r.deps.Broke(bg, pv.Name())
		case blocked:
			r.turnText(bg, processID, res.Text)
			r.park(bg, processID)
		case verr == nil:
			r.turnText(bg, processID, res.Text)
			result, results := res.Text, r.cfg.Home.Results(processID)
			if len(t.cmd.Result) > 0 {
				kept, err := r.keepFiles(bg, t.project, processID, results, string(res.Structured))
				if err != nil {
					r.finish(bg, processID, StateError, string(res.Structured), false, fault.Of(err))
					return
				}
				result = kept
			} else if err := r.keepLeft(bg, t.project, processID, results); err != nil {
				r.warn(bg, processID, "deck kept none of the files the turn left: "+err.Error())
			}
			r.finish(bg, processID, StateDone, result, false, nil)
		case retried:
			raw := string(res.Structured)
			if raw == "" {
				raw = res.Text
			}
			r.finish(bg, processID, StateDone, raw, true, nil)
		default:
			retried = true
			req.Resume = true
			req.Prompt = "Your final JSON did not match the result schema: " + verr.Error() +
				"\nDo not redo the work. Send only the corrected JSON."
			if a.sub != nil {
				a.sub.Spend()
				req.Prompt = "You ended your turn without handing in the result with the result_submit tool: " + verr.Error() +
					"\nDo not redo the work. Call result_submit with only the JSON result."
			}
			continue
		}
		return
	}
}

// validate checks structured output against the result schema; null, nothing to do, only where the schema takes it.
func validate(cmd *manifest.Command, structured json.RawMessage) error {
	if len(cmd.Result) == 0 {
		return nil
	}
	if len(structured) == 0 {
		return errors.New("no structured output")
	}
	var v any
	if err := json.Unmarshal(structured, &v); err != nil {
		return err
	}
	return cmd.ValidateResult(v)
}

func callLimit(pv provider.Provider) time.Duration {
	if l, ok := pv.(provider.CallLimiter); ok {
		return l.CallLimit()
	}
	return 0
}

// submitted reports whether the session handed in a result.
func submitted(s *gateway.Submit) bool {
	got, _ := s.Result()
	return got != nil
}
