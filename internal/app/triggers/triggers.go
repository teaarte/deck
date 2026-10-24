// Package triggers keeps the schedule of projects as commands: the triggers of manifests and their pauses.
package triggers

import (
	"cmp"
	"context"
	"deck2/internal/app/access"
	"encoding/json"
	"fmt"
	"time"

	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/trigger"
)

// Service keeps the triggers; the scheduler fires them, the gateway runs a trigger now.
type Service struct {
	St        *store.Store
	Now       func() time.Time
	Scheduler *trigger.Scheduler
	Gateway   *gateway.Gateway
	// Works looks up the model that does the work of a command, on the models of a workspace, prices in a language.
	Works func(ctx context.Context, workspace int64, lang string) func(projectID int64, command string) provider.Work
	// Words are deck's phrases in a language.
	Words func(lang string) map[string]string
	// OpenFailures are the failed processes of the workspace not fixed by a later run, retried or dismissed.
	OpenFailures func(ctx context.Context, act registry.Actor) ([]db.Process, error)
}

// ownTrigger loads a trigger of the actor's workspace; agents reach only their own project.
func (s *Service) ownTrigger(ctx context.Context, act registry.Actor, id int64) (db.Trigger, db.Project, error) {
	t, err := s.St.Q.GetTrigger(ctx, id)
	return access.Owned(ctx, s.St.Q, act, t, err, t.ProjectID, registry.NotFound("deck: no trigger %d", id))
}

type triggersListIn struct {
	Project string `type:"project" opt:"" desc:"Project name; without it an agent of a project gets its own, the owner and the master every project"`
	Lang    string `opt:"" desc:"Language of the words of the states, such as ru; English without one"`
}

// triggerView is a trigger with its next run, whose pause holds it, and how its last run ended.
type triggerView struct {
	ID        int64           `json:"id"`
	Project   string          `json:"project"`
	Command   string          `json:"command"`
	Kind      string          `json:"kind"`
	Spec      string          `json:"spec"`
	// Every is how often it runs in words, for a trigger that is no schedule.
	Every string `json:"every,omitempty"`
	Params    json.RawMessage `json:"params"`
	NextRunAt string          `json:"next_run_at"`
	// Paused is the trigger's own pause or its project's; ProjectPaused and OwnPaused say which.
	Paused        bool `json:"paused"`
	ProjectPaused bool `json:"project_paused"`
	OwnPaused     bool `json:"own_paused"`
	SkipMissed    bool `json:"skip_missed"`
	// Draft is a trigger of a draft, which no schedule or event starts; it has no next run then.
	Draft       bool   `json:"draft"`
	LastProcess int64  `json:"last_process"`
	LastState   string `json:"last_state"`
	// Word and Tone say whether it runs; LastWord and LastTone how its last run ended.
	Word     string `json:"word"`
	Tone     string `json:"tone,omitempty"`
	LastWord string `json:"last_word,omitempty"`
	LastTone string `json:"last_tone,omitempty"`
	// Work is the model that does the work of an agent or a model command, with the models that could.
	provider.Work
}

