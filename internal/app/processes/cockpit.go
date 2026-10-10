package processes

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"deck2/internal/app/records"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// ofRecord names the record the input field record of a process gives, with its title.
func (s *Service) ofRecord(ctx context.Context, p db.Process, v *processView) {
	var in struct {
		Record int64 `json:"record"`
	}
	if json.Unmarshal([]byte(p.Input), &in) != nil || in.Record == 0 {
		return
	}
	v.Record = in.Record
	rec, err := s.St.Q.GetRecord(ctx, in.Record)
	if err != nil || rec.ProjectID != p.ProjectID {
		return
	}
	var c *manifest.Collection
	if proj, err := s.St.Q.GetProject(ctx, p.ProjectID); err == nil {
		if m, err := docs.Manifest(ctx, s.St.Q, proj); err == nil {
			c = m.Collection(rec.Collection)
		}
	}
	v.RecordTitle = records.ViewOf(rec, c).Name()
}

// usualRuns is how many finished runs of a command it takes to know how long it usually works.
const usualRuns = 5

// shown says the state of a process in the words of lang; an open one with what it waits for, since when it works
// and the topic its reply answers.
func (s *Service) shown(ctx context.Context, p db.Process, v *processView, lang string) {
	var question string
	if !p.FinishedAt.Valid {
		v.Since = cmp.Or(p.ReopenedAt.String, p.StartedAt)
		v.Long = s.long(ctx, p, v.Since)
		asks, _ := s.St.Q.ListOpenAsksOfProcess(ctx, p.ID)
		if i := slices.IndexFunc(asks, holds); i >= 0 {
			v.Ask, v.Question = asks[i].ID, asks[i].Text
			question = untrusted.Cut(asks[i].Text, 200)
		}
		if t, err := s.St.Q.TopicOfProcess(ctx, p.ID); err == nil {
			v.Topic = t.Title
		}
	}
	ws := s.Words(lang)
	v.Shown = process.Show(p, question, v.Long, func(phrase string) string { return manifest.Tr(ws, phrase) })
}

// long reports whether a running process works, since it was last opened, more than twice as long as its
// command's usual run, the median of its last runs, and over a minute.
func (s *Service) long(ctx context.Context, p db.Process, since string) bool {
	started, err := time.Parse(time.RFC3339Nano, since)
	if err != nil || p.State != process.StateRunning {
		return false
	}
	took := s.Now().Sub(started)
	ms, err := s.St.Q.RecentDurations(ctx, db.RecentDurationsParams{ProjectID: p.ProjectID, Command: p.Command})
	if err != nil || len(ms) < usualRuns {
		return false
	}
	slices.Sort(ms)
	usual := time.Duration(ms[len(ms)/2]) * time.Millisecond
	return took > time.Minute && took > 2*usual
}

type processRetryIn struct {
	Process int64 `type:"process" desc:"Process id"`
}

// processRetry starts the command of a failed process again with its input.
func (s *Service) processRetry(ctx context.Context, act registry.Actor, in processRetryIn) (any, error) {
	p, err := s.failed(ctx, act, in.Process)
	if err != nil {
		return nil, err
	}
	proj, err := s.St.Q.GetProject(ctx, p.ProjectID)
	if err != nil {
		return nil, err
	}
	input := json.RawMessage(p.Input)
	if !json.Valid(input) {
		input = json.RawMessage("{}")
	}
	return s.Invoke(ctx, act, fmt.Sprintf("%s.%s", proj.Name, p.Command), input)
}

// failed is a process of the actor's workspace among those that did not work, as the cockpit lists them.
func (s *Service) failed(ctx context.Context, act registry.Actor, id int64) (db.Process, error) {
	p, err := s.ownProcess(ctx, act, id)
	if err != nil {
		return p, err
	}
	ps, err := s.St.Q.ListFailedProcesses(ctx, s.failedParams(act, p.ProjectID, p.ID, 1))
	if err != nil {
		return p, err
	}
	if len(ps) == 0 {
		return p, registry.Invalid("deck: process %d is not among those that did not work", id)
	}
	return p, nil
}

// failedParams read the processes that did not work in the last week; a project or id of zero is any.
func (s *Service) failedParams(act registry.Actor, project, id, lim int64) db.ListFailedProcessesParams {
	return db.ListFailedProcessesParams{WorkspaceID: act.Workspace, ProjectID: project, ID: id, Since: store.Time(s.Now().Add(-7 * 24 * time.Hour)), Lim: lim}
}

type processDismissIn struct {
	Process int64 `type:"process" desc:"Process id"`
}

// processDismiss takes a failed process off the cockpit.
func (s *Service) processDismiss(ctx context.Context, act registry.Actor, in processDismissIn) (any, error) {
	if _, err := s.failed(ctx, act, in.Process); err != nil {
		return nil, err
	}
	if err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.DismissProcess(ctx, in.Process) }); err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
