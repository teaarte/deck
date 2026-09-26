package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/store/db"
)

// checks keeps when deck next looks at the version of the main provider and retries a red self-check.
type checks struct {
	running bool
	started bool
	version time.Time
	retry   time.Time
}

// Checks runs the self-check of the main provider at the first call, when its version changed, and again
// while it is red; only the Loop calls it, so tests run self-checks on purpose.
func (a *App) Checks(ctx context.Context, now time.Time) {
	sc, ok := a.main.(provider.SelfChecker)
	if !ok {
		return
	}
	a.checkMu.Lock()
	due := !a.check.started || !now.Before(a.check.version)
	retry := !a.check.retry.IsZero() && !now.Before(a.check.retry)
	if a.check.running || !due && !retry {
		a.checkMu.Unlock()
		return
	}
	a.check.version = now.Add(a.Cfg.VersionEvery)
	first := !a.check.started
	a.checkMu.Unlock()
	v, err := sc.Version(ctx)
	if err != nil {
		v = "unknown: " + err.Error()
	}
	h, _ := a.Store.Q.GetHealth(ctx, process.HealthSubject(a.main.Name()))
	if first || retry || v != h.Version {
		a.startCheck(false)
	}
}

// startCheck runs a self-check in the background unless one is running; a change of version or a red
// self-check holds the turns of the provider until it ends.
func (a *App) startCheck(force bool) bool {
	sc, ok := a.main.(provider.SelfChecker)
	if !ok {
		return false
	}
	a.checkMu.Lock()
	if a.check.running {
		a.checkMu.Unlock()
		return false
	}
	a.check.running, a.check.started, a.check.retry = true, true, time.Time{}
	a.checkMu.Unlock()
	ctx := a.ctx
	subject := process.HealthSubject(a.main.Name())
	v, err := sc.Version(ctx)
	if err != nil {
		v = "unknown: " + err.Error()
	}
	h, _ := a.Store.Q.GetHealth(ctx, subject)
	if force || v != h.Version || h.Status == "error" || h.Status == "checking" {
		a.writeCheck(ctx, subject, "checking", "self-check of "+v+" is running", "")
	}
	a.bg.Add(1)
	go func() {
		defer a.bg.Done()
		cctx, cancel := context.WithTimeout(ctx, a.Cfg.SelfCheckCeiling)
		res := sc.SelfCheck(cctx)
		cancel()
		if res.Version == "" {
			res.Version = v
		}
		status, text := "ok", ""
		switch {
		case ctx.Err() != nil:
			status, text = "checking", "deck stopped during the self-check"
		case res.RateLimited:
			status, text = "paused", "the self-check hit the usage limit; "+errText(res.Err)
		case res.NeedsLogin:
			status, text = "needs_login", "the self-check found no valid login; "+errText(res.Err)
		case res.Err != nil:
			status, text = "error", fmt.Sprintf("self-check of %s failed: %s", res.Version, res.Err)
		}
		a.writeCheck(context.WithoutCancel(ctx), subject, status, text, res.Version)
		a.checkMu.Lock()
		a.check.running = false
		if status == "error" {
			a.check.retry = a.Cfg.Now().Add(a.Cfg.SelfCheckRetry)
		}
		a.checkMu.Unlock()
		if status != "error" && status != "checking" {
			a.Runner.ProviderReady(context.WithoutCancel(ctx), a.main.Name())
		}
	}()
	return true
}

func (a *App) writeCheck(ctx context.Context, subject, status, text, version string) {
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := q.SetHealth(ctx, db.SetHealthParams{Subject: subject, Status: status, Error: text}); err != nil {
			return err
		}
		if version == "" {
			return nil
		}
		return q.SetHealthVersion(ctx, db.SetHealthVersionParams{Version: version, Subject: subject})
	})
	if err != nil {
		slog.Error("self-check health", "err", err)
	}
	a.Bus.Publish("health")
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
