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

// ModelCall is one call of a model for the journal of model calls: of a process when it has one, else of a project.
type ModelCall struct {
	Workspace, Project, Process int64
	// Provider is what the call is shown by: a model of deck's settings, or the provider of an agent turn.
	Provider     string
	Usage        provider.Usage
	Subscription bool
}

// LogModelCall is the one writer of the journal of model calls; a call of a provider that counts only the session
// costs what the session grew by since its last call, or all of it when the count started over.
func LogModelCall(ctx context.Context, q *db.Queries, c ModelCall, now string) error {
	u := c.Usage
	if u.CostUSD == nil && u.SessionCostUSD != nil {
		p, err := q.GetProcess(ctx, c.Process)
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
	return q.LogModelCall(ctx, db.LogModelCallParams{WorkspaceID: c.Workspace, ProjectID: store.ID(c.Project), ProcessID: store.ID(c.Process),
		ConnectionID: store.ID(u.Connection), Provider: c.Provider, RequestModel: u.RequestModel, ResponseModel: u.Model,
		InputTokens: u.Input, OutputTokens: u.Output, CacheReadTokens: u.CacheRead, CacheCreationTokens: u.CacheCreation,
		DurationMs: u.Duration.Milliseconds(), CostUsd: store.Cost(u.CostUSD), CreatedAt: now,
		SessionCostUsd: store.Cost(u.SessionCostUSD), Subscription: store.Bool(c.Subscription)})
}

// usage writes one model call of an agent turn; a model of deck's settings tells whether it is on a subscription.
func (r *Runner) usage(ctx context.Context, workspace, processID int64, pv provider.Provider, u provider.Usage) {
	r.answered(processID)
	_, subscribed := pv.(provider.Subscribed)
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if m, err := q.GetModelOf(ctx, db.GetModelOfParams{ConnectionID: u.Connection, Model: u.RequestModel}); u.Connection != 0 && err == nil {
			subscribed = m.Model.Subscription != 0
		}
		return LogModelCall(ctx, q, ModelCall{Workspace: workspace, Process: processID, Provider: pv.Name(), Usage: u, Subscription: subscribed}, store.Time(r.now()))
	})
	if err != nil {
		slog.Error("model call journal", "process", processID, "err", err)
	}
}
