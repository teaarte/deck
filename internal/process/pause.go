package process

import (
	"context"
	"time"

	"deck2/internal/fault"
	"deck2/internal/provider"
	"deck2/internal/store/db"
)

// pause holds a process on a usage limit of its provider until it is called again, until the owner stops it; Wake
// then goes on in the same session.
func (r *Runner) pause(ctx context.Context, pv provider.Provider, processID int64, until time.Time) {
	w := waiting{Message: "deck: the provider's usage limit should have reset. Continue where you stopped."}
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := r.leaveRunning(ctx, q, processID, wait{reason: WaitLimit, on: pv.Name(), silence: SilenceRetry, until: until}, w, false)
		return err
	})
	if err != nil {
		r.finish(ctx, processID, StateError, "", false, fault.Wrap("pause on the usage limit", err))
	}
}
