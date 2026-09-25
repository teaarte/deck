package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"github.com/google/uuid"

	"deck2/internal/ask"
	"deck2/internal/events"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const (
	StateRunning       = "running"
	StateWaitingAnswer = "waiting_answer"
	StateNeedsLogin    = "needs_login"
	StateDone          = "done"
	StateError         = "error"
	StateInterrupted   = "interrupted"
)

// Runner owns process lifecycles. A process stops in waiting_answer when the agent is blocked on the
// owner; answers that arrive later are delivered in one resume message.
type Runner struct {
	st       *store.Store
	gw       *gateway.Gateway
	bus      *events.Bus
	provider provider.Provider
	runsDir  string
	mcpBase  string
	projects Projects

	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.Mutex
	stopped bool
}

// Projects gives the runner the current manifest and md prompt of a project.
type Projects interface {
	Manifest(ctx context.Context, projectID int64) (*manifest.Manifest, error)
	Prompt(ctx context.Context, projectID int64) (string, error)
	Text(ctx context.Context, projectID int64, name string) (string, error)
}

func NewRunner(st *store.Store, gw *gateway.Gateway, bus *events.Bus, p provider.Provider, runsDir, mcpBase string, projects Projects) *Runner {
	ctx, cancel := context.WithCancel(context.Background())
	return &Runner{st: st, gw: gw, bus: bus, provider: p, runsDir: runsDir, mcpBase: mcpBase, projects: projects, ctx: ctx, cancel: cancel}
}

// Shutdown interrupts running providers and waits; interrupted processes stay running and resume on next start.
func (r *Runner) Shutdown() {
	r.mu.Lock()
	r.stopped = true
	r.mu.Unlock()
	r.cancel()
	r.wg.Wait()
}

func (r *Runner) Wait() { r.wg.Wait() }

// Start creates a process for a command of the manifest revision the caller was authorized against and
// runs it in the background.
func (r *Runner) Start(ctx context.Context, projectID int64, m *manifest.Manifest, command string, input map[string]any) (int64, error) {
	proj, err := r.st.Q.GetProject(ctx, projectID)
	if err != nil {
		return 0, err
	}
	cmd := m.Command(command)
	if cmd == nil {
		return 0, fmt.Errorf("project %q has no command %q", proj.Name, command)
	}
	if cmd.Executor.Type != "agent" {
		return 0, registry.Invalid("deck: %s executors are not available yet", cmd.Executor.Type)
	}
	if _, err := r.commandBody(ctx, projectID, cmd); err != nil {
		return 0, err
	}
	if input == nil {
		input = map[string]any{}
	}
	in, _ := json.Marshal(input)
	var p db.Process
	session := uuid.NewString()
	err = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		p, err = q.CreateProcess(ctx, db.CreateProcessParams{ProjectID: projectID, Command: command, State: StateRunning,
			Provider: r.provider.Name(), Input: string(in)})
		if err != nil {
			return err
		}
		return q.SetProcessSession(ctx, db.SetProcessSessionParams{SessionID: session, ID: p.ID})
	})
	if err != nil {
		return 0, err
	}
	r.bus.Publish("processes")
	r.spawn(turn{process: p.ID, project: projectID, m: m, cmd: cmd, input: string(in), session: session})
	return p.ID, nil
}

func (r *Runner) commandBody(ctx context.Context, projectID int64, cmd *manifest.Command) (string, error) {
	if cmd.Executor.PromptFile == "" {
		return cmd.Executor.Prompt, nil
	}
	body, err := r.projects.Text(ctx, projectID, cmd.Executor.PromptFile)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(body) == "" {
		return "", registry.Invalid("deck: prompt file %q of %s is missing or empty", cmd.Executor.PromptFile, cmd.Name)
	}
	return body, nil
}

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
}

// Deliver resumes a process parked in waiting_answer with every answer or expiry it has not seen yet.
// It is safe to call concurrently: only one caller wins the waiting_answer -> running transition.
func (r *Runner) Deliver(ctx context.Context, processID int64) error {
	pending, err := r.st.Q.ListUndeliveredAsks(ctx, processID)
	if err != nil || len(pending) == 0 {
		return err
	}
	for _, a := range pending {
		if a.Kind == ask.KindLogin {
			return r.relogin(ctx, processID, a, pending)
		}
	}
	if ok, err := r.claim(ctx, processID, StateWaitingAnswer); err != nil || !ok {
		return err
	}
	if err := r.resume(ctx, processID, deliveryMessage(pending), askIDs(pending), false); err != nil {
		r.finish(ctx, processID, StateError, "", false, "resume after answer: "+err.Error())
		return err
	}
	return nil
}

