package trigger

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/robfig/cron/v3"

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
// first minute after it; in an hour they repeat, a spec of every hour holds its slots in both passes, another once.
func Next(spec string, after time.Time) (string, error) {
	sched, err := manifest.CronParser.Parse(spec)
	if err != nil {
		return "", err
	}
	s, ok := sched.(*cron.SpecSchedule)
	hourly := ok && s.Hour&(1<<24-1) == 1<<24-1
	a := after.In(time.Local)
	// From the first pass of a repeated hour a spec of every hour goes on minute by minute into the second.
	if ps := wallPasses(a); hourly && len(ps) == 2 && !a.After(ps[0]) {
		for m := a.Truncate(time.Minute).Add(time.Minute); !m.After(ps[1]); m = m.Add(time.Minute) {
			if w := wall(m); sched.Next(w.Add(-time.Second)).Equal(w) {
				return store.Time(m), nil
			}
		}
	}
	w := wall(a)
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
		passes := wallPasses(t)
		if !hourly {
			passes = passes[len(passes)-1:]
		}
		for _, p := range passes {
			if p.After(after) {
				return store.Time(p), nil
			}
		}
	}
}

// wall is the reading of the owner's clock at t, as a time in UTC.
func wall(t time.Time) time.Time {
	t = t.In(time.Local)
	return time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), t.Second(), t.Nanosecond(), time.UTC)
}

// wallPasses are the instants that read the wall time of t: two in an hour the clocks repeat, earlier first.
func wallPasses(t time.Time) []time.Time {
	_, before := t.Add(-3 * time.Hour).Zone()
	_, later := t.Add(3 * time.Hour).Zone()
	d := time.Duration(before-later) * time.Second
	if d <= 0 {
		return []time.Time{t}
	}
	var out []time.Time
	for _, p := range []time.Time{t.Add(-d), t, t.Add(d)} {
		if p.Hour() == t.Hour() && p.Minute() == t.Minute() && p.Day() == t.Day() {
			out = append(out, p)
		}
	}
	return out
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

// goesOn reports whether the process a fire gave is a run still going rather than one it started: its last run,
// another run of the record it joined, or the run a refused one handed on to.
func (s *Scheduler) goesOn(ctx context.Context, t db.Trigger, id int64) bool {
	if t.LastProcessID.Valid && id == t.LastProcessID.Int64 {
		return true
	}
	p, err := s.st.Q.GetProcess(ctx, id)
	return err == nil && p.TriggerID.Int64 != t.ID
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
	subject := health.Trigger(p.Name, t.Command)
	if err == nil && id != 0 && s.goesOn(ctx, t, id) {
		s.after(ctx, t, now)
		if perr == nil {
			if err := health.Write(ctx, s.st, health.Project(p), subject, health.Warning,
				fmt.Sprintf("the %s trigger did not start %s at %s: its run #%d still goes", t.Kind, t.Command, t.NextRunAt.String, id)); err != nil {
				slog.Error("write health", "err", err)
			}
		}
		return
	}
	if err == nil {
		if perr == nil {
			s.tx(ctx, func(ctx context.Context, q *db.Queries) error {
				return health.Clear(ctx, q, health.Project(p), subject)
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
		if err := health.Write(ctx, s.st, health.Project(p), subject, health.Error, text); err != nil {
			slog.Error("write health", "err", err)
		}
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
