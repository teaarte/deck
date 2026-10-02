// Package health keeps the health rows of deck: one per subject, with its state and why.
package health

import (
	"context"

	"deck2/internal/events"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// limit is the longest text a health row keeps.
const limit = 2000

// Set writes a health row in a transaction; the caller publishes "health" once it commits.
func Set(ctx context.Context, q *db.Queries, subject, status, text string) error {
	return q.SetHealth(ctx, db.SetHealthParams{Subject: subject, Status: status, Error: untrusted.Cut(text, limit)})
}

// Write sets a health row in a transaction of its own and tells the screens.
func Write(ctx context.Context, st *store.Store, bus *events.Bus, subject, status, text string) {
	err := st.Tx(context.WithoutCancel(ctx), func(ctx context.Context, q *db.Queries) error { return Set(ctx, q, subject, status, text) })
	if err == nil {
		bus.Publish("health")
	}
}
