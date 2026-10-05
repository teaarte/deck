package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"

	"github.com/google/uuid"
)

// Started is what a call that starts a process gives back.
type Started struct {
	Process int64 `json:"process"`
}

// Origin is who started a process: the process that called or triggered it, the trigger, the retry key;
// Trial asks for a trial run, which processes of a draft and children of a trial run always are.
type Origin struct {
	Parent  int64
	Trigger int64
	// Confirmed is a call of the trigger the owner said yes to; its schedule checks do not apply.
	Confirmed bool
	Key       string
	Trial     bool
	// Session is an existing session the agent continues, by resume with Message when Resume is set.
	Session string
	Resume  bool
	Message string
	// Note goes into the first prompt of a fresh session, also of one that replaces a lost session.
	Note string
	// Files are stored files of the project given to the agent with its first message, as with Give.
	Files []int64
}

func (r *Runner) ceilingOf(m *manifest.Manifest, cmd *manifest.Command) time.Duration {
	if cmd.TimeoutMinutes > 0 {
		return time.Duration(cmd.TimeoutMinutes) * time.Minute
	}
	if cmd.Executor.Type == KindTool {
		return ScriptCeiling
	}
	if cmd.Executor.Type != KindScript {
		return Ceiling
	}
	if s := m.Script(cmd.Executor.Script); s != nil && s.TimeoutMinutes > 0 {
		return time.Duration(s.TimeoutMinutes) * time.Minute
	}
	return ScriptCeiling
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
	kind, providerName, session, folder := cmd.Executor.Type, "", uuid.NewString(), ""
	switch kind {
	case KindAgent:
		pv, err := r.deps.For(ctx, projectID, cmd)
		if err != nil {
			return 0, err
		}
		providerName = pv.Name()
		if _, err := r.commandBody(ctx, projectID, cmd); err != nil {
			return 0, err
		}
		if _, err := r.deps.Skills(ctx, projectID, cmd.Executor.Skills); err != nil {
			return 0, err
		}
		if o.Session != "" {
			session = o.Session
		}
		if !cmd.Executor.Folder.IsZero() {
			if !slices.Contains(pv.Caps(), provider.Folder) {
				return 0, registry.Invalid("deck: %s works in a project folder, and provider %s cannot yet; pick Claude Code or Codex for it", cmd.Name, pv.Name())
			}
			if folder, err = r.deps.Folder(ctx, projectID, cmd.Executor.Folder); err != nil {
				return 0, err
			}
		}
	case KindScript:
		if _, err := r.scriptSource(ctx, projectID, m, cmd); err != nil {
			return 0, err
		}
		session = ""
	case KindTool:
		session = ""
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
	trial := o.Trial
	err = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		pr, err := q.GetProject(ctx, projectID)
		if err != nil {
			return err
		}
		trial = trial || pr.Status == store.ProjectDraft
		var parent db.Process
		if o.Parent != 0 {
			if parent, err = q.GetProcess(ctx, o.Parent); err != nil {
				return err
			}
			trial = trial || parent.Trial == 1
		}
		if o.Trigger != 0 && !o.Confirmed {
			t, err := q.GetTrigger(ctx, o.Trigger)
			if err != nil {
				return err
			}
			if t.Paused == 1 || pr.Paused == 1 {
				return registry.Invalid("deck: %s was not started: its trigger or project is paused", command)
			}
			if (t.Kind == "cron" || t.Kind == "once") && t.NextRunAt.String > store.Time(r.now()) {
				return registry.Invalid("deck: %s was not started: its trigger moved to %s since it was due", command, t.NextRunAt.String)
			}
			if pr.Status == store.ProjectDraft && parent.Trial != 1 {
				return registry.Invalid("deck: %s was not started: project %s is a draft; only a trial run starts its triggers", command, pr.Name)
			}
		}
		if o.Key != "" {
			old, err := q.FindActiveProcess(ctx, db.FindActiveProcessParams{ProjectID: projectID, Command: command, RetryKey: o.Key, Trial: b2i(trial)})
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
			if depth = parent.Depth + 1; depth > MaxDepth {
				text := fmt.Sprintf("deck stopped a chain of %d processes: %s.%s was not started after process %d (%s).",
					MaxDepth, proj.Name, command, parent.ID, parent.Command)
				refused = registry.Refuse("deck: %s was not started: a chain of processes may be at most %d long", command, MaxDepth)
				return q.AddDeckNote(ctx, db.AddDeckNoteParams{ProjectID: proj.ID, Text: text})
			}
		}
		// A call the owner allowed later may come from a trigger that is gone by then, as a run later is.
		trigger := o.Trigger
		if _, err := q.GetTrigger(ctx, trigger); errors.Is(err, sql.ErrNoRows) {
			trigger = 0
		}
		p, err = q.CreateProcess(ctx, db.CreateProcessParams{Trial: b2i(trial), ProjectID: projectID, Command: command, Kind: kind, State: StateRunning,
			Provider: providerName, Input: string(in), SessionID: session, ParentID: ref(o.Parent), Depth: depth,
			TriggerID: ref(trigger), RetryKey: o.Key, CeilingMs: r.ceilingOf(m, cmd).Milliseconds(), ManifestRevision: m.Revision, Folder: folder,
			StartNote: o.Note})
		if err != nil {
			return err
		}
		if trigger != 0 {
			if err := q.SetTriggerLast(ctx, db.SetTriggerLastParams{LastProcessID: sql.NullInt64{Int64: p.ID, Valid: true}, ID: trigger}); err != nil {
				return err
			}
		}
		return bindInput(ctx, q, cmd, projectID, p.ID, input)
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
	if len(o.Files) > 0 {
		line, err := r.Give(ctx, p.ID, o.Files)
		if err == nil {
			o.Message = strings.TrimSpace(o.Message + "\n\n" + line)
			err = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
				return q.SetStartNote(ctx, db.SetStartNoteParams{StartNote: strings.TrimSpace(o.Note + "\n\n" + line), ID: p.ID})
			})
		}
		if err != nil {
			r.finish(ctx, p.ID, StateError, "", false, "give files: "+err.Error())
			return 0, err
		}
	}
	r.changed(ctx, p.ID, "processes")
	resume := o.Resume && kind == KindAgent && o.Session != ""
	r.spawn(turn{process: p.ID, project: projectID, m: m, cmd: cmd, input: string(in), session: session, resume: resume, message: o.Message})
	return p.ID, nil
}

// scriptSource is the current text of the script a command runs.
func (r *Runner) scriptSource(ctx context.Context, projectID int64, m *manifest.Manifest, cmd *manifest.Command) (string, error) {
	s := m.Script(cmd.Executor.Script)
	src, err := r.deps.Text(ctx, projectID, "script", s.File)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(src) == "" {
		return "", registry.Invalid("deck: script file %q of %s is missing, empty or not a script document", s.File, cmd.Name)
	}
	return src, nil
}

func b2i(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func ref(id int64) sql.NullInt64 { return sql.NullInt64{Int64: id, Valid: id != 0} }

func (r *Runner) commandBody(ctx context.Context, projectID int64, cmd *manifest.Command) (string, error) {
	if cmd.Executor.PromptFile == "" {
		return cmd.Executor.Prompt, nil
	}
	body, err := r.deps.Text(ctx, projectID, "", cmd.Executor.PromptFile)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(body) == "" {
		return "", registry.Invalid("deck: prompt file %q of %s is missing or empty", cmd.Executor.PromptFile, cmd.Name)
	}
	return body, nil
}
