package process

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// pause holds a process its provider refused on a usage limit until the limit resets, or for a probe an hour on
// when no reset time came, until the owner stops it; the ceiling stands still.
func (r *Runner) pause(ctx context.Context, pv provider.Provider, processID int64, resetsAt int64, text string) {
	now := r.now()
	next := now.Add(pauseProbe).UnixMilli()
	if reset := resetsAt * 1000; reset > now.UnixMilli() {
		next = reset
	}
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		n, err := q.PauseProcess(ctx, db.PauseProcessParams{UntilMs: ms(next), Now: now.UnixMilli(), ID: processID})
		if err != nil || n == 0 {
			return err
		}
		why := fmt.Sprintf("usage limit reached; next try at %s: %s", store.Time(time.UnixMilli(next)), untrusted.Cut(strings.TrimSpace(text), 500))
		return setHealth(ctx, q, pv, "paused", why)
	})
	if err != nil {
		r.finish(ctx, processID, StateError, "", false, "pause on the usage limit: "+err.Error())
	}
}

// Unpause tries again every paused process whose next try is due, in the same session, with the answers that came
// meanwhile.
func (r *Runner) Unpause(ctx context.Context, now time.Time) {
	ps, err := r.st.Q.ListPausesDue(ctx, now.UnixMilli())
	if err != nil {
		slog.Error("paused processes", "err", err)
		return
	}
	for _, p := range ps {
		if ok, err := r.claim(ctx, p.ID, StateRateLimited); err != nil || !ok {
			continue
		}
		msg := "deck: the provider's usage limit should have reset. Continue where you stopped."
		pending, _ := r.untold(ctx, p.ID)
		if !pending.empty() {
			msg += "\n" + pending.message()
		}
		if err := r.resume(ctx, p.ID, msg, pending.ids(), false); err != nil {
			r.finish(ctx, p.ID, StateError, "", false, "resume after the usage limit: "+err.Error())
		}
	}
}

func ms(v int64) sql.NullInt64 { return sql.NullInt64{Int64: v, Valid: true} }
