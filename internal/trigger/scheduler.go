package trigger

import (
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"time"

	"deck2/internal/manifest"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const (
	KindCron  = "cron"
	KindLane  = "lane"
	KindAfter = "after"
	KindOnce  = "once"
)

// Starter starts the command of a trigger row with the given input.
type Starter interface {
	Fire(ctx context.Context, t db.TriggerState, input map[string]any) (int64, error)
}

// Scheduler runs cron and run-later triggers in the local time zone. A run later than Grace was missed
// (deck down or the machine asleep): it fires once, and several missed runs are spaced Spread apart.
type Scheduler struct {
	st     *store.Store
	start  Starter
	Now    func() time.Time
	Spread time.Duration
	Grace  time.Duration
}

func New(st *store.Store, s Starter, now func() time.Time, spread time.Duration) *Scheduler {
	return &Scheduler{st: st, start: s, Now: now, Spread: spread, Grace: 2 * time.Minute}
}

// Identity is what a manifest trigger is known by across manifest revisions.
func Identity(t manifest.Trigger) (kind, spec string) {
	switch {
	case t.Cron != "":
		return KindCron, t.Cron
	case t.Lane != "":
		return KindLane, t.Lane
	default:
		return KindAfter, t.After
	}
}

// Find is the stored row of a manifest trigger.
func Find(rows []db.TriggerState, t manifest.Trigger) (db.TriggerState, bool) {
	kind, spec := Identity(t)
	for _, r := range rows {
		if r.Command == t.Command && r.Kind == kind && r.Spec == spec {
			return r, true
		}
	}
	return db.TriggerState{}, false
}

// Sync stores the triggers of a manifest, keeping the next run, pause and id of unchanged ones and dropping
// those the manifest no longer has; run-later rows stay.
func (s *Scheduler) Sync(ctx context.Context, q *db.Queries, projectID int64, m *manifest.Manifest) error {
	old, err := q.ListProjectTriggers(ctx, projectID)
	if err != nil {
		return err
	}
	for _, r := range old {
		if r.Kind == KindOnce {
			continue
		}
		gone := true
		for _, t := range m.Triggers {
			if k, sp := Identity(t); k == r.Kind && sp == r.Spec && t.Command == r.Command {
				gone = false
			}
		}
		if gone {
			if err := q.DeleteTrigger(ctx, r.ID); err != nil {
				return err
			}
		}
	}
	for _, t := range m.Triggers {
		kind, spec := Identity(t)
		var next sql.NullString
		if kind == KindCron {
			n, err := s.next(spec, s.Now())
			if err != nil {
				return err
			}
			next = sql.NullString{String: n, Valid: true}
		}
		params, _ := json.Marshal(orEmpty(t.Params))
		skip := int64(0)
		if t.SkipMissed {
			skip = 1
		}
		err := q.UpsertTrigger(ctx, db.UpsertTriggerParams{ProjectID: projectID, Command: t.Command, Kind: kind, Spec: spec,
			Params: string(params), NextRunAt: next, SkipMissed: skip})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Scheduler) next(cron string, after time.Time) (string, error) {
	sched, err := manifest.CronParser.Parse(cron)
	if err != nil {
		return "", err
	}
	return store.Time(sched.Next(after.In(time.Local))), nil
}

// Rewind moves a cron trigger whose next run has passed to its next slot after now, so a paused period is
// not caught up.
func (s *Scheduler) Rewind(ctx context.Context, q *db.Queries, t db.TriggerState) error {
	if t.Kind != KindCron || t.NextRunAt.String > store.Time(s.Now()) {
		return nil
	}
	next, err := s.next(t.Spec, s.Now())
	if err != nil {
		return err
	}
	return q.SetTriggerNext(ctx, db.SetTriggerNextParams{NextRunAt: sql.NullString{String: next, Valid: true}, ID: t.ID})
}

// Tick starts every due trigger once. Missed runs start one now and the others at later slots written to
// the base; a missed cron trigger that skips missed runs only moves to its next slot.
func (s *Scheduler) Tick(ctx context.Context) {
	now := s.Now()
	due, err := s.st.Q.DueTriggers(ctx, store.Time(now))
	if err != nil {
		slog.Error("due triggers", "err", err)
		return
	}
	missedBefore := store.Time(now.Add(-s.Grace))
	late := 0
	for _, t := range due {
		missed := t.NextRunAt.String < missedBefore
		switch {
		case missed && t.Kind == KindCron && t.SkipMissed == 1:
			s.after(ctx, t, now)
		case missed && late > 0:
			slot := store.Time(now.Add(time.Duration(late) * s.Spread))
			late++
			s.tx(ctx, func(ctx context.Context, q *db.Queries) error {
				return q.SetTriggerNext(ctx, db.SetTriggerNextParams{NextRunAt: sql.NullString{String: slot, Valid: true}, ID: t.ID})
			})
		default:
			if missed {
				late++
			}
			s.fire(ctx, t, now)
		}
	}
}

func (s *Scheduler) fire(ctx context.Context, t db.TriggerState, now time.Time) {
	params, err := manifest.DecodeObject([]byte(t.Params))
	if err != nil {
		slog.Error("trigger params", "trigger", t.ID, "err", err)
	} else if _, err := s.start.Fire(ctx, t, params); err != nil {
		slog.Error("trigger start", "trigger", t.ID, "command", t.Command, "err", err)
	}
	s.after(ctx, t, now)
}

// after moves a fired or skipped trigger on: a cron trigger to its next slot, a run-later row away.
func (s *Scheduler) after(ctx context.Context, t db.TriggerState, now time.Time) {
	s.tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if t.Kind == KindOnce {
			return q.DeleteTrigger(ctx, t.ID)
		}
		next, err := s.next(t.Spec, now)
		if err != nil {
			return err
		}
		return q.SetTriggerNext(ctx, db.SetTriggerNextParams{NextRunAt: sql.NullString{String: next, Valid: true}, ID: t.ID})
	})
}

func (s *Scheduler) tx(ctx context.Context, f func(ctx context.Context, q *db.Queries) error) {
	if err := s.st.Tx(ctx, f); err != nil {
		slog.Error("trigger update", "err", err)
	}
}

func orEmpty(p map[string]any) map[string]any {
	if p == nil {
		return map[string]any{}
	}
	return p
}
