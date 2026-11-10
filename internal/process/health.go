package process

import (
	"context"
	"time"

	"deck2/internal/health"
	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// unpause clears the pause of a provider once the reset a paused turn waited for has come; the turn tells again.
func unpause(ctx context.Context, q *db.Queries, now time.Time, name string) error {
	if h, err := health.Get(ctx, q, health.Of{}, health.Provider(name)); err != nil || h.Status != health.Paused {
		return nil
	}
	return ProviderHealth(ctx, q, now, name, health.OK, "")
}

// ProviderHealth writes the health row of a provider, a row of the machine; the one writer of such rows.
func ProviderHealth(ctx context.Context, q *db.Queries, now time.Time, name, status, text string) error {
	_, err := health.Set(ctx, q, now, health.Of{}, health.Provider(name), status, text)
	return err
}

// providerHealth records what a turn showed about its provider. The health of a provider with a self-check is
// the self-check's: a turn only lifts a lost login or a limit there.
func (r *Runner) providerHealth(ctx context.Context, pv provider.Provider, res provider.Result, err error) {
	_, checked := pv.(provider.SelfChecker)
	subject := health.Provider(pv.Name())
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		switch {
		case err != nil && !checked:
			return ProviderHealth(ctx, q, r.now(), pv.Name(), health.Error, err.Error())
		case err != nil || res.Held() != "":
			return nil
		case checked:
			return q.MarkHealthy(ctx, db.MarkHealthyParams{Subject: subject, Now: store.Time(r.now())})
		}
		return ProviderHealth(ctx, q, r.now(), pv.Name(), health.OK, "")
	})
}

// setHealth records what a turn found about its provider, unless its self-check is running or red: then the
// health is the self-check's.
func setHealth(ctx context.Context, q *db.Queries, now time.Time, pv provider.Provider, status, text string) error {
	subject := health.Provider(pv.Name())
	if _, checked := pv.(provider.SelfChecker); checked {
		if h, err := health.Get(ctx, q, health.Of{}, subject); err == nil && (h.Status == health.Checking || h.Status == health.Error) {
			return nil
		}
	}
	return ProviderHealth(ctx, q, now, pv.Name(), status, text)
}
