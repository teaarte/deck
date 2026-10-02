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
	"strconv"
	"strings"
	"time"

	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/provider"
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
	asks    []int64
	pv      provider.Provider
	// folder is the project folder the turn works in, or empty for a work folder of its own.
	folder string
	// live is the entry of the turn among the live ones, set when it is spawned.
	live *liveTurn
	// slot says the turn holds an agent slot of workspace, handed to it or taken when it starts.
	slot      bool
	workspace int64
}

func (r *Runner) run(ctx context.Context, t turn) {
	bg := context.WithoutCancel(ctx)
	defer func() {
		if t.slot {
			r.releaseSlot(bg, t.workspace)
		}
	}()
	processID := t.process
	p, err := r.st.Q.GetProcess(bg, processID)
	if err != nil || p.State != StateRunning {
		return
	}
	trial := p.Trial == 1
	switch t.cmd.Executor.Type {
	case KindScript:
		r.runScript(ctx, t, trial)
		return
	case KindTool:
		r.runTool(ctx, t, trial)
		return
	}
	pv, err := r.providers.Named(bg, t.project, p.Provider)
	if err != nil {
		r.finish(bg, processID, StateError, "", false, err.Error())
		return
	}
	if missing := pv.Caps().Missing(t.cmd.Requires); len(missing) > 0 {
		r.finish(bg, processID, StateError, "", false, fmt.Sprintf("deck: provider %s no longer fits %s: it lacks %s",
			pv.Name(), t.cmd.Name, strings.Join(missing, ", ")))
		return
	}
	t.pv = pv
	if !r.providers.Ready(bg, pv.Name()) {
		r.waitProvider(bg, t)
		return
	}
	proj, err := r.st.Q.GetProject(bg, t.project)
	if err != nil {
		r.finish(bg, processID, StateError, "", false, err.Error())
		return
	}
	t.folder = p.Folder
	if t.folder != "" {
		if real, err := filepath.EvalSymlinks(t.folder); err != nil || real != t.folder {
			r.finish(bg, processID, StateError, "", false, "deck: the project folder "+t.folder+" is gone or moved")
			return
		}
		if !r.holdFolder(bg, t, t.folder) {
			return
		}
	}
	if !t.slot {
		if !r.takeSlot(bg, proj.WorkspaceID) {
			r.waitSlot(bg, t, proj.WorkspaceID)
			return
		}
		t.slot, t.workspace = true, proj.WorkspaceID
	}
	dir := r.runDir(processID)
	work := filepath.Join(dir, "work")
	data := r.scripts.DataDir(t.project)
	repo := false
	if t.folder != "" {
		work = t.folder
		_, err := os.Stat(filepath.Join(work, ".git"))
		repo = err == nil
	} else if err := os.MkdirAll(work, 0o700); err != nil {
		r.finish(bg, processID, StateError, "", false, err.Error())
		return
	}
	if err := errors.Join(os.MkdirAll(filepath.Join(dir, "results"), 0o700), os.MkdirAll(data, 0o700)); err != nil {
		r.finish(bg, processID, StateError, "", false, err.Error())
		return
	}
	r.projects.Servers(bg, t.project)
	var sub *gateway.Submit
	if len(t.cmd.Result) > 0 && !slices.Contains(pv.Caps(), provider.StructuredOutput) {
		sub = &gateway.Submit{Schema: t.cmd.Result, Validate: func(raw json.RawMessage) error { return validate(t.cmd, raw) }}
	}
	tok, err := r.gw.Register(bg, &gateway.Session{ProcessID: processID, ProjectID: t.project, Workspace: proj.WorkspaceID, Trial: trial, Submit: sub,
		Ask: pv.QuestionTool() == provider.AskTool, Wait: callLimit(pv), Master: proj.Builtin == 1, Commands: t.cmd.Executor.Calls})
	if err != nil {
		r.finish(bg, processID, StateError, "", false, err.Error())
		return
	}
	defer r.gw.Unregister(tok)
	tools := r.gw.Tools(tok)
	stream, err := os.OpenFile(filepath.Join(dir, "stream"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		r.finish(bg, processID, StateError, "", false, err.Error())
		return
	}
	defer stream.Close()
	err = r.st.Tx(bg, func(ctx context.Context, q *db.Queries) error {
		return q.StartCeiling(ctx, db.StartCeilingParams{Now: r.now().UnixMilli(), ID: processID})
	})
	if err != nil {
		r.finish(bg, processID, StateError, "", false, "start the ceiling: "+err.Error())
		return
	}

	noNetwork := t.m.NoNetwork
	if now, err := r.projects.ManifestAt(bg, t.project, 0); err == nil && now.NoNetwork {
		noNetwork = true
	}
	req := provider.Request{
		Cwd: work, Repo: repo, Prompt: t.message, ResultSchema: t.cmd.Result,
		MCPURL: r.cfg.MCPBase + "/mcp/" + tok, AllowedTools: r.gw.AllowedTools(tok), PermissionMCP: gateway.PermissionTool(),
		SessionID: t.session, Resume: t.resume, Hidden: r.cfg.Hidden(t.project, processID), Dirs: []string{data, filepath.Join(dir, "results")}, NoNetwork: noNetwork,
		OnStart: func(pid int) { r.setGroup(bg, processID, pid) },
		OnEvent: func(e provider.Event) { r.event(bg, processID, stream, e) },
		OnUsage: func(u provider.Usage) { r.usage(bg, processID, pv.Name(), u) },
	}
	var in chan provider.Message
	if slices.Contains(pv.Caps(), provider.LiveInput) && t.live != nil {
		in = make(chan provider.Message)
		req.Live = in
	}
	listen := func(on bool) {
		if in == nil {
			return
		}
		r.mu.Lock()
		if t.live.input = nil; on {
			t.live.input = in
		}
		r.mu.Unlock()
	}
	if sub != nil {
		req.ResultSchema = nil
		if t.resume {
			req.Prompt += "\nWhen you are done, hand in the whole final result with the result_submit tool of the deck MCP server, then end your turn.\n"
		}
	}
	if req.Skills, err = r.projects.Skills(bg, t.project, t.cmd.Executor.Skills); err != nil {
		r.finish(bg, processID, StateError, "", false, err.Error())
		return
	}
	if t.input, _, err = r.inputFiles(bg, t.cmd, t.project, processID, t.input, true); err != nil {
		r.finish(bg, processID, StateError, "", false, err.Error())
		return
	}
	if !t.resume {
		if req.Prompt, err = r.firstPrompt(bg, proj, t, tools, data); err != nil {
			r.finish(bg, processID, StateError, "", false, err.Error())
			return
		}
	}
	asks := t.asks
	for retried := false; ; {
		listen(true)
		res, err := pv.Run(ctx, req)
		listen(false)
		r.setGroup(bg, processID, 0)
		if r.cancelled(bg, ctx, processID, func() {}) {
			return
		}
		if err == nil && res.SessionMissing {
			// The asks stay undelivered, so the fresh session parks and gets them by resume.
			asks = nil
			req.Resume = false
			if req.Prompt, err = r.firstPrompt(bg, proj, t, tools, data); err != nil {
				r.finish(bg, processID, StateError, "", false, err.Error())
				return
			}
			continue
		}
		_ = r.st.Tx(bg, func(ctx context.Context, q *db.Queries) error {
			for _, id := range asks {
				if err := q.MarkAskDelivered(ctx, id); err != nil {
					return err
				}
			}
			if err == nil && !res.RateLimited {
				return q.EndPause(ctx, processID)
			}
			return nil
		})
		asks = nil
		r.providerHealth(bg, pv, res, err)
		settled := err == nil && !res.NeedsLogin && !res.RateLimited && !res.IsError
		if sub != nil && settled {
			var spent bool
			res.Structured, spent = sub.Result()
			retried = retried || spent
		}
		verr := error(nil)
		blocked := settled && r.blocked(bg, processID)
		if settled && !blocked {
			verr = validate(t.cmd, res.Structured)
		}
		if (verr == nil || retried) && r.stopFirst(bg, processID) {
			return
		}
		switch {
		case err != nil:
			r.finish(bg, processID, StateError, "", false, err.Error())
		case res.RateLimited:
			r.pause(bg, pv, processID, res.ResetsAt, res.Text)
		case res.NeedsLogin:
			r.needLogin(bg, pv, processID, res.Text)
		case res.IsError:
			r.finish(bg, processID, StateError, res.Text, false, "provider reported an error: "+res.Text)
		case blocked:
			r.turnText(bg, processID, res.Text)
			r.park(bg, processID)
		case verr == nil:
			r.turnText(bg, processID, res.Text)
			result := res.Text
			if len(t.cmd.Result) > 0 {
				result = string(res.Structured)
				if err := r.keepFiles(bg, t.project, processID, filepath.Join(dir, "results"), result); err != nil {
					r.finish(bg, processID, StateError, result, false, err.Error())
					return
				}
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
			if sub != nil {
				sub.Spend()
				req.Prompt = "You ended your turn without handing in the result with the result_submit tool: " + verr.Error() +
					"\nDo not redo the work. Call result_submit with only the JSON result."
			}
			continue
		}
		return
	}
}

func (r *Runner) turnText(ctx context.Context, processID int64, text string) {
	if r.OnTurn != nil {
		r.OnTurn(ctx, processID, text)
	}
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
	r.bus.Publish("log")
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
	wasSilent := false
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		p, err := q.GetProcess(ctx, processID)
		if err != nil {
			return err
		}
		wasSilent = p.SilentSinceMs.Valid
		return q.SetProcessOutput(ctx, db.SetProcessOutputParams{OutputMs: now.UnixMilli(), ID: processID})
	})
	if wasSilent {
		r.bus.Publish("processes")
	}
}

// runDir is the folder of an open process: its work, results and log.
func (r *Runner) runDir(processID int64) string {
	return filepath.Join(r.cfg.RunsDir, strconv.FormatInt(processID, 10))
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
	instructions, err := r.projects.Prompt(ctx, t.project)
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
	mem, err := r.projects.Memory(ctx, proc)
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

// validate checks structured output against the result schema; an empty result (null) always fits.
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
	if v == nil {
		return nil
	}
	return cmd.ValidateResult(v)
}

func callLimit(pv provider.Provider) time.Duration {
	if l, ok := pv.(provider.CallLimiter); ok {
		return l.CallLimit()
	}
	return 0
}
