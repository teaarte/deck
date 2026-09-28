package screens

import (
	"context"
	"encoding/json"

	"deck2/internal/screen"
	"deck2/internal/store/db"
	"deck2/internal/trigger"
)

type Trigger struct {
	ID        int64           `json:"id"`
	Project   string          `json:"project"`
	Command   string          `json:"command"`
	Kind      string          `json:"kind"`
	Spec      string          `json:"spec,omitempty"`
	Params    json.RawMessage `json:"params"`
	NextRunAt string          `json:"next_run_at,omitempty"`
	Paused    bool            `json:"paused"`
	// ProjectPaused says the pause is the project's; Own is the trigger's own pause.
	ProjectPaused bool   `json:"project_paused"`
	Own           bool   `json:"-"`
	SkipMissed    bool   `json:"skip_missed"`
	LastProcess   int64  `json:"last_process,omitempty"`
	LastState     string `json:"last_state,omitempty"`
}

// triggerItems are the triggers of every project with their buttons: run now, pause or go on, cancel.
func (s Sources) triggerItems(ctx context.Context, sc screen.Scope) ([]map[string]any, error) {
	got, err := s.Deck.Triggers(ctx, sc.Workspace)
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for _, v := range got {
		if sc.Project != 0 {
			if p, err := s.Store.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: sc.Workspace, Name: v.Project}); err != nil || p.ID != sc.Project {
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
		} else if v.Own {
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