func (r *Runner) claim(ctx context.Context, processID int64, from string) (bool, error) {
	var n int64
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		n, err = q.ClaimProcess(ctx, db.ClaimProcessParams{ToState: StateRunning, ID: processID, FromState: from})
		return err
	})
	return n > 0, err
}

// relogin continues a process that stopped on a lost login: the same session, or a new one that gets the
// gateway journal of the old one as its report of what was done.
func (r *Runner) relogin(ctx context.Context, processID int64, login db.Ask, pending []db.Ask) error {
	var n int64
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		if n, err = q.ClaimProcess(ctx, db.ClaimProcessParams{ToState: StateRunning, ID: processID, FromState: StateNeedsLogin}); err != nil || n == 0 {
			return err
		}
		return q.MarkAskDelivered(ctx, login.ID)
	})
	if err != nil || n == 0 {
		return err
	}
	var others []db.Ask
	for _, a := range pending {
		if a.Kind != ask.KindLogin {
			others = append(others, a)
		}
	}
	var msg string
	fresh := login.Answer == ask.LoginNewSession
	if fresh {
		msg = r.report(ctx, processID)
	} else {
		msg = "deck: you were logged out and the owner has logged in again. Continue where you stopped."
	}
	if len(others) > 0 {
		msg += "\n" + deliveryMessage(others)
	}
	if err := r.resume(ctx, processID, msg, askIDs(others), fresh); err != nil {
		r.finish(ctx, processID, StateError, "", false, "continue after login: "+err.Error())
		return err
	}
	return nil
}

// report tells a new session what the cut-off one did, from the gateway journal.
func (r *Runner) report(ctx context.Context, processID int64) string {
	calls, _ := r.st.Q.ListToolCalls(ctx, processID)
	var b strings.Builder
	b.WriteString("deck: an earlier session on this task was cut off by a lost login and cannot be continued.\n")
	if len(calls) == 0 {
		b.WriteString("It made no deck tool calls.\n")
	} else {
		b.WriteString("Its deck tool calls, in order:\n")
		for _, c := range calls {
			fmt.Fprintf(&b, "- %s: %s", c.Tool, c.Outcome)
			if c.Params != "" {
				fmt.Fprintf(&b, " %s", truncate(c.Params, 300))
			}
			b.WriteString("\n")
		}
	}
	b.WriteString("Check what already exists (records, files, cards) before redoing any of it.")
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

func askIDs(as []db.Ask) []int64 {
	ids := make([]int64, 0, len(as))
	for _, a := range as {
		ids = append(ids, a.ID)
	}
	return ids
}

// ResumeInterrupted continues processes left running by a previous deck instance, with any answers
// they had not received yet, and delivers answers that reached parked processes before the restart.
func (r *Runner) ResumeInterrupted(ctx context.Context) error {
	ps, err := r.st.Q.ListProcessesByState(ctx, StateRunning)
	if err != nil {
		return err
	}
	for _, p := range ps {
		if p.SessionID == "" {
			r.finish(ctx, p.ID, StateInterrupted, "", false, "deck restarted before the session started")
			continue
		}
		msg := "deck restarted, continue where you stopped."
		pending, err := r.st.Q.ListUndeliveredAsks(ctx, p.ID)
		if err != nil {
			return err
		}
		if len(pending) > 0 {
			msg = "deck restarted. " + deliveryMessage(pending)
		}
		if err := r.resume(ctx, p.ID, msg, askIDs(pending), false); err != nil {
			r.finish(ctx, p.ID, StateInterrupted, "", false, err.Error())
		}
	}
	for _, state := range []string{StateWaitingAnswer, StateNeedsLogin} {
		parked, err := r.st.Q.ListProcessesByState(ctx, state)
		if err != nil {
			return err
		}
		for _, p := range parked {
			_ = r.Deliver(ctx, p.ID)
		}
	}
	return nil
}

// resume spawns the provider on the existing session, or on a new one whose first prompt keeps the message.
// The caller has set the state to running; the asks are marked delivered once the provider returns.
func (r *Runner) resume(ctx context.Context, processID int64, message string, asks []int64, fresh bool) error {
	p, err := r.st.Q.GetProcess(ctx, processID)
	if err != nil {
		return err
	}
	if p.SessionID == "" {
		return errors.New("process has no session to resume")
	}
	_, m, err := r.load(ctx, p.ProjectID)
	if err != nil {
		return err
	}
	cmd := m.Command(p.Command)
	if cmd == nil {
		return fmt.Errorf("command %q no longer exists", p.Command)
	}
	t := turn{process: processID, project: p.ProjectID, m: m, cmd: cmd, input: p.Input, session: p.SessionID,
		resume: !fresh, message: message, asks: asks}
	if fresh {
		t.session, t.message = uuid.NewString(), ""
		err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.StartProcessSession(ctx, db.StartProcessSessionParams{SessionID: t.session, StartNote: message, ID: processID})
		})
		if err != nil {
			return err
		}
	}
	r.bus.Publish("processes")
	r.spawn(t)
	return nil
}

