package process

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

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
	// Command names a call without a process, such as a self-check.
	Command string
}

// LogModelCall is the one writer of the journal of model calls; a call of a provider that counts only the session
// costs what the session grew by since its last call, or all of it when the count started over.
func LogModelCall(ctx context.Context, q *db.Queries, c ModelCall, now string) error {
	u := c.Usage
	if u.CostUSD == nil && u.SessionCostUSD != nil && c.Process == 0 {
		u.CostUSD = u.SessionCostUSD
	}
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
		SessionCostUsd: store.Cost(u.SessionCostUSD), Subscription: store.Bool(c.Subscription), Command: c.Command})
}

// usage writes one model call of an agent turn; a model of deck's settings tells whether it is on a subscription.
func (r *Runner) usage(ctx context.Context, workspace, processID int64, pv provider.Provider, u provider.Usage) {
	r.answered(processID)
	LogUsage(ctx, r.st, ModelCall{Workspace: workspace, Process: processID, Usage: u}, pv, r.now())
}

// LogUsage keeps a call of the model of an agent turn of provider pv, in a transaction of its own: on a subscription
// when the provider or the model of its connection is one.
func LogUsage(ctx context.Context, st *store.Store, c ModelCall, pv provider.Provider, now time.Time) {
	_, c.Subscription = pv.(provider.Subscribed)
	c.Provider = pv.Name()
	err := st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if m, err := q.GetModelOf(ctx, db.GetModelOfParams{ConnectionID: c.Usage.Connection, Model: c.Usage.RequestModel}); c.Usage.Connection != 0 && err == nil {
			c.Subscription = m.Model.Subscription != 0
		}
		return LogModelCall(ctx, q, c, store.Time(now))
	})
	if err != nil {
		slog.Error("model call journal", "process", c.Process, "command", c.Command, "err", err)
	}
}

// Spent adds up what calls cost: the paid ones, nil when one has no price or there is none, and apart what those
// on a subscription would cost, nil when there is none with a price.
func Spent[T any](calls []T, of func(T) (cost sql.NullFloat64, subscription bool)) (paid, subscribed *float64) {
	sum, sub, priced, n, m := 0.0, 0.0, true, 0, 0
	for _, c := range calls {
		cost, s := of(c)
		switch {
		case s && cost.Valid:
			sub, m = sub+cost.Float64, m+1
		case !s:
			sum, priced, n = sum+cost.Float64, priced && cost.Valid, n+1
		}
	}
	if n > 0 && priced {
		paid = &sum
	}
	if m > 0 {
		subscribed = &sub
	}
	return paid, subscribed
}
