package process

import (
	"context"
	"log/slog"
	"os"
	"time"

	"deck2/internal/store"
	"deck2/internal/store/db"
)

const (
	// silentAfter is how long a running process may go without output before it shows as silent.
	silentAfter = 30 * time.Minute
	// emptyKept is how long a process with an empty result stays in the history.
	emptyKept = 24 * time.Hour
)

// Due stops the processes at their ceiling, hears the closed ones not heard, wakes the held ones (the paused whose
// next try came too), marks the silent ones and purges the empty results kept long enough.
func (r *Runner) Due(ctx context.Context, now time.Time) {
	r.stopAtCeiling(ctx, now)
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
		if err := r.end(ctx, id, StateError, "deck: the process reached its time ceiling"); err != nil {
			slog.Error("stop at ceiling", "process", id, "err", err)
		}
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