// spawn starts a turn unless deck is stopping; the process then stays running and resumes on the next start.
func (r *Runner) spawn(t turn) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.stopped {
		return
	}
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.run(t)
	}()
}

func (r *Runner) run(t turn) {
	ctx := r.ctx
	bg := context.WithoutCancel(ctx)
	processID := t.process
	dir := filepath.Join(r.runsDir, fmt.Sprint(processID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		r.finish(bg, processID, StateError, "", false, err.Error())
		return
	}
	proj, err := r.st.Q.GetProject(bg, t.project)
	if err != nil {
		r.finish(bg, processID, StateError, "", false, err.Error())
		return
	}
	tok, err := r.gw.Register(bg, &gateway.Session{ProcessID: processID, ProjectID: t.project, Workspace: proj.WorkspaceID})
	if err != nil {
		r.finish(bg, processID, StateError, "", false, err.Error())
		return
	}
	defer r.gw.Unregister(tok)
	tools := r.gw.Tools(tok)

	req := provider.Request{
		Cwd: dir, Prompt: t.message, ResultSchema: t.cmd.Result,
		MCPURL: r.mcpBase + "/mcp/" + tok, AllowedTools: r.gw.AllowedTools(tok), PermissionMCP: gateway.PermissionTool(),
		SessionID: t.session, Resume: t.resume,
	}
	if !t.resume {
		if req.Prompt, err = r.firstPrompt(bg, proj, t, tools); err != nil {
			r.finish(bg, processID, StateError, "", false, err.Error())
			return
		}
	}
	asks := t.asks
	for retried := false; ; {
		res, err := r.provider.Run(ctx, req)
		if ctx.Err() != nil {
			return
		}
		if err == nil && res.SessionMissing {
			// The asks stay undelivered, so the fresh session parks and gets them by resume.
			asks = nil
			req.Resume = false
			if req.Prompt, err = r.firstPrompt(bg, proj, t, tools); err != nil {
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
			return nil
		})
		asks = nil
		if err != nil {
			r.finish(bg, processID, StateError, "", false, err.Error())
			return
		}
		if res.NeedsLogin {
			r.needLogin(bg, processID, res.Text)
			return
		}
		r.health(bg, "ok", "")
		if res.IsError {
			r.finish(bg, processID, StateError, res.Text, false, "provider reported an error: "+res.Text)
			return
		}
		if r.blocked(bg, processID) {
			r.park(bg, processID)
			return
		}
		verr := validate(t.cmd, res.Structured)
		if verr == nil {
			result := res.Text
			if len(t.cmd.Result) > 0 {
				result = string(res.Structured)
			}
			r.finish(bg, processID, StateDone, result, false, "")
			return
		}
		if retried {
			raw := string(res.Structured)
			if raw == "" {
				raw = res.Text
			}
			r.finish(bg, processID, StateDone, raw, true, "")
			return
		}
		retried = true
		req.Resume = true
		req.Prompt = "Your final JSON did not match the result schema: " + verr.Error() +
			"\nDo not redo the work. Send only the corrected JSON."
	}
}

func (r *Runner) firstPrompt(ctx context.Context, proj db.Project, t turn, tools []string) (string, error) {
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
	p := buildPrompt(proj.Name, instructions, body, t.m, t.cmd, t.input, tools, r.provider.QuestionTool())
	if proc.StartNote != "" {
		p += "\n" + proc.StartNote + "\n"
	}
	return p, nil
}

// needLogin parks the process until the owner logs the provider in again and picks how to continue.
func (r *Runner) needLogin(ctx context.Context, processID int64, text string) {
	payload, _ := json.Marshal(map[string]any{"answers": ask.LoginAnswers})
	msg := fmt.Sprintf("%s needs a login: %s. Log in again in a terminal, then retry this session or start a new one with a report of what was done.",
		r.provider.Name(), strings.TrimSpace(text))
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := q.SetProcessState(ctx, db.SetProcessStateParams{State: StateNeedsLogin, ID: processID}); err != nil {
			return err
		}
		if _, err := q.CreateAsk(ctx, db.CreateAskParams{ProcessID: processID, Kind: ask.KindLogin, Text: msg, Payload: string(payload)}); err != nil {
			return err
		}
		return q.SetHealth(ctx, db.SetHealthParams{Subject: r.healthSubject(), Status: "needs_login", Error: text})
	})
	if err != nil {
		r.finish(ctx, processID, StateError, "", false, "needs login: "+text)
		return
	}
	r.bus.Publish("processes")
	r.bus.Publish("asks")
	r.bus.Publish("health")
}

