package trigger

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"deck2/internal/manifest"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const timeLayout = "2006-01-02T15:04:05.000Z"

type Starter interface {
	Start(ctx context.Context, projectID int64, command string, input map[string]any) (int64, error)
}

// Scheduler runs cron triggers in the local time zone. A trigger missed while deck was down or the
// machine slept fires once, then its next run is computed from the current time.
type Scheduler struct {
	st    *store.Store
	start Starter
	Now   func() time.Time
}

func New(st *store.Store, s Starter) *Scheduler {
	return &Scheduler{st: st, start: s, Now: time.Now}
}

// Sync registers the triggers of a manifest, keeping next_run_at for unchanged cron expressions and
// dropping triggers the manifest no longer has.
func (s *Scheduler) Sync(ctx context.Context, q *db.Queries, projectID int64, m *manifest.Manifest) error {
	keep := map[[2]string]bool{}
	for _, t := range m.Triggers {
		if t.Cron != "" {
			keep[[2]string{t.Command, t.Cron}] = true
		}
	}
	old, err := q.ListProjectTriggers(ctx, projectID)
	if err != nil {
		return err
	}
	for _, t := range old {
		if !keep[[2]string{t.Command, t.Cron}] {
			if err := q.DeleteTrigger(ctx, db.DeleteTriggerParams{ProjectID: projectID, Command: t.Command, Cron: t.Cron}); err != nil {
				return err
			}
		}
	}
	for _, t := range m.Triggers {
		if t.Cron == "" {
			continue
		}
		sched, err := manifest.CronParser.Parse(t.Cron)
		if err != nil {
			return err
		}
		next := sched.Next(s.Now().In(time.Local)).UTC().Format(timeLayout)
		params, _ := json.Marshal(orEmpty(t.Params))
		if err := q.UpsertTrigger(ctx, db.UpsertTriggerParams{ProjectID: projectID, Command: t.Command, Cron: t.Cron, NextRunAt: next, Params: string(params)}); err != nil {
			return err
		}
	}
	return nil
}

// Tick starts every due trigger once and moves it to its next slot after now.
func (s *Scheduler) Tick(ctx context.Context) {
	now := s.Now()
	due, err := s.st.Q.DueTriggers(ctx, now.UTC().Format(timeLayout))
	if err != nil {
		slog.Error("due triggers", "err", err)
		return
	}
	for _, t := range due {
		sched, err := manifest.CronParser.Parse(t.Cron)
		if err != nil {
			slog.Error("trigger cron", "command", t.Command, "err", err)
			continue
		}
		params, err := manifest.DecodeObject([]byte(t.Params))
		if err != nil {
			slog.Error("trigger params", "command", t.Command, "err", err)
			continue
		}
		if _, err := s.start.Start(ctx, t.ProjectID, t.Command, params); err != nil {
			slog.Error("trigger start", "command", t.Command, "err", err)
		}
		next := sched.Next(now.In(time.Local)).UTC().Format(timeLayout)
		err = s.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return q.SetTriggerNext(ctx, db.SetTriggerNextParams{NextRunAt: next, ProjectID: t.ProjectID, Command: t.Command, Cron: t.Cron})
		})
		if err != nil {
			slog.Error("trigger next", "err", err)
		}
	}
}

func orEmpty(p map[string]any) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	return p
}
