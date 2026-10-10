package process

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"deck2/internal/ask"
	"deck2/internal/health"
	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// needLogin parks the process until the owner logs the provider in again and picks how to continue.
func (r *Runner) needLogin(ctx context.Context, pv provider.Provider, processID int64, text string) {
	payload, _ := json.Marshal(map[string]any{"answers": ask.LoginAnswers})
	msg := fmt.Sprintf("%s needs a login: %s. Log in again in a terminal, then retry this session or start a new one with a report of what was done.",
		pv.Name(), strings.TrimSpace(text))
	var n int64
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		if n, err = r.leaveRunning(ctx, q, processID, StateNeedsLogin, waiting{}, false, sql.NullInt64{}); err != nil || n == 0 {
			return err
		}
		if _, err := ask.New(ctx, q, processID, ask.KindLogin, msg, string(payload), time.Time{}); err != nil {
			return err
		}
		return setHealth(ctx, q, pv, health.NeedsLogin, text)
	})
	if err != nil {
		r.finish(ctx, processID, StateError, "", false, "needs login: "+text)
		return
	}
	if n == 0 {
		return
	}
}

// lostTools holds a turn whose agent did not get deck's tools until the self-check of its provider is green again;
// the provider is red meanwhile, so its other turns wait too.
func (r *Runner) lostTools(ctx context.Context, t turn, pv provider.Provider, err error) {
	text := "the agent started without deck's tools, so deck holds the turns of " + pv.Name() + " until its self-check passes: " + err.Error()
	txErr := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := ProviderHealth(ctx, q, pv.Name(), health.Error, untrusted.Cut(text, 1000)); err != nil {
			return err
		}
		if err := q.SetHealthCheck(ctx, db.SetHealthCheckParams{Verdict: "adapter", Subject: HealthSubject(pv.Name())}); err != nil {
			return err
		}
		_, err := r.holdIn(ctx, q, t, StateWaitingProvider)
		return err
	})
	if txErr != nil {
		r.finish(ctx, t.process, StateError, "", false, err.Error())
		return
	}
	r.deps.Broke(ctx, pv.Name())
}

// HealthSubject is the health row of a provider.
func HealthSubject(name string) string { return "provider:" + name }

// ProviderHealth writes the health row of a provider, a row of the machine; the one writer of such rows.
func ProviderHealth(ctx context.Context, q *db.Queries, name, status, text string) error {
	return health.Set(ctx, q, health.Of{}, HealthSubject(name), status, text)
}

// providerHealth records what a turn showed about its provider. The health of a provider with a self-check is
// the self-check's: a turn only lifts a lost login or a limit there.
func (r *Runner) providerHealth(ctx context.Context, pv provider.Provider, res provider.Result, err error) {
	_, checked := pv.(provider.SelfChecker)
	subject := HealthSubject(pv.Name())
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		switch {
		case err != nil && !checked:
			return ProviderHealth(ctx, q, pv.Name(), health.Error, err.Error())
		case err != nil || res.NeedsLogin || res.RateLimited:
			return nil
		case checked:
			return q.MarkHealthy(ctx, subject)
		}
		return ProviderHealth(ctx, q, pv.Name(), health.OK, "")
	})
}

// setHealth records what a turn found about its provider, unless its self-check is running or red: then the
// health is the self-check's.
func setHealth(ctx context.Context, q *db.Queries, pv provider.Provider, status, text string) error {
	subject := HealthSubject(pv.Name())
	if _, checked := pv.(provider.SelfChecker); checked {
		if h, err := q.GetHealth(ctx, subject); err == nil && (h.Status == health.Checking || h.Status == health.Error) {
			return nil
		}
	}
	return ProviderHealth(ctx, q, pv.Name(), status, text)
}

// usage writes one model call of a process to the journal of model calls; a turn whose provider counts only the
// session costs what the session grew by since its last call, or all of it when the count started over.
func (r *Runner) usage(ctx context.Context, workspace, processID int64, name string, u provider.Usage) {
	err := r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if u.CostUSD == nil && u.SessionCostUSD != nil {
			p, err := q.GetProcess(ctx, processID)
			if err != nil {
				return err
			}
			if last, err := q.LastSessionCost(ctx, p.SessionID); err == nil {
				cost := *u.SessionCostUSD - last
				if cost < 0 {
					cost = *u.SessionCostUSD
				}
				u.CostUSD = &cost
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
		}
		return q.LogModelCall(ctx, db.LogModelCallParams{WorkspaceID: workspace, ProcessID: store.ID(processID), ConnectionID: store.ID(u.Connection), Provider: name,
			RequestModel: u.RequestModel, ResponseModel: u.Model,
			InputTokens: u.Input, OutputTokens: u.Output, CacheReadTokens: u.CacheRead, CacheCreationTokens: u.CacheCreation,
			DurationMs: u.Duration.Milliseconds(), CostUsd: store.Cost(u.CostUSD), CreatedAt: store.Time(r.now()),
			SessionCostUsd: store.Cost(u.SessionCostUSD)})
	})
	if err != nil {
		slog.Error("model call journal", "process", processID, "err", err)
	}
}

// blocked reports whether the owner still owes the agent an answer; a result produced meanwhile is
// premature (a schema forces the agent to answer even when told to stop), so the process parks instead.
func (r *Runner) blocked(ctx context.Context, processID int64) bool {
	open, _ := r.st.Q.ListOpenAsksOfProcess(ctx, processID)
	pending, _ := r.untold(ctx, processID)
	return len(open) > 0 || !pending.empty()
}

// questioned reports whether a question to the owner is still open; a turn that broke meanwhile waits for its
// answer instead of failing, and the answer resumes the session.
func (r *Runner) questioned(ctx context.Context, processID int64) bool {
	open, _ := r.st.Q.ListOpenAsksOfProcess(ctx, processID)
	return slices.ContainsFunc(open, func(a db.Ask) bool { return a.Kind == ask.KindQuestion && a.State == ask.StateOpen })
}

// park moves the process to waiting_answer, where its ceiling does not tick, and immediately delivers
// answers that arrived while it was running.
func (r *Runner) park(ctx context.Context, processID int64) {
	var parked bool
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		parked, err = r.parkIn(ctx, q, processID)
		return err
	})
	if !parked {
		return
	}
	if err := r.Deliver(ctx, processID); err != nil {
		r.finish(ctx, processID, StateError, "", false, "resume after answer: "+err.Error())
	}
	_ = r.hear(ctx, processID, func(h Hook, p db.Process) { h.Parked(ctx, p) })
}

// parkIn moves a running process to waiting_answer in a transaction; its ceiling stops.
func (r *Runner) parkIn(ctx context.Context, q *db.Queries, processID int64) (bool, error) {
	n, err := r.leaveRunning(ctx, q, processID, StateWaitingAnswer, waiting{}, false, sql.NullInt64{})
	return n > 0, err
}
