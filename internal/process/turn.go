package process

import (
	"context"
	"deck2/internal/ask"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
}

func (r *Runner) run(ctx context.Context, t turn) {
	bg := context.WithoutCancel(ctx)
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
	dir := filepath.Join(r.cfg.RunsDir, fmt.Sprint(processID))
	work := filepath.Join(dir, "work")
	if err := errors.Join(os.MkdirAll(work, 0o700), os.MkdirAll(filepath.Join(dir, "results"), 0o700)); err != nil {
		r.finish(bg, processID, StateError, "", false, err.Error())
		return
	}
	proj, err := r.st.Q.GetProject(bg, t.project)
	if err != nil {
		r.finish(bg, processID, StateError, "", false, err.Error())
		return
	}
	tok, err := r.gw.Register(bg, &gateway.Session{ProcessID: processID, ProjectID: t.project, Workspace: proj.WorkspaceID, Trial: trial})
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
	_ = r.st.Tx(bg, func(ctx context.Context, q *db.Queries) error {
		return q.StartCeiling(ctx, db.StartCeilingParams{Now: r.now().UnixMilli(), ID: processID})
	})

	req := provider.Request{
		Cwd: work, Prompt: t.message, ResultSchema: t.cmd.Result,
		MCPURL: r.cfg.MCPBase + "/mcp/" + tok, AllowedTools: r.gw.AllowedTools(tok), PermissionMCP: gateway.PermissionTool(),
		SessionID: t.session, Resume: t.resume, Hidden: r.cfg.Hidden,
		OnStart: func(pid int) { r.setGroup(bg, processID, pid) },
		OnEvent: func(e provider.Event) { r.event(bg, processID, stream, e) },
	}
	if req.Skills, err = r.projects.Skills(bg, t.project, t.cmd.Executor.Skills); err != nil {
		r.finish(bg, processID, StateError, "", false, err.Error())
		return
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
		r.setGroup(bg, processID, 0)
		if ctx.Err() != nil {
			var stop stopCause
			if errors.As(context.Cause(ctx), &stop) {
				r.finish(bg, processID, StateStopped, "", false, stop.reason)
			}
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
		if err == nil && !res.NeedsLogin {
			r.health(bg, "ok", "")
		}
		verr := error(nil)
		blocked := err == nil && !res.NeedsLogin && !res.IsError && r.blocked(bg, processID)
		if err == nil && !res.NeedsLogin && !res.IsError && !blocked {
			verr = validate(t.cmd, res.Structured)
		}
		if (verr == nil || retried) && r.stopFirst(bg, processID) {
			return
		}
		if err != nil {
			r.finish(bg, processID, StateError, "", false, err.Error())
			return
		}
		if res.NeedsLogin {
			r.needLogin(bg, processID, res.Text)
			return
		}
		if res.IsError {
			r.finish(bg, processID, StateError, res.Text, false, "provider reported an error: "+res.Text)
			return
		}
		if blocked {
			r.park(bg, processID)
			return
		}
		if verr == nil {
			result := res.Text
			if len(t.cmd.Result) > 0 {
				result = string(res.Structured)
				if err := r.keepFiles(bg, t.project, processID, filepath.Join(dir, "results"), result); err != nil {
					r.finish(bg, processID, StateError, result, false, err.Error())
					return
				}
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

type logLine struct {
	At   string `json:"at"`
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`
}

// event appends a top-level provider event to the process log and moves its last output time.
func (r *Runner) event(ctx context.Context, processID int64, stream *os.File, e provider.Event) {
	now := r.now()
	b, _ := json.Marshal(logLine{At: store.Time(now), Kind: e.Kind, Text: e.Text})
	_, _ = stream.Write(append(b, '\n'))
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

// LogPath is the file with the log of a process while it is open.
func (r *Runner) LogPath(processID int64) string {
	return filepath.Join(r.cfg.RunsDir, fmt.Sprint(processID), "stream")
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
	var n int64
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		if n, err = q.ClaimProcess(ctx, db.ClaimProcessParams{ToState: StateNeedsLogin, ID: processID, FromState: StateRunning}); err != nil || n == 0 {
			return err
		}
		if err := q.PauseCeiling(ctx, db.PauseCeilingParams{Now: r.now().UnixMilli(), ID: processID}); err != nil {
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
	if n == 0 {
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

// park moves the process to waiting_answer, where its ceiling does not tick, and immediately delivers
// answers that arrived while it was running.
func (r *Runner) park(ctx context.Context, processID int64) {
	var n int64
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		if n, err = q.ClaimProcess(ctx, db.ClaimProcessParams{ToState: StateWaitingAnswer, ID: processID, FromState: StateRunning}); err != nil || n == 0 {
			return err
		}
		return q.PauseCeiling(ctx, db.PauseCeilingParams{Now: r.now().UnixMilli(), ID: processID})
	})
	if n == 0 {
		return
	}
	r.bus.Publish("processes")
	if err := r.Deliver(ctx, processID); err != nil {
		r.finish(ctx, processID, StateError, "", false, "resume after answer: "+err.Error())
	}
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
