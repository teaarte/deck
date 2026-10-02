package app

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/trigger"
)

// ownTrigger loads a trigger of the actor's workspace; agents reach only their own project.
func (a *App) ownTrigger(ctx context.Context, act registry.Actor, id int64) (db.Trigger, db.Project, error) {
	t, err := a.Store.Q.GetTrigger(ctx, id)
	return owned(ctx, a.Store.Q, act, t, err, t.ProjectID, registry.NotFound("deck: no trigger %d", id))
}

type runLaterIn struct {
	Command string `desc:"Command name, project.command"`
	At      string `type:"datetime" desc:"When to run"`
	Params  string `type:"text" opt:"" desc:"Command input as a JSON object"`
}

func (a *App) runLater(ctx context.Context, act registry.Actor, in runLaterIn) (any, error) {
	if act.Trial {
		return nil, registry.Refuse("deck: trial run: run later would start a real run, so it was declined")
	}
	at, err := time.Parse(time.RFC3339, in.At)
	if err != nil {
		return nil, registry.Invalid("deck: at: %v", err)
	}
	if strings.TrimSpace(in.Params) == "" {
		in.Params = "{}"
	}
	params, err := manifest.DecodeObject([]byte(in.Params))
	if err != nil {
		return nil, registry.Invalid("deck: params: %v", err)
	}
	// The class rules apply when the trigger fires, so only the caller and the input are checked now.
	cmd, _, err := a.Registry.Resolve(ctx, act, in.Command, json.RawMessage(in.Params))
	if err != nil {
		return nil, err
	}
	if cmd.ProjectID == 0 || !slices.Contains(cmd.Callers, registry.Trigger) {
		return nil, registry.Invalid("deck: %s cannot be run later: only project commands that triggers may call", in.Command)
	}
	_, name, _ := strings.Cut(in.Command, ".")
	raw, _ := json.Marshal(params)
	var t db.Trigger
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if p, err := q.GetProject(ctx, cmd.ProjectID); err != nil || p.Status == store.ProjectDraft {
			if err == nil {
				err = registry.Invalid("deck: %s is a draft; it runs on a schedule after the owner enables it", p.Name)
			}
			return err
		}
		t, err = q.CreateOnceTrigger(ctx, db.CreateOnceTriggerParams{ProjectID: cmd.ProjectID, Command: name, Params: string(raw),
			NextRunAt: sql.NullString{String: store.Time(at), Valid: true}})
		return err
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("triggers")
	return map[string]any{"trigger": t.ID, "at": t.NextRunAt.String}, nil
}

type runLaterCancelIn struct {
	Trigger int64 `desc:"Trigger id"`
}

func (a *App) runLaterCancel(ctx context.Context, act registry.Actor, in runLaterCancelIn) (any, error) {
	t, _, err := a.ownTrigger(ctx, act, in.Trigger)
	if err != nil {
		return nil, err
	}
	if t.Kind != trigger.KindOnce {
		return nil, registry.Invalid("deck: trigger %d comes from the manifest; pause it instead", t.ID)
	}
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.DeleteTrigger(ctx, t.ID) })
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("triggers")
	return map[string]any{"ok": true}, nil
}

