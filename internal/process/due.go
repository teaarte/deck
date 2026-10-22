package process

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"deck2/internal/fault"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const (
	// silentAfter is how long a running process may go without output before it shows as silent.
	silentAfter = 30 * time.Minute
	// emptyKept is how long a process with an empty result stays in the history.
	emptyKept = 24 * time.Hour
	// ceilingWarning is how long before its ceiling an agent is told to save its work.
	ceilingWarning = 10 * time.Minute
)

// Due stops the processes at their ceiling, takes turns from models that gave no sign or went down, hears the closed ones not heard, wakes the held ones (the paused whose
// next try came too), marks the silent ones and purges the empty results kept long enough.
func (r *Runner) Due(ctx context.Context, now time.Time) {
	r.stopAtCeiling(ctx, now)
	r.watchTurns(ctx, now)
	r.warnOfCeiling(ctx, now)
	r.DrainClosed(ctx)
	r.Wake(ctx)
	r.markSilent(ctx, now)
	r.purgeEmpty(ctx, now)
}

func (r *Runner) stopAtCeiling(ctx context.Context, now time.Time) {
	ids, err := r.st.Q.ListCeilingReached(ctx, now.UnixMilli())
	if err != nil {
		slog.Error("ceilings", "err", err)
		return
	}
	for _, id := range ids {
		text := "deck: the process reached its time ceiling"
		if kids, err := r.st.Q.ListRunningChildren(ctx, store.ID(id)); err == nil && len(kids) > 0 {
			names := make([]string, 0, len(kids))
			for _, k := range kids {
				names = append(names, fmt.Sprintf("#%d %s", k.ID, k.Command))
			}
			text += "; the processes it started still run: " + strings.Join(names, ", ")
		}
		if err := r.end(ctx, id, StateError, fault.New(fault.Timeout, text), false); err != nil {
			slog.Error("stop at ceiling", "process", id, "err", err)
		}
	}
}

// warnOfCeiling tells a running agent once that its ceiling nears, so it saves what it did and says where it stopped.
func (r *Runner) warnOfCeiling(ctx context.Context, now time.Time) {
	rows, err := r.st.Q.ListCeilingNear(ctx, db.ListCeilingNearParams{Now: now.UnixMilli(), Soon: now.Add(ceilingWarning).UnixMilli()})
	if err != nil {
		slog.Error("ceilings near", "err", err)
		return
	}
	r.mu.Lock()
	for id, deadline := range r.warned {
		if deadline > 0 && deadline <= now.UnixMilli() {
			delete(r.warned, id)
		}
	}
	var due []db.ListCeilingNearRow
	for _, p := range rows {
		if r.warned[p.ID] != p.DeadlineMs && r.warned[p.ID] != -p.DeadlineMs {
			r.warned[p.ID] = -p.DeadlineMs
			due = append(due, p)
		}
	}
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.wg.Add(len(due))
	r.mu.Unlock()
	for _, p := range due {
		left := time.Duration(p.DeadlineMs-now.UnixMilli()) * time.Millisecond
		text := fmt.Sprintf("deck: this process reaches its time ceiling in %d minutes and then stops. Save what you did "+
			"to documents or data now, end any wait, and say in your result where you stopped and how to go on.", int(left.Round(time.Minute)/time.Minute))
		go func() {
			defer r.wg.Done()
			said := r.Say(r.ctx, p.ID, text)
			r.mu.Lock()
			defer r.mu.Unlock()
			if said == Refused {
				delete(r.warned, p.ID)
			} else {
				r.warned[p.ID] = p.DeadlineMs
			}
		}()
	}
}

// startCeiling starts the clock of the ceiling of a process from now.
func (r *Runner) startCeiling(ctx context.Context, id int64) error {
	return r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.StartCeiling(ctx, db.StartCeilingParams{Now: r.now().UnixMilli(), ID: id})
	})
}

func (r *Runner) markSilent(ctx context.Context, now time.Time) {
	_, err := store.TxGet(r.st, ctx, func(ctx context.Context, q *db.Queries) ([]int64, error) {
		return q.MarkSilentProcesses(ctx, now.Add(-silentAfter).UnixMilli())
	})
	if err != nil {
		slog.Error("silent processes", "err", err)
	}
}

func (r *Runner) purgeEmpty(ctx context.Context, now time.Time) {
	ids, err := store.TxGet(r.st, ctx, func(ctx context.Context, q *db.Queries) ([]int64, error) {
		return q.PurgeEmptyProcesses(ctx, store.Time(now.Add(-emptyKept)))
	})
	if err != nil {
		slog.Error("purge empty processes", "err", err)
		return
	}
	for _, id := range ids {
		_ = os.RemoveAll(r.runDir(id))
	}
}
