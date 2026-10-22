package process

import (
	"context"

	"deck2/internal/health"
	"deck2/internal/provider"
	"deck2/internal/store/db"
)

// unpause clears the pause of a provider once the reset a paused turn waited for has come; the turn tells again.
func unpause(ctx context.Context, q *db.Queries, name string) error {
	if h, err := health.Get(ctx, q, health.Of{}, health.Provider(name)); err != nil || h.Status != health.Paused {
		return nil
	}
	return ProviderHealth(ctx, q, name, health.OK, "")
}

// ProviderHealth writes the health row of a provider, a row of the machine; the one writer of such rows.
func ProviderHealth(ctx context.Context, q *db.Queries, name, status, text string) error {
	_, err := health.Set(ctx, q, health.Of{}, health.Provider(name), status, text)
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
	subject := health.Provider(pv.Name())
	if _, checked := pv.(provider.SelfChecker); checked {
		if h, err := health.Get(ctx, q, health.Of{}, subject); err == nil && (h.Status == health.Checking || h.Status == health.Error) {
			return nil
		}
	}
	return ProviderHealth(ctx, q, pv.Name(), status, text)
}
