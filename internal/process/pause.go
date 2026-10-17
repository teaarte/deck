package process

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"deck2/internal/fault"
	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// pause holds a process on a usage limit until it resets, or an hour when no reset time came, until the owner stops
// it; Wake then goes on in the same session, and the self-check of the provider runs again then.
func (r *Runner) pause(ctx context.Context, pv provider.Provider, processID int64, resetsAt int64, text string) {
	next := r.resetOf(resetsAt)
	w := waiting{Message: "deck: the provider's usage limit should have reset. Continue where you stopped."}
	var n int64
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		if n, err = r.leaveRunning(ctx, q, processID, StateRateLimited, w, false, sql.NullInt64{Int64: next, Valid: true}); err != nil || n == 0 {
			return err
		}
		return setHealth(ctx, q, pv, "paused", limitText(next, text))
	})
	if err != nil {
		r.finish(ctx, processID, StateError, "", false, fault.Wrap("pause on the usage limit", err))
		return
	}
	if n > 0 {
		r.deps.Recheck(pv.Name(), time.UnixMilli(next))
	}
}

// limited pauses a provider on a usage limit a process of it left for another model, until the limit resets.
func (r *Runner) limited(ctx context.Context, pv provider.Provider, resetsAt int64, text string) {
	next := r.resetOf(resetsAt)
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return setHealth(ctx, q, pv, "paused", limitText(next, text))
	})
	if err == nil {
		r.deps.Recheck(pv.Name(), time.UnixMilli(next))
	}
}

// resetOf is when a usage limit resets in unix milliseconds: resetsAt, or an hour from now when it did not say.
func (r *Runner) resetOf(resetsAt int64) int64 {
	now := r.now()
	if reset := resetsAt * 1000; reset > now.UnixMilli() {
		return reset
	}
	return now.Add(pauseProbe).UnixMilli()
}

func limitText(next int64, text string) string {
	return fmt.Sprintf("usage limit reached; next try at %s: %s", store.Time(time.UnixMilli(next)), untrusted.Cut(strings.TrimSpace(text), 500))
}
