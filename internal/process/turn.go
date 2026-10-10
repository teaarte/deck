package process

import (
	"bufio"
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
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
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
	fail := func(msg string) { r.finish(bg, t.process, StateError, "", false, msg) }
	a, ok := r.prepare(bg, t, p, trial, fail)
	if !ok {
		return
	}
	defer a.cleanup()
	r.converse(ctx, t, a, fail)
}

// prepare gets a turn its provider, folder and slot, or leaves it waiting for them, then its folders, gateway
// session, log and request; false when the turn does not go on.
func (r *Runner) prepare(ctx context.Context, t *turn, p db.Process, trial bool, fail func(string)) (*agentTurn, bool) {
	pv, err := r.deps.Named(ctx, t.project, p.Provider)
	if err != nil {
		fail(err.Error())
		return nil, false
	}
	if missing := pv.Caps().Missing(t.cmd.Requires); len(missing) > 0 {
		fail(fmt.Sprintf("deck: provider %s no longer fits %s: it lacks %s", pv.Name(), t.cmd.Name, strings.Join(missing, ", ")))
		return nil, false
	}
	t.pv = pv
	if !r.deps.Ready(ctx, pv.Name()) {
		r.hold(ctx, *t, StateWaitingProvider)
		return nil, false
	}
	proj, err := r.st.Q.GetProject(ctx, t.project)
	if err != nil {
		fail(err.Error())
		return nil, false
	}
	t.folder = p.Folder
	if t.folder != "" {
		if real, err := filepath.EvalSymlinks(t.folder); err != nil || real != t.folder {
			fail("deck: the project folder " + t.folder + " is gone or moved")
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
	work, repo := filepath.Join(a.dir, "work"), false
	if t.folder != "" {
		work = t.folder
		_, err := os.Stat(filepath.Join(work, ".git"))
		repo = err == nil
	} else if err := os.MkdirAll(work, 0o700); err != nil {
		fail(err.Error())
		return nil, false
	}
	if err := errors.Join(os.MkdirAll(filepath.Join(a.dir, "results"), 0o700), os.MkdirAll(a.data, 0o700)); err != nil {
		fail(err.Error())
		return nil, false
	}
	r.deps.Servers(ctx, t.project)
	if len(t.cmd.Result) > 0 && !slices.Contains(pv.Caps(), provider.StructuredOutput) {
		a.sub = &gateway.Submit{Schema: t.cmd.Result, Validate: func(raw json.RawMessage) error { return validate(t.cmd, raw) }}
	}
	tok, err := r.gw.Register(ctx, &gateway.Session{ProcessID: t.process, ProjectID: t.project, Workspace: proj.WorkspaceID, Trial: trial, Submit: a.sub,
		Ask: pv.QuestionTool() == provider.AskTool, Wait: callLimit(pv), Master: proj.Builtin == 1, Commands: t.cmd.Executor.Calls})
	if err != nil {
		fail(err.Error())
		return nil, false
	}
	a.tools = r.gw.Tools(tok)
	stream, err := os.OpenFile(filepath.Join(a.dir, "stream"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		r.gw.Unregister(tok)
		fail(err.Error())
		return nil, false
	}
	a.cleanup = func() { stream.Close(); r.gw.Unregister(tok) }
	if err := r.startCeiling(ctx, t.process); err != nil {
		a.cleanup()
		fail("start the ceiling: " + err.Error())
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
		SessionID: t.session, Resume: t.resume, Hidden: r.cfg.Hidden(), Dirs: []string{a.data, filepath.Join(a.dir, "results")}, Read: read, NoNetwork: noNetwork, Hosts: hosts,
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
	}
	if err != nil {
		a.cleanup()
		fail(err.Error())
		return nil, false
	}
	return a, true
}

// converse runs the provider until the turn has an outcome: done by the schema, parked for the owner, paused on
// a limit, a lost login or an error; a result off the schema is asked for once more.
func (r *Runner) converse(ctx context.Context, t *turn, a *agentTurn, fail func(string)) {
	bg := context.WithoutCancel(ctx)
	processID, pv, req := t.process, t.pv, a.req
	asks := t.asks
	for retried := false; ; {
		a.listen(true)
		res, err := pv.Run(ctx, req)
		a.listen(false)
		r.setGroup(bg, processID, 0)
		if r.cut(bg, ctx, processID) {
			return
		}
		if err == nil && res.SessionMissing {
			// The owner's messages go with the fresh session; the asks stay undelivered, so it parks for them.
			asks = told{Remarks: asks.Remarks}
			req.Resume = false
			if req.Prompt, err = r.firstPrompt(bg, a.proj, *t, a.tools, a.data); err != nil {
				fail(err.Error())
				return
			}
			req.Prompt += r.remarksText(bg, processID, asks.Remarks)
			continue
		}
		if err != nil && res.NoTools {
			t.asks, t.resume = asks, req.Resume
			if req.Resume {
				t.message = req.Prompt
			}
			r.lostTools(bg, *t, pv, err)
			return
		}
		if a.sub != nil && err == nil && !res.IsError && submitted(a.sub) {
			// A turn that ended on its own with its result in is done, whatever a later call of the model got.
			res.RateLimited, res.NeedsLogin = false, false
		}
		_ = r.st.Tx(bg, func(ctx context.Context, q *db.Queries) error { return asks.mark(ctx, q) })
		asks = told{}
		r.providerHealth(bg, pv, res, err)
		settled := err == nil && !res.NeedsLogin && !res.RateLimited && !res.IsError
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
		case (err != nil || res.IsError) && r.questioned(bg, processID):
			r.park(bg, processID)
		case err != nil:
			fail(err.Error())
			r.deps.Broke(bg, pv.Name())
		case res.RateLimited:
			r.pause(bg, pv, processID, res.ResetsAt, res.Text)
		case res.NeedsLogin:
			r.needLogin(bg, pv, processID, res.Text)
		case res.IsError:
			r.finish(bg, processID, StateError, res.Text, false, "provider reported an error: "+res.Text)
			r.deps.Broke(bg, pv.Name())
		case blocked:
			r.turnText(bg, processID, res.Text)
			r.park(bg, processID)
		case verr == nil:
			r.turnText(bg, processID, res.Text)
			result, results := res.Text, filepath.Join(a.dir, "results")
			if len(t.cmd.Result) > 0 {
				kept, err := r.keepFiles(bg, t.project, processID, results, string(res.Structured))
				if err != nil {
					r.finish(bg, processID, StateError, string(res.Structured), false, err.Error())
					return
				}
				result = kept
			} else if err := r.keepLeft(bg, t.project, processID, results); err != nil {
				r.finish(bg, processID, StateError, result, false, err.Error())
				return
			}
			r.finish(bg, processID, StateDone, result, false, "")
		case retried:
			raw := string(res.Structured)
			if raw == "" {
				raw = res.Text
			}
			r.finish(bg, processID, StateDone, raw, true, "")
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

func (r *Runner) turnText(ctx context.Context, processID int64, text string) {
	_ = r.hear(ctx, processID, func(h Hook, p db.Process) { h.Turn(ctx, p, text) })
}

// logLine is a line of the process log: an event in the words of ACP session/update.
type logLine struct {
	At     string `json:"at"`
	Kind   string `json:"kind"`
	ID     string `json:"id,omitempty"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
	Text   string `json:"text,omitempty"`
}

// event appends a top-level provider event to the process log and moves its last output time, at most once a second.
func (r *Runner) event(ctx context.Context, processID int64, stream *os.File, e provider.Event) {
	now := r.now()
	b, _ := json.Marshal(logLine{At: store.Time(now), Kind: e.Kind, ID: e.ID, Title: e.Title, Status: e.Status, Text: untrusted.Cut(e.Text, 4000)})
	_, _ = stream.Write(append(b, '\n'))
	r.logged(ctx, processID)
	r.mu.Lock()
	last, seen := r.marked[processID]
	due := !seen || now.Sub(last) >= time.Second
	if due {
		r.marked[processID] = now
	}
	r.mu.Unlock()
	if !due {
		return
	}
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.SetProcessOutput(ctx, db.SetProcessOutputParams{OutputMs: now.UnixMilli(), ID: processID})
	})
}

// runDir is the folder of an open process: its work, results and log.
func (r *Runner) runDir(processID int64) string {
	return r.cfg.Home.Run(processID)
}

// clearRun removes what a script or tool left in its run folder but its log.
func (r *Runner) clearRun(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "stream" {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// Tail is the last n lines of the log of a process, only those of the kinds given when any are; a process
// without a log has none.
func (r *Runner) Tail(processID int64, n int, kinds ...string) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	f, err := os.Open(r.LogPath(processID))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var line struct{ Kind string }
		if json.Unmarshal(sc.Bytes(), &line) != nil || len(kinds) > 0 && !slices.Contains(kinds, line.Kind) {
			continue
		}
		if out = append(out, append(json.RawMessage{}, sc.Bytes()...)); len(out) > n {
			out = out[1:]
		}
	}
	return out, sc.Err()
}

// LogPath is the file with the log of a process.
func (r *Runner) LogPath(processID int64) string {
	return filepath.Join(r.runDir(processID), "stream")
}

func (r *Runner) firstPrompt(ctx context.Context, proj db.Project, t turn, tools []string, data string) (string, error) {
	body, err := r.commandBody(ctx, t.project, t.cmd)
	if err != nil {
		return "", err
	}
	instructions, err := r.deps.Prompt(ctx, t.project)
	if err != nil {
		return "", err
	}
	proc, err := r.st.Q.GetProcess(ctx, t.process)
	if err != nil {
		return "", err
	}
	p := buildPrompt(t.process, proj.Name, instructions, body, t.m, t.cmd, t.input, tools, t.pv.QuestionTool())
	if slices.Contains(t.pv.Caps(), provider.Sandbox) {
		p += "\nThe data folder of this project keeps indexes, caches and state between runs: " + data + "\n"
	}
	if t.folder != "" {
		p += "\nYou work in the project folder " + t.folder + ": the owner's own folder, not a scratch copy. No other agent of deck works in it while you do.\n"
	}
	mem, err := r.deps.Memory(ctx, proc)
	if err != nil {
		return "", err
	}
	if mem != "" {
		p += "\n" + mem + "\n"
	}
	if proc.StartNote != "" {
		p += "\n" + proc.StartNote + "\n"
	}
	return p, nil
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