func (r *Runner) healthSubject() string { return "provider:" + r.provider.Name() }

func (r *Runner) health(ctx context.Context, status, text string) {
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.SetHealth(ctx, db.SetHealthParams{Subject: r.healthSubject(), Status: status, Error: text})
	})
}

// blocked reports whether the owner still owes the agent an answer; a result produced meanwhile is
// premature (a schema forces the agent to answer even when told to stop), so the process parks instead.
func (r *Runner) blocked(ctx context.Context, processID int64) bool {
	open, _ := r.st.Q.ListOpenAsksOfProcess(ctx, processID)
	pending, _ := r.st.Q.ListUndeliveredAsks(ctx, processID)
	return len(open) > 0 || len(pending) > 0
}

// park moves the process to waiting_answer and immediately delivers answers that arrived while it was running.
func (r *Runner) park(ctx context.Context, processID int64) {
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.SetProcessState(ctx, db.SetProcessStateParams{State: StateWaitingAnswer, ID: processID})
	})
	r.bus.Publish("processes")
	if err := r.Deliver(ctx, processID); err != nil {
		r.finish(ctx, processID, StateError, "", false, "resume after answer: "+err.Error())
	}
}

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

func (r *Runner) finish(ctx context.Context, processID int64, state, result string, notBySchema bool, errText string) {
	nbs := int64(0)
	if notBySchema {
		nbs = 1
	}
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := q.FinishProcess(ctx, db.FinishProcessParams{State: state, Result: result, NotBySchema: nbs, Error: errText, ID: processID}); err != nil {
			return err
		}
		return q.CloseOpenAsksOfProcess(ctx, processID)
	})
	if err != nil {
		slog.Error("finish process", "process", processID, "err", err)
	}
	r.bus.Publish("processes")
	r.bus.Publish("asks")
}

func (r *Runner) load(ctx context.Context, projectID int64) (db.Project, *manifest.Manifest, error) {
	proj, err := r.st.Q.GetProject(ctx, projectID)
	if err != nil {
		return proj, nil, err
	}
	m, err := r.projects.Manifest(ctx, projectID)
	return proj, m, err
}

func deliveryMessage(asks []db.Ask) string {
	var b strings.Builder
	b.WriteString("deck: the owner has responded to what you were waiting for.\n")
	for _, a := range asks {
		switch {
		case a.Kind == ask.KindLogin:
		case a.Kind == "question" && a.State == "answered":
			b.WriteString("- Question: " + a.Text + "\n  Answer: " + a.Answer + "\n")
		case a.Kind == "confirm" && a.State == "answered" && a.Answer == "allow":
			b.WriteString("- Allowed: " + a.Text + ". Make exactly the same call again; it will go through.\n")
		case a.Kind == "confirm" && a.State == "answered":
			b.WriteString("- Rejected: " + a.Text + ". " + gateway.RejectMessage(a.Answer) + "\n")
		case a.Kind == "confirm":
			b.WriteString("- Expired: " + a.Text + ". The call was not made.\n")
		default:
			b.WriteString("- Expired without an answer: " + a.Text + "\n")
		}
	}
	b.WriteString("Continue the task.")
	return b.String()
}

// buildPrompt mentions only the tools the provider session really has.
func buildPrompt(project, instructions, body string, m *manifest.Manifest, cmd *manifest.Command, input string, tools []string, questionTool string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are running command %q of deck project %q.\n\n", cmd.Name, project)
	if strings.TrimSpace(instructions) != "" {
		fmt.Fprintf(&b, "Project instructions:\n%s\n\nCommand instructions:\n", strings.TrimSpace(instructions))
	}
	b.WriteString(body)
	fmt.Fprintf(&b, "\n\nInput: %s\n", input)
	if len(m.Collections) > 0 && slices.Contains(tools, "records_upsert") {
		b.WriteString("\nCollections of this project (write with the records_upsert tool of the deck MCP server, records as JSONL):\n")
		for _, c := range m.Collections {
			fmt.Fprintf(&b, "- %s: %s. Record JSON schema: %s\n", c.Name, c.Description, string(c.Schema))
		}
	}
	if questionTool != "" {
		fmt.Fprintf(&b, "\nIf you need a decision from the owner, ask with %s.\n", questionTool)
	}
	return b.String()
}
