package app

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
	"deck2/internal/registry"
	"deck2/internal/screen"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/trigger"
)

type triggerView struct {
	ID        int64           `json:"id"`
	Project   string          `json:"project"`
	Command   string          `json:"command"`
	Kind      string          `json:"kind"`
	Spec      string          `json:"spec,omitempty"`
	Params    json.RawMessage `json:"params"`
	NextRunAt string          `json:"next_run_at,omitempty"`
	Paused    bool            `json:"paused"`
	// ProjectPaused says the pause is the project's; own is the trigger's own pause.
	ProjectPaused bool `json:"project_paused"`
	own           bool
	SkipMissed    bool   `json:"skip_missed"`
	LastProcess   int64  `json:"last_process,omitempty"`
	LastState     string `json:"last_state,omitempty"`
}

// ownTrigger loads a trigger of the actor's workspace; agents reach only their own project.
func (a *App) ownTrigger(ctx context.Context, act registry.Actor, id int64) (db.TriggerState, db.Project, error) {
	t, err := a.Store.Q.GetTrigger(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return t, db.Project{}, registry.NotFound("deck: no trigger %d", id)
	}
	if err != nil {
		return t, db.Project{}, err
	}
	p, err := projectOf(ctx, a.Store.Q, act, t.ProjectID, registry.NotFound("deck: no trigger %d", id))
	return t, p, err
}

func (a *App) runLater(ctx context.Context, act registry.Actor, in struct {
	Command, Params string
	At              string
}) (any, error) {
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
	cmd, _, err := a.Registry.Check(ctx, act, in.Command, json.RawMessage(in.Params))
	if err != nil {
		return nil, err
	}
	if cmd.ProjectID == 0 || !slices.Contains(cmd.Callers, registry.Trigger) {
		return nil, registry.Invalid("deck: %s cannot be run later: only project commands that triggers may call", in.Command)
	}
	_, name, _ := strings.Cut(in.Command, ".")
	raw, _ := json.Marshal(params)
	var t db.TriggerState
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

func (a *App) runLaterCancel(ctx context.Context, act registry.Actor, in struct{ Trigger int64 }) (any, error) {
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

func (a *App) triggersList(ctx context.Context, act registry.Actor, in struct{ Project string }) (any, error) {
	var pid int64
	if in.Project != "" || act.Kind == registry.Agent {
		p, err := a.scope(ctx, act, in.Project)
		if err != nil {
			return nil, err
		}
		pid = p.ID
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
		v := triggerView{ID: t.ID, Project: p.Name, Command: t.Command, Kind: t.Kind, Spec: t.Spec, Params: json.RawMessage(t.Params),
			NextRunAt: t.NextRunAt.String, Paused: t.Paused == 1 || p.Paused == 1, ProjectPaused: p.Paused == 1, own: t.Paused == 1,
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

// triggerRun starts the command of a schedule trigger now with its params, with the trigger's rights but not
// as its slot: a pause does not hold it, its schedule stays, and a run of it still going is returned.
func (a *App) triggerRun(ctx context.Context, act registry.Actor, in struct{ Trigger int64 }) (any, error) {
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
	as := registry.Actor{Kind: registry.Trigger, Workspace: p.WorkspaceID, ProjectID: p.ID}
	out, err := a.Gateway.CallAs(ctx, as, p.Name+"."+t.Command, raw)
	if err != nil {
		return nil, err
	}
	started, _ := out.(Started)
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if started.Process != 0 {
			if err := q.SetTriggerLast(ctx, db.SetTriggerLastParams{LastProcessID: sql.NullInt64{Int64: started.Process, Valid: true}, ID: t.ID}); err != nil {
				return err
			}
		}
		if t.Kind == trigger.KindOnce {
			return q.DeleteTrigger(ctx, t.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("triggers")
	return out, nil
}

// triggerItems are the triggers of every project with their buttons: run now, pause or go on, cancel.
func (s sources) triggerItems(ctx context.Context, sc screen.Scope) ([]map[string]any, error) {
	act := registry.Actor{Kind: registry.Owner, Workspace: sc.Workspace}
	got, err := s.a.triggersList(ctx, act, struct{ Project string }{})
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, v := range got.([]triggerView) {
		if sc.Project != 0 {
			if p, err := s.a.Store.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: sc.Workspace, Name: v.Project}); err != nil || p.ID != sc.Project {
				continue
			}
		}
		var params any
		_ = json.Unmarshal(v.Params, &params)
		it := map[string]any{"id": v.ID, "project": v.Project, "command": v.Command, "kind": v.Kind, "spec": v.Spec, "params": params,
			"next_run_at": v.NextRunAt, "paused": v.Paused, "project_paused": v.ProjectPaused, "skip_missed": v.SkipMissed,
			"last_process": v.LastProcess, "last_state": v.LastState}
		acts := []map[string]any{}
		if v.Kind == trigger.KindCron || v.Kind == trigger.KindOnce {
			acts = append(acts, call("run_now", false, "deck.trigger_run", map[string]any{"trigger": v.ID}))
		}
		if v.Kind == trigger.KindOnce {
			acts = append(acts, call("cancel_run", false, "deck.run_later_cancel", map[string]any{"trigger": v.ID}))
		} else if v.own {
			acts = append(acts, call("resume", false, "deck.trigger_pause", map[string]any{"trigger": v.ID, "paused": false}))
		} else {
			acts = append(acts, call("pause", false, "deck.trigger_pause", map[string]any{"trigger": v.ID, "paused": true}))
		}
		if v.LastProcess != 0 {
			acts = append(acts, map[string]any{"verb": "open", "screen": "process", "record": v.LastProcess})
		}
		it["_actions"] = acts
		out = append(out, it)
	}
	return out, nil
}

// triggerPause pauses one trigger; a cron trigger that goes on again skips the runs of its pause.
func (a *App) triggerPause(ctx context.Context, act registry.Actor, in struct {
	Trigger int64
	Paused  bool
}) (any, error) {
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

// projectPause pauses every trigger of a project, including those it gets later.
func (a *App) projectPause(ctx context.Context, act registry.Actor, in struct {
	Project string
	Paused  bool
}) (any, error) {
	p, err := a.scope(ctx, act, in.Project)
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
