package processes

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"deck2/internal/docs"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
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
	v.RecordTitle = rec.Key
	proj, err := s.St.Q.GetProject(ctx, p.ProjectID)
	if err != nil {
		return
	}
	if m, err := docs.Manifest(ctx, s.St.Q, proj); err == nil {
		if c := m.Collection(rec.Collection); c != nil {
			var data map[string]any
			_ = json.Unmarshal([]byte(rec.Data), &data)
			if t := c.RecordTitle(data); t != "" {
				v.RecordTitle = t
			}
		}
	}
}

// usualRuns is how many finished runs of a command it takes to know how long it usually works.
const usualRuns = 5

// howLong is how long an open process works, and whether that is more than twice as long as its command's usual
// run, the median of its last runs, and over a minute.
func (s *Service) howLong(ctx context.Context, p db.Process, v *processView) {
	started, err := time.Parse(time.RFC3339Nano, p.StartedAt)
	if err != nil {
		return
	}
	took := s.Now().Sub(started)
	v.Seconds = int64(took / time.Second)
	ms, err := s.St.Q.RecentDurations(ctx, db.RecentDurationsParams{ProjectID: p.ProjectID, Command: p.Command})
	if err != nil || len(ms) < usualRuns {
		return
	}
	slices.Sort(ms)
	usual := time.Duration(ms[len(ms)/2]) * time.Millisecond
	v.Long = took > time.Minute && took > 2*usual
}

type processRetryIn struct {
	Process int64 `type:"process" desc:"Process id"`
}

// processRetry starts the command of a failed process again with its input.
func (s *Service) processRetry(ctx context.Context, act registry.Actor, in processRetryIn) (any, error) {
	p, err := s.ownProcess(ctx, act, in.Process)
	if err != nil {
		return nil, err
	}
	if p.State != process.StateError && !(p.State == process.StateDone && p.NotBySchema == 1) {
		return nil, registry.Invalid("deck: process %d did not fail", p.ID)
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

type processDismissIn struct {
	Process int64 `type:"process" desc:"Process id"`
}

// processDismiss takes a failed process off the cockpit.
func (s *Service) processDismiss(ctx context.Context, act registry.Actor, in processDismissIn) (any, error) {
	if _, err := s.ownProcess(ctx, act, in.Process); err != nil {
		return nil, err
	}
	n, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) { return q.DismissProcess(ctx, in.Process) })
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.Invalid("deck: process %d did not fail", in.Process)
	}
	return map[string]any{"ok": true}, nil
}