type triggersListIn struct {
	Project string `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
}

// triggerView is a trigger with its next run, whose pause holds it, and how its last run ended.
type triggerView struct {
	ID        int64           `json:"id"`
	Project   string          `json:"project"`
	Command   string          `json:"command"`
	Kind      string          `json:"kind"`
	Spec      string          `json:"spec"`
	Params    json.RawMessage `json:"params"`
	NextRunAt string          `json:"next_run_at"`
	// Paused is the trigger's own pause or its project's; ProjectPaused and OwnPaused say which.
	Paused        bool   `json:"paused"`
	ProjectPaused bool   `json:"project_paused"`
	OwnPaused     bool   `json:"own_paused"`
	SkipMissed    bool   `json:"skip_missed"`
	LastProcess   int64  `json:"last_process"`
	LastState     string `json:"last_state"`
}

func (a *App) triggersList(ctx context.Context, act registry.Actor, in triggersListIn) ([]triggerView, error) {
	pid, err := a.filter(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	ts, err := a.Store.Q.ListWorkspaceTriggers(ctx, db.ListWorkspaceTriggersParams{WorkspaceID: act.Workspace, ProjectID: pid})
	if err != nil {
		return nil, err
	}
	names := map[int64]db.Project{}
	out := make([]triggerView, 0, len(ts))
	for _, t := range ts {
		p, ok := names[t.ProjectID]
		if !ok {
			if p, err = a.Store.Q.GetProject(ctx, t.ProjectID); err != nil {
				return nil, err
			}
			names[t.ProjectID] = p
		}
		v := triggerView{ID: t.ID, Project: p.Name, Command: t.Command, Kind: t.Kind, Spec: t.Spec, Params: json.RawMessage(cmp.Or(t.Params, "null")),
			NextRunAt: t.NextRunAt.String, Paused: t.Paused == 1 || p.Paused == 1, ProjectPaused: p.Paused == 1, OwnPaused: t.Paused == 1,
			SkipMissed: t.SkipMissed == 1}
		if t.LastProcessID.Valid {
			v.LastProcess = t.LastProcessID.Int64
			if lp, err := a.Store.Q.GetProcess(ctx, v.LastProcess); err == nil {
				v.LastState = lp.State
			}
		}
		out = append(out, v)
	}
	return out, nil
}

type triggerRunIn struct {
	Trigger int64 `desc:"Trigger id"`
}

// triggerRun starts the command of a schedule trigger now with its params, with the trigger's rights but not
// as its slot: a pause does not hold it, its schedule stays, and a run of it still going is returned.
func (a *App) triggerRun(ctx context.Context, act registry.Actor, in triggerRunIn) (any, error) {
	t, p, err := a.ownTrigger(ctx, act, in.Trigger)
	if err != nil {
		return nil, err
	}
	if t.Kind != trigger.KindCron && t.Kind != trigger.KindOnce {
		return nil, registry.Invalid("deck: trigger %d starts on a %s; only schedule triggers run now", t.ID, t.Kind)
	}
	params, err := manifest.DecodeObject([]byte(t.Params))
	if err != nil {
		return nil, err
	}
	if params == nil {
		params = map[string]any{}
	}
	params[manifest.RetryKey] = fmt.Sprintf("trigger:%d", t.ID)
	raw, _ := json.Marshal(params)
	as := registry.Actor{Kind: registry.Trigger, Workspace: p.WorkspaceID, ProjectID: p.ID, TriggerID: t.ID, Confirmed: true}
	out, err := a.Gateway.CallAs(ctx, as, p.Name+"."+t.Command, raw)
	if err != nil {
		return nil, err
	}
	if t.Kind == trigger.KindOnce {
		if err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.DeleteTrigger(ctx, t.ID) }); err != nil {
			return nil, err
		}
	}
	a.Bus.Publish("triggers")
	return out, nil
}

type triggerPauseIn struct {
	Trigger int64 `desc:"Trigger id"`
	Paused  bool  `desc:"Pause when true, resume when false"`
}

// triggerPause pauses one trigger; a cron trigger that goes on again skips the runs of its pause.
func (a *App) triggerPause(ctx context.Context, act registry.Actor, in triggerPauseIn) (any, error) {
	t, _, err := a.ownTrigger(ctx, act, in.Trigger)
	if err != nil {
		return nil, err
	}
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := q.SetTriggerPaused(ctx, db.SetTriggerPausedParams{Paused: flag(in.Paused), ID: t.ID}); err != nil {
			return err
		}
		if in.Paused {
			return nil
		}
		return a.Scheduler.Rewind(ctx, q, t)
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("triggers")
	return map[string]any{"ok": true}, nil
}

type projectPauseIn struct {
	Project string `type:"project" desc:"Project name"`
	Paused  bool   `desc:"Pause when true, resume when false"`
}

// projectPause pauses every trigger of a project, including those it gets later.
func (a *App) projectPause(ctx context.Context, act registry.Actor, in projectPauseIn) (any, error) {
	p, err := a.project(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := q.SetProjectPaused(ctx, db.SetProjectPausedParams{Paused: flag(in.Paused), ID: p.ID}); err != nil {
			return err
		}
		if in.Paused {
			return nil
		}
		ts, err := q.ListProjectTriggers(ctx, p.ID)
		if err != nil {
			return err
		}
		for _, t := range ts {
			if err := a.Scheduler.Rewind(ctx, q, t); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("triggers")
	a.Bus.Publish("projects")
	return map[string]any{"ok": true}, nil
}

func flag(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

func (a *App) triggersCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("run_later", "Run later",
			"Runs a project command once at the given time; a run missed while deck was down runs after it starts.",
			registry.Everyone, a.runLater),
		registry.Def("run_later_cancel", "Cancel run later",
			"Cancels a run scheduled with run later.",
			registry.Everyone, a.runLaterCancel),
		registry.Read("triggers_list", "Schedule",
			"Lists triggers with their next run, pause and the state of their last run.",
			registry.Everyone, a.triggersList).On("triggers"),
		registry.Def("trigger_pause", "Pause trigger",
			"Pauses or resumes one trigger; a resumed schedule does not catch up its pause.",
			registry.OwnerSide, a.triggerPause),
		registry.Def("trigger_run", "Run now",
			"Starts the command of a schedule trigger now with its params; its schedule stays, and a run still going is returned.",
			registry.OwnerSide, a.triggerRun),
		registry.Def("project_pause", "Pause project",
			"Pauses or resumes every trigger of a project, including triggers added later.",
			registry.OwnerSide, a.projectPause),
	}
}
