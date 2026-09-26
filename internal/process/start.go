package process

import (
	"context"
	"database/sql"
	"deck2/internal/ask"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Origin is who started a process: the process that called or triggered it, the trigger, the retry key.
type Origin struct {
	Parent  int64
	Trigger int64
	Key     string
}

func (r *Runner) ceilingOf(m *manifest.Manifest, cmd *manifest.Command) time.Duration {
	if cmd.TimeoutMinutes > 0 {
		return time.Duration(cmd.TimeoutMinutes) * time.Minute
	}
	if cmd.Executor.Type != KindScript {
		return r.cfg.Ceiling
	}
	if s := m.Script(cmd.Executor.Script); s != nil && s.TimeoutMinutes > 0 {
		return time.Duration(s.TimeoutMinutes) * time.Minute
	}
	return r.cfg.ScriptCeiling
}

// Start runs a command of the manifest revision the caller was authorized against in the background. A key
// of a process still going returns that process; a chain longer than MaxDepth is refused with a warning.
func (r *Runner) Start(ctx context.Context, projectID int64, m *manifest.Manifest, command string, input map[string]any, o Origin) (int64, error) {
	proj, err := r.st.Q.GetProject(ctx, projectID)
	if err != nil {
		return 0, err
	}
	cmd := m.Command(command)
	if cmd == nil {
		return 0, fmt.Errorf("project %q has no command %q", proj.Name, command)
	}
	kind, providerName, session := cmd.Executor.Type, r.provider.Name(), uuid.NewString()
	switch kind {
	case KindAgent:
		if _, err := r.commandBody(ctx, projectID, cmd); err != nil {
			return 0, err
		}
	case KindScript:
		if _, err := r.scriptSource(ctx, projectID, m, cmd); err != nil {
			return 0, err
		}
		providerName, session = "", ""
	default:
		return 0, registry.Invalid("deck: %s executors are not available yet", kind)
	}
	if input == nil {
		input = map[string]any{}
	}
	in, _ := json.Marshal(input)
	var p db.Process
	var refused error
	reused := false
	err = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if o.Trigger != 0 {
			t, err := q.GetTrigger(ctx, o.Trigger)
			if err != nil {
				return err
			}
			p, err := q.GetProject(ctx, projectID)
			if err != nil {
				return err
			}
			if t.Paused == 1 || p.Paused == 1 {
				return registry.Invalid("deck: %s was not started: its trigger or project is paused", command)
			}
		}
		if o.Key != "" {
			old, err := q.FindActiveProcess(ctx, db.FindActiveProcessParams{ProjectID: projectID, Command: command, RetryKey: o.Key})
			if err == nil {
				p, reused = old, true
				return nil
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		depth := int64(1)
		if o.Parent != 0 {
			parent, err := q.GetProcess(ctx, o.Parent)
			if err != nil {
				return err
			}
			if depth = parent.Depth + 1; depth > MaxDepth {
				text := fmt.Sprintf("deck stopped a chain of %d processes: %s.%s was not started after process %d (%s).",
					MaxDepth, proj.Name, command, parent.ID, parent.Command)
				refused = registry.Refuse("deck: %s was not started: a chain of processes may be at most %d long", command, MaxDepth)
				_, err := q.CreateAsk(ctx, db.CreateAskParams{ProcessID: parent.ID, Kind: ask.KindWarning, Text: text, Payload: "{}"})
				return err
			}
		}
		var err error
		p, err = q.CreateProcess(ctx, db.CreateProcessParams{ProjectID: projectID, Command: command, Kind: kind, State: StateRunning,
			Provider: providerName, Input: string(in), SessionID: session, ParentID: ref(o.Parent), Depth: depth,
			TriggerID: ref(o.Trigger), RetryKey: o.Key, CeilingMs: r.ceilingOf(m, cmd).Milliseconds(), ManifestRevision: m.Revision})
		return err
	})
	if err != nil {
		return 0, err
	}
	if refused != nil {
		r.bus.Publish("asks")
		return 0, refused
	}
	if reused {
		return p.ID, nil
	}
	r.bus.Publish("processes")
	r.spawn(turn{process: p.ID, project: projectID, m: m, cmd: cmd, input: string(in), session: session})
	return p.ID, nil
}

// scriptSource is the current text of the script a command runs.
func (r *Runner) scriptSource(ctx context.Context, projectID int64, m *manifest.Manifest, cmd *manifest.Command) (string, error) {
	s := m.Script(cmd.Executor.Script)
	src, err := r.projects.Text(ctx, projectID, s.File)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(src) == "" {
		return "", registry.Invalid("deck: script file %q of %s is missing or empty", s.File, cmd.Name)
	}
	return src, nil
}

func ref(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: id != 0} }

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
