package process

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

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

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
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
	r.cancel()
	r.wg.Wait()
}

func (r *Runner) Wait() { r.wg.Wait() }

// Start creates a process for a project command and runs it in the background.
func (r *Runner) Start(ctx context.Context, projectID int64, command string, input map[string]any) (int64, error) {
	proj, m, err := r.load(ctx, projectID)
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
	body := cmd.Executor.Prompt
	if cmd.Executor.PromptFile != "" {
		if body, err = r.projects.Text(ctx, projectID, cmd.Executor.PromptFile); err != nil {
			return 0, err
		}
		if strings.TrimSpace(body) == "" {
			return 0, registry.Invalid("deck: prompt file %q of %s is missing or empty", cmd.Executor.PromptFile, command)
		}
	}
	if input == nil {
		input = map[string]any{}
	}
	in, _ := json.Marshal(input)
	p, err := r.st.Q.CreateProcess(ctx, db.CreateProcessParams{ProjectID: projectID, Command: command, State: StateRunning,
		Provider: r.provider.Name(), Input: string(in)})
	if err != nil {
		return 0, err
	}
	r.bus.Publish("processes")
	instructions, err := r.projects.Prompt(ctx, projectID)
	if err != nil {
		return 0, err
	}
	r.spawn(p.ID, projectID, cmd, buildPrompt(proj.Name, instructions, body, m, cmd, string(in)), "", nil)
	return p.ID, nil
}

// Deliver resumes a process parked in waiting_answer with every answer or expiry it has not seen yet.
// It is safe to call concurrently: only one caller wins the waiting_answer -> running transition.
func (r *Runner) Deliver(ctx context.Context, processID int64) error {
	pending, err := r.st.Q.ListUndeliveredAsks(ctx, processID)
	if err != nil || len(pending) == 0 {
		return err
	}
	n, err := r.st.Q.ClaimProcess(ctx, db.ClaimProcessParams{ToState: StateRunning, ID: processID, FromState: StateWaitingAnswer})
	if err != nil || n == 0 {
		return err
	}
	if err := r.resume(ctx, processID, deliveryMessage(pending), askIDs(pending)); err != nil {
		r.finish(ctx, processID, StateError, "", false, "resume after answer: "+err.Error())
		return err
	}
	return nil
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
		if err := r.resume(ctx, p.ID, msg, askIDs(pending)); err != nil {
			r.finish(ctx, p.ID, StateInterrupted, "", false, err.Error())
		}
	}
	parked, err := r.st.Q.ListProcessesByState(ctx, StateWaitingAnswer)
	if err != nil {
		return err
	}
	for _, p := range parked {
		_ = r.Deliver(ctx, p.ID)
	}
	return nil
}

// resume spawns the provider on the existing session; the caller has already set the state to running.
// The asks are marked delivered once the provider returns, so a crash before that delivers them again.
func (r *Runner) resume(ctx context.Context, processID int64, message string, asks []int64) error {
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
	r.bus.Publish("processes")
	r.spawn(processID, p.ProjectID, cmd, message, p.SessionID, asks)
	return nil
}

func (r *Runner) spawn(processID, projectID int64, cmd *manifest.Command, prompt, resume string, asks []int64) {
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.run(processID, projectID, cmd, prompt, resume, asks)
	}()
}

func (r *Runner) run(processID, projectID int64, cmd *manifest.Command, prompt, resume string, asks []int64) {
	ctx := r.ctx
	bg := context.WithoutCancel(ctx)
	dir := filepath.Join(r.runsDir, fmt.Sprint(processID))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		r.finish(bg, processID, StateError, "", false, err.Error())
		return
	}
	tok, err := r.gw.Register(bg, &gateway.Session{ProcessID: processID, ProjectID: projectID})
	if err != nil {
		r.finish(bg, processID, StateError, "", false, err.Error())
		return
	}
	defer r.gw.Unregister(tok)

	req := provider.Request{
		Cwd: dir, Prompt: prompt, ResultSchema: cmd.Result,
		MCPURL: r.mcpBase + "/mcp/" + tok, AllowedTools: r.gw.AllowedTools(tok), PermissionMCP: gateway.PermissionTool(),
		ResumeSession: resume,
		OnSession: func(id string) {
			_ = r.st.Q.SetProcessSession(bg, db.SetProcessSessionParams{SessionID: id, ID: processID})
		},
	}
	for attempt := 0; ; attempt++ {
		res, err := r.provider.Run(ctx, req)
		if ctx.Err() != nil {
			return
		}
		for _, id := range asks {
			_ = r.st.Q.MarkAskDelivered(bg, id)
		}
		asks = nil
		if err != nil {
			r.finish(bg, processID, StateError, "", false, err.Error())
			return
		}
		if res.IsError {
			r.finish(bg, processID, StateError, res.Text, false, "provider reported an error")
			return
		}
		if r.blocked(bg, processID) {
			r.park(bg, processID)
			return
		}
		verr := validate(cmd, res.Structured)
		if verr == nil {
			result := res.Text
			if len(cmd.Result) > 0 {
				result = string(res.Structured)
			}
			r.finish(bg, processID, StateDone, result, false, "")
			return
		}
		if attempt > 0 {
			raw := string(res.Structured)
			if raw == "" {
				raw = res.Text
			}
			r.finish(bg, processID, StateDone, raw, true, "")
			return
		}
		req.ResumeSession = res.SessionID
		req.Prompt = "Your final JSON did not match the result schema: " + verr.Error() +
			"\nDo not redo the work. Send only the corrected JSON."
	}
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
	_ = r.st.Q.SetProcessState(ctx, db.SetProcessStateParams{State: StateWaitingAnswer, ID: processID})
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
	_ = r.st.Q.FinishProcess(ctx, db.FinishProcessParams{State: state, Result: result, NotBySchema: nbs, Error: errText, ID: processID})
	_ = r.st.Q.CloseOpenAsksOfProcess(ctx, processID)
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

func buildPrompt(project, instructions, body string, m *manifest.Manifest, cmd *manifest.Command, input string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "You are running command %q of deck project %q.\n\n", cmd.Name, project)
	if strings.TrimSpace(instructions) != "" {
		fmt.Fprintf(&b, "Project instructions:\n%s\n\nCommand instructions:\n", strings.TrimSpace(instructions))
	}
	b.WriteString(body)
	fmt.Fprintf(&b, "\n\nInput: %s\n", input)
	if len(m.Collections) > 0 {
		b.WriteString("\nCollections of this project (write with the records_upsert tool of the deck MCP server, records as JSONL):\n")
		for _, c := range m.Collections {
			fmt.Fprintf(&b, "- %s: %s. Record JSON schema: %s\n", c.Name, c.Description, string(c.Schema))
		}
	}
	b.WriteString("\nIf you need a decision from the owner, ask with AskUserQuestion.\n")
	return b.String()
}
