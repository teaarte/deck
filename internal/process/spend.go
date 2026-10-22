package process

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// usage writes one model call of a process to the journal of model calls; a turn whose provider counts only the
// session costs what the session grew by since its last call, or all of it when the count started over.
func (r *Runner) usage(ctx context.Context, workspace, processID int64, pv provider.Provider, u provider.Usage) {
	_, subscribed := pv.(provider.Subscribed)
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if m, err := q.GetModelOf(ctx, db.GetModelOfParams{ConnectionID: u.Connection, Model: u.RequestModel}); u.Connection != 0 && err == nil {
			subscribed = m.Model.Subscription != 0
		}
		if u.CostUSD == nil && u.SessionCostUSD != nil {
			p, err := q.GetProcess(ctx, processID)
			if err != nil {
				return err
			}
			last, err := q.LastSessionCost(ctx, p.SessionID)
			if err != nil && !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			cost := *u.SessionCostUSD - last
			if cost < 0 {
				cost = *u.SessionCostUSD
			}
			u.CostUSD = &cost
		}
		return q.LogModelCall(ctx, db.LogModelCallParams{WorkspaceID: workspace, ProcessID: store.ID(processID), ConnectionID: store.ID(u.Connection), Provider: pv.Name(),
			RequestModel: u.RequestModel, ResponseModel: u.Model,
			InputTokens: u.Input, OutputTokens: u.Output, CacheReadTokens: u.CacheRead, CacheCreationTokens: u.CacheCreation,
			DurationMs: u.Duration.Milliseconds(), CostUsd: store.Cost(u.CostUSD), CreatedAt: store.Time(r.now()),
			SessionCostUsd: store.Cost(u.SessionCostUSD), Subscription: store.Bool(subscribed)})
	})
	if err != nil {
		slog.Error("model call journal", "process", processID, "err", err)
	}
}
