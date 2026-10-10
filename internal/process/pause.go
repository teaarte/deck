package process

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// pause holds a process on a usage limit until it resets, or an hour when no reset time came, until the owner stops
// it; Wake then goes on in the same session, and the self-check of the provider runs again then.
func (r *Runner) pause(ctx context.Context, pv provider.Provider, processID int64, resetsAt int64, text string) {
	now := r.now()
	next := now.Add(pauseProbe).UnixMilli()
	if reset := resetsAt * 1000; reset > now.UnixMilli() {
		next = reset
	}
	w := waiting{Message: "deck: the provider's usage limit should have reset. Continue where you stopped."}
	var n int64
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		if n, err = r.leaveRunning(ctx, q, processID, StateRateLimited, w, false, sql.NullInt64{Int64: next, Valid: true}); err != nil || n == 0 {
			return err
		}
		why := fmt.Sprintf("usage limit reached; next try at %s: %s", store.Time(time.UnixMilli(next)), untrusted.Cut(strings.TrimSpace(text), 500))
		return setHealth(ctx, q, pv, "paused", why)
	})
	if err != nil {
		r.finish(ctx, processID, StateError, "", false, "pause on the usage limit: "+err.Error())
		return
	}
	if n > 0 {
		r.deps.Recheck(pv.Name(), time.UnixMilli(next))
	}
}