func (s *Service) triggersList(ctx context.Context, act registry.Actor, in triggersListIn) ([]triggerView, error) {
	pid, err := access.Filter(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	ts, err := s.St.Q.ListWorkspaceTriggers(ctx, db.ListWorkspaceTriggersParams{WorkspaceID: act.Workspace, ProjectID: pid})
	if err != nil {
		return nil, err
	}
	names := map[int64]db.Project{}
	works := map[string]provider.Work{}
	work := s.Works(ctx, act.Workspace, in.Lang)
	ws := s.Words(in.Lang)
	tr := func(phrase string) string { return manifest.Tr(ws, phrase) }
	var failing map[int64]bool
	open := func(id int64) bool {
		if failing == nil {
			failing = map[int64]bool{}
			ps, err := s.OpenFailures(ctx, act)
			if err != nil {
				return true
			}
			for _, p := range ps {
				failing[p.ID] = true
			}
		}
		return failing[id]
	}
	out := make([]triggerView, 0, len(ts))
	for _, t := range ts {
		p, ok := names[t.ProjectID]
		if !ok {
			if p, err = s.St.Q.GetProject(ctx, t.ProjectID); err != nil {
				return nil, err
			}
			names[t.ProjectID] = p
		}
		v := triggerView{ID: t.ID, Project: p.Name, Command: t.Command, Kind: t.Kind, Spec: t.Spec, Params: json.RawMessage(cmp.Or(t.Params, "null")),
			NextRunAt: t.NextRunAt.String, Paused: t.Paused == 1 || p.Paused == 1, ProjectPaused: p.Paused == 1, OwnPaused: t.Paused == 1,
			SkipMissed: t.SkipMissed == 1, Draft: p.Status != store.ProjectEnabled}
		if v.Draft {
			v.NextRunAt = ""
		}
		if t.Kind == trigger.KindAfter {
			v.Every = fmt.Sprintf(tr("after %s"), t.Spec)
		}
		key := fmt.Sprint(t.ProjectID, ".", t.Command)
		w, ok := works[key]
		if !ok {
			w = work(t.ProjectID, t.Command)
			works[key] = w
		}
		v.Work = w
		if t.LastProcessID.Valid {
			v.LastProcess = t.LastProcessID.Int64
			if lp, err := s.St.Q.GetProcess(ctx, v.LastProcess); err == nil {
				v.LastState = lp.State
				shown := process.Show(lp, "", false, tr)
				v.LastWord, v.LastTone = shown.Word, shown.Tone
				if v.LastTone == "error" && !open(lp.ID) {
					v.LastWord, v.LastTone = tr("failed, fixed since"), "muted"
				}
			}
		}
		switch {
		case v.Draft:
			v.Word, v.Tone = tr("draft: does not run"), "muted"
		case v.ProjectPaused:
			v.Word, v.Tone = tr("module paused"), "muted"
		case v.Paused:
			v.Word, v.Tone = tr("paused"), "muted"
		default:
			v.Word = tr("on")
		}
		out = append(out, v)
	}
	return out, nil
}

type triggerRunIn struct {
	Trigger int64 `desc:"Trigger id"`
}

// triggerRun starts the command of a schedule trigger now with its params, with the trigger's rights but not
// as its slot: its schedule stays, a run of it still going is returned, and only the owner's run passes a pause.
func (s *Service) triggerRun(ctx context.Context, act registry.Actor, in triggerRunIn) (any, error) {
	t, p, err := s.ownTrigger(ctx, act, in.Trigger)
	if err != nil {
		return nil, err
	}
	if t.Kind != trigger.KindCron {
		return nil, registry.Invalid("deck: trigger %d starts on a %s; only schedule triggers run now", t.ID, t.Kind)
	}
	name := p.Name + "." + t.Command
	if (act.Kind == registry.Token || act.Commands != nil) && !registry.Permits(act.Commands, name) {
		return nil, registry.Refuse("deck: this caller may not call %s, so it does not run it now", name)
	}
	if act.Kind != registry.Owner && (t.Paused == 1 || p.Paused == 1) {
		return nil, registry.Invalid("deck: %s was not started: its trigger or project is paused; only the owner runs it now", name)
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
	return s.Gateway.CallAs(ctx, as, name, raw)
}

type triggerPauseIn struct {
	Trigger int64 `desc:"Trigger id"`
	Paused  bool  `desc:"Pause when true, resume when false"`
}

// triggerPause pauses one trigger; a cron trigger that goes on again skips the runs of its pause.
func (s *Service) triggerPause(ctx context.Context, act registry.Actor, in triggerPauseIn) (any, error) {
	t, _, err := s.ownTrigger(ctx, act, in.Trigger)
	if err != nil {
		return nil, err
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := q.SetTriggerPaused(ctx, db.SetTriggerPausedParams{Paused: flag(in.Paused), ID: t.ID}); err != nil {
			return err
		}
		if in.Paused {
			return nil
		}
		return s.Scheduler.Rewind(ctx, q, t)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

type projectPauseIn struct {
	Project string `type:"project" desc:"Project name"`
	Paused  bool   `desc:"Pause when true, resume when false"`
}

// projectPause pauses every trigger of a project, including those it gets later.
func (s *Service) projectPause(ctx context.Context, act registry.Actor, in projectPauseIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
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
			if err := s.Scheduler.Rewind(ctx, q, t); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

func flag(b bool) int64 {
	if b {
		return 1
	}
	return 0
}

// Commands are the commands of triggers.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Read("triggers_list", "Schedule",
			"Lists triggers with their next run, pause, the state of their last run and the model an agent command starts on.",
			registry.Everyone, s.triggersList).On("triggers", "processes", "models", "assignments", "projects", "documents", "health"),
		registry.Def("trigger_pause", "Pause trigger",
			"Pauses or resumes one trigger; a resumed schedule does not catch up its pause.",
			registry.OwnerSide, s.triggerPause),
		registry.Def("trigger_run", "Run now",
			"Starts the command of a schedule trigger now with its params; its schedule stays, a run still going is returned, and only the owner starts one of a paused trigger or project.",
			registry.OwnerSide, s.triggerRun),
		registry.Def("project_pause", "Pause project",
			"Pauses or resumes every trigger of a project, including triggers added later.",
			registry.OwnerSide, s.projectPause),
	}
}
