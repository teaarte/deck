package processes

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"deck2/internal/app/access"
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
	before := store.Time(s.Now())
	out, err := s.Invoke(ctx, act, fmt.Sprintf("%s.%s", proj.Name, p.Command), input)
	if err != nil {
		return out, err
	}
	var started struct{ Process int64 }
	// A run already going under the same retry key is not one this retry started.
	if b, err := json.Marshal(out); err == nil && json.Unmarshal(b, &started) == nil && started.Process != 0 && started.Process != p.ID &&
		s.startedSince(ctx, started.Process, before) {
		err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.SetFollows(ctx, db.SetFollowsParams{Follows: store.ID(p.ID), ID: started.Process})
		})
		return out, err
	}
	return out, nil
}

// failed is a process of the actor's workspace among those that did not work, as the cockpit lists them.
func (s *Service) failed(ctx context.Context, act registry.Actor, id int64) (db.Process, error) {
	p, err := s.ownProcess(ctx, act, id)
	if err != nil {
		return p, err
	}
	return p, s.stillFailed(ctx, s.St.Q, act, p)
}

// stillFailed refuses a process no longer among those that did not work.
func (s *Service) stillFailed(ctx context.Context, q *db.Queries, act registry.Actor, p db.Process) error {
	ps, err := q.ListFailedProcesses(ctx, s.failedParams(act, p.ProjectID, p.ID, 1))
	if err == nil && len(ps) == 0 {
		err = registry.Invalid("deck: process %d is not among those that did not work", p.ID)
	}
	return err
}

// failedParams read the processes that did not work in the last week; a project or id of zero is any.
func (s *Service) failedParams(act registry.Actor, project, id, lim int64) db.ListFailedProcessesParams {
	return db.ListFailedProcessesParams{WorkspaceID: act.Workspace, ProjectID: project, ID: id, Since: store.Time(s.Now().Add(-7 * 24 * time.Hour)), Lim: lim}
}

type processDismissIn struct {
	Process int64 `type:"process" opt:"" desc:"Process id"`
	All     bool  `opt:"" desc:"Every failed process the cockpit shows instead of one; each stays in the history"`
}

// processDismiss takes a failed process off the cockpit, or every one it shows.
func (s *Service) processDismiss(ctx context.Context, act registry.Actor, in processDismissIn) (any, error) {
	if in.All == (in.Process != 0) {
		return nil, registry.Invalid("deck: give one process, or all")
	}
	if in.All {
		return s.dismissAll(ctx, act)
	}
	p, err := s.ownProcess(ctx, act, in.Process)
	if err != nil {
		return nil, err
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := s.stillFailed(ctx, q, act, p); err != nil {
			return err
		}
		return q.DismissSameFailures(ctx, db.DismissSameFailuresParams{ProjectID: p.ProjectID, Command: p.Command,
			ErrorType: p.ErrorType, Error: p.Error, ID: p.ID})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

// dismissAll takes every failed process the actor sees off the cockpit.
func (s *Service) dismissAll(ctx context.Context, act registry.Actor) (any, error) {
	pid, err := access.Filter(ctx, s.St.Q, act, "")
	if err != nil {
		return nil, err
	}
	n, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (int, error) {
		ps, err := q.ListFailedProcesses(ctx, s.failedParams(act, pid, 0, 2000))
		for _, p := range ps {
			if err != nil {
				break
			}
			err = q.DismissSameFailures(ctx, db.DismissSameFailuresParams{ProjectID: p.ProjectID, Command: p.Command,
				ErrorType: p.ErrorType, Error: p.Error, ID: p.ID})
		}
		return len(ps), err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true, "closed": n}, nil
}

// Failed is how many lines of failed processes the cockpit shows, the same failures one line.
func (s *Service) Failed(ctx context.Context, act registry.Actor) (int64, error) {
	ps, err := s.St.Q.ListFailedProcesses(ctx, s.failedParams(act, 0, 0, 2000))
	ps, _ = sameFailures(ps, len(ps))
	return int64(len(ps)), err
}

// startedSince reports whether a process started at or after a time.
func (s *Service) startedSince(ctx context.Context, id int64, at string) bool {
	p, err := s.St.Q.GetProcess(ctx, id)
	return err == nil && p.StartedAt >= at
}
