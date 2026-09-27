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

// checks keeps when deck next looks at the version of a built-in provider and retries its red self-check.
type checks struct {
	running bool
	started bool
	version time.Time
	retry   time.Time
}

func (a *App) checksOf(name string) *checks {
	c := a.check[name]
	if c == nil {
		c = &checks{}
		a.check[name] = c
	}
	return c
}

// Checks runs the self-check of each built-in provider at the first call, when its version changed, and again
// while it is red; only the Loop calls it, so tests run self-checks on purpose.
func (a *App) Checks(ctx context.Context, now time.Time) {
	for _, pv := range a.builtin {
		sc, ok := pv.(provider.SelfChecker)
		if !ok {
			continue
		}
		a.checkMu.Lock()
		c := a.checksOf(pv.Name())
		due := !c.started || !now.Before(c.version)
		retry := !c.retry.IsZero() && !now.Before(c.retry)
		if c.running || !due && !retry {
			a.checkMu.Unlock()
			continue
		}
		c.version = now.Add(a.Cfg.VersionEvery)
		first := !c.started
		a.checkMu.Unlock()
		v, err := sc.Version(ctx)
		if err != nil {
			v = "unknown: " + err.Error()
		}
		h, _ := a.Store.Q.GetHealth(ctx, process.HealthSubject(pv.Name()))
		if first || retry || v != h.Version {
			a.startCheck(pv.Name(), false)
		}
	}
}

// startCheck runs a self-check in the background unless one is running; a change of version or a red check
// holds the provider's turns until it ends. One cut short by a limit or a login keeps no version, so it reruns.
func (a *App) startCheck(name string, force bool) bool {
	pv := a.builtinNamed(name)
	sc, ok := pv.(provider.SelfChecker)
	if !ok {
		return false
	}
	a.checkMu.Lock()
	c := a.checksOf(name)
	if c.running {
		a.checkMu.Unlock()
		return false
	}
	c.running, c.started, c.retry = true, true, time.Time{}
	a.checkMu.Unlock()
	ctx := a.ctx
	subject := process.HealthSubject(name)
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
		version := res.Version
		if status != "ok" && status != "error" {
			version = ""
		}
		a.writeCheck(context.WithoutCancel(ctx), subject, status, text, version)
		a.checkMu.Lock()
		c.running = false
		if status != "ok" {
			c.retry = a.Cfg.Now().Add(a.Cfg.SelfCheckRetry)
		}
		a.checkMu.Unlock()
		if status != "error" && status != "checking" {
			a.Runner.ProviderReady(context.WithoutCancel(ctx), name)
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
