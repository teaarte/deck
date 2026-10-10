package trigger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"deck2/internal/health"

	"deck2/internal/manifest"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const (
	KindCron  = "cron"
	KindAfter = "after"
)

// Fire starts the command of a trigger row with the given input.
type Fire func(ctx context.Context, t db.Trigger, input map[string]any) (int64, error)

// spread spaces missed runs apart; grace is how late a run may be before it counts as missed.
const (
	spread = time.Minute
	grace  = 2 * time.Minute
)

// Scheduler runs cron triggers in the local time zone. A run later than grace was missed
// (deck down or the machine asleep): it fires once, and several missed runs are spaced spread apart.
type Scheduler struct {
	st    *store.Store
	start Fire
	Now   func() time.Time
}

func New(st *store.Store, fire Fire, now func() time.Time) *Scheduler {
	return &Scheduler{st: st, start: fire, Now: now}
}

// Identity is what a manifest trigger is known by across manifest revisions.
func Identity(t manifest.Trigger) (kind, spec string) {
	switch {
	case t.On != "":
		return t.On, ""
	case t.Cron != "":
		return KindCron, t.Cron
	default:
		return KindAfter, t.After
	}
}

// Find is the stored row of a manifest trigger.
func Find(rows []db.Trigger, t manifest.Trigger) (db.Trigger, bool) {
	kind, spec := Identity(t)
	for _, r := range rows {
		if r.Command == t.Command && r.Kind == kind && r.Spec == spec {
			return r, true
		}
	}
	return db.Trigger{}, false
}

// Sync stores the triggers of a manifest, keeping the next run, pause and id of unchanged ones and dropping
// those the manifest no longer has.
func (s *Scheduler) Sync(ctx context.Context, q *db.Queries, projectID int64, m *manifest.Manifest) error {
	old, err := q.ListProjectTriggers(ctx, projectID)
	if err != nil {
		return err
	}
	for _, r := range old {
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
		if t.On != "" {
			continue
		}
		kind, spec := Identity(t)
		var next sql.NullString
		if kind == KindCron {
			n, err := Next(spec, s.Now())
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

// Next is the slot of a cron spec after a time by the owner's wall clock: one in an hour the clocks skip runs at the
// first minute after it, and an hour they repeat holds each slot once.
func Next(cron string, after time.Time) (string, error) {
	sched, err := manifest.CronParser.Parse(cron)
	if err != nil {
		return "", err
	}
	a := after.In(time.Local)
	w := time.Date(a.Year(), a.Month(), a.Day(), a.Hour(), a.Minute(), a.Second(), a.Nanosecond(), time.UTC)
	for {
		w = sched.Next(w)
		t := time.Date(w.Year(), w.Month(), w.Day(), w.Hour(), w.Minute(), w.Second(), 0, time.Local)
		if t.Hour() != w.Hour() || t.Minute() != w.Minute() {
			_, off := t.Zone()
			t = t.Truncate(time.Minute)
			for _, o := t.Add(-time.Minute).Zone(); o == off; _, o = t.Add(-time.Minute).Zone() {
				t = t.Add(-time.Minute)
			}
		}
		// The second pass of a repeated hour has had its slot already.
		if t.After(after) {
			return store.Time(t), nil
		}
	}
}

// Rewind moves a cron trigger whose next run has passed to its next slot after now, so a paused period is
// not caught up.
func (s *Scheduler) Rewind(ctx context.Context, q *db.Queries, t db.Trigger) error {
	if t.Kind != KindCron || t.NextRunAt.String > store.Time(s.Now()) {
		return nil
	}
	next, err := Next(t.Spec, s.Now())
	if err != nil {
		return err
	}
	return q.SetTriggerNext(ctx, db.SetTriggerNextParams{NextRunAt: sql.NullString{String: next, Valid: true}, ID: t.ID})
}

// Tick starts every due trigger once and returns how many it moved. Missed runs start one now and the others
// at later slots written to the base; a missed cron trigger that skips missed runs only moves to its next slot.
func (s *Scheduler) Tick(ctx context.Context) int {
	now := s.Now()
	due, err := s.st.Q.DueTriggers(ctx, store.Time(now))
	if err != nil {
		slog.Error("due triggers", "err", err)
		return 0
	}
	missedBefore := store.Time(now.Add(-grace))
	late := 0
	for _, t := range due {
		missed := t.NextRunAt.String < missedBefore
		switch {
		case missed && t.Kind == KindCron && t.SkipMissed == 1:
			s.after(ctx, t, now)
		case missed && late > 0:
			slot := store.Time(now.Add(time.Duration(late) * spread))
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
	return len(due)
}

// fire starts the command of a trigger and moves it on; why it did not start, or the run still going that took its
// slot, is in the health of its project until a run of it starts.
func (s *Scheduler) fire(ctx context.Context, t db.Trigger, now time.Time) {
	params, err := manifest.DecodeObject([]byte(t.Params))
	var id int64
	if err == nil {
		id, err = s.start(ctx, t, params)
	}
	if ctx.Err() != nil {
		return
	}
	p, perr := s.st.Q.GetProject(ctx, t.ProjectID)
	subject := "trigger:" + p.Name + "." + t.Command
	if err == nil && t.LastProcessID.Valid && id == t.LastProcessID.Int64 {
		s.after(ctx, t, now)
		if perr == nil {
			health.Write(ctx, s.st, health.Project(p), subject, health.Warning,
				fmt.Sprintf("the %s trigger did not start %s at %s: its run #%d still goes", t.Kind, t.Command, t.NextRunAt.String, id))
		}
		return
	}
	if err == nil {
		if perr == nil {
			s.tx(ctx, func(ctx context.Context, q *db.Queries) error {
				return q.DeleteHealth(ctx, db.DeleteHealthParams{ProjectID: store.ID(p.ID), Subject: subject})
			})
		}
		// The run moved its slot with it; a call held for the owner started none.
		if id == 0 {
			s.after(ctx, t, now)
		}
		return
	}
	slog.Error("trigger start", "trigger", t.ID, "command", t.Command, "err", err)
	text := fmt.Sprintf("the %s trigger did not start %s: %v", t.Kind, t.Command, err)
	s.after(ctx, t, now)
	if perr == nil {
		health.Write(ctx, s.st, health.Project(p), subject, health.Error, text)
	}
}

// after moves a fired or skipped cron trigger to its next slot.
func (s *Scheduler) after(ctx context.Context, t db.Trigger, now time.Time) {
	s.tx(ctx, func(ctx context.Context, q *db.Queries) error {
		next, err := Next(t.Spec, now)
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
