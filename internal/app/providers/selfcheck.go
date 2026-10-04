package providers

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"deck2/internal/health"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/store/db"
)

// checks keeps when deck next looks at the version of a built-in provider and retries its red self-check.
// selfCheckRetry repeats a red self-check; versionEvery looks for new programs of the built-in providers.
const (
	selfCheckRetry = 30 * time.Minute
	versionEvery   = 10 * time.Minute
)

type checks struct {
	running bool
	// again is set when the provider changed during a running check, which then checks it once more.
	again   bool
	started bool
	version time.Time
	retry   time.Time
}

func (s *Service) checksOf(name string) *checks {
	if s.check == nil {
		s.check = map[string]*checks{}
	}
	c := s.check[name]
	if c == nil {
		c = &checks{}
		s.check[name] = c
	}
	return c
}

// Checks runs the self-check of each provider that has one at the first call, when its version changed, and again
// while it is red; only the Loop calls it, so tests run self-checks on purpose.
func (s *Service) Checks(ctx context.Context, now time.Time) {
	for _, pv := range s.checked(ctx) {
		sc := pv.(provider.SelfChecker)
		s.mu.Lock()
		c := s.checksOf(pv.Name())
		due := !c.started || !now.Before(c.version)
		retry := !c.retry.IsZero() && !now.Before(c.retry)
		if c.running || !due && !retry {
			s.mu.Unlock()
			continue
		}
		c.version = now.Add(versionEvery)
		first := !c.started
		s.mu.Unlock()
		v, err := sc.Version(ctx)
		if err != nil {
			v = "unknown: " + err.Error()
		}
		h, _ := s.St.Q.GetHealth(ctx, process.HealthSubject(pv.Name()))
		if first || retry || v != h.Version {
			s.startCheck(pv.Name(), false)
		}
	}
}

// startCheck runs a self-check in the background unless one is running; a change of version or a red check
// holds the provider's turns until it ends. One cut short by a limit or a login keeps no version, so it reruns.
func (s *Service) startCheck(name string, force bool) bool {
	sc, ok := s.named(s.Ctx, name).(provider.SelfChecker)
	if !ok {
		return false
	}
	s.mu.Lock()
	c := s.checksOf(name)
	if c.running {
		c.again = c.again || force
		s.mu.Unlock()
		return false
	}
	c.running, c.started, c.retry = true, true, time.Time{}
	s.mu.Unlock()
	ctx := s.Ctx
	subject := process.HealthSubject(name)
	v, err := sc.Version(ctx)
	if err != nil {
		v = "unknown: " + err.Error()
	}
	h, _ := s.St.Q.GetHealth(ctx, subject)
	if force || v != h.Version || h.Status == "error" || h.Status == "checking" {
		s.writeCheck(ctx, subject, "checking", "self-check of "+v+" is running", "", "", 0)
	}
	s.BG.Add(1)
	go func() {
		defer s.BG.Done()
		ceiling := s.SelfCheckCeiling
		if c, ok := sc.(interface{ CheckCeiling() time.Duration }); ok && c.CheckCeiling() > 0 {
			ceiling = c.CheckCeiling()
		}
		cctx, cancel := context.WithTimeout(ctx, ceiling)
		res := sc.SelfCheck(cctx)
		cancel()
		if res.Version == "" {
			res.Version = v
		}
		status, text, verdict := "ok", "", ""
		switch {
		case ctx.Err() != nil:
			status, text = "checking", "deck stopped during the self-check"
		case res.RateLimited:
			status, text = "paused", "the self-check hit the usage limit; "+errText(res.Err)
		case res.NeedsLogin:
			status, text = "needs_login", "the self-check found no valid login; "+errText(res.Err)
		case res.Err != nil && res.Slow:
			status, text, verdict = "error", fmt.Sprintf("the model did not finish the self-check of %s within %s: %s", res.Version, ceiling, res.Err), "model"
		case res.Err != nil && res.ModelFailed:
			status, text, verdict = "error", fmt.Sprintf("the model does not manage the self-check of %s: %s; pick a stronger model", res.Version, res.Err), "model"
		case res.Err != nil:
			status, text, verdict = "error", fmt.Sprintf("self-check of %s failed: %s", res.Version, res.Err), "adapter"
		}
		if res.Speed > 0 {
			text = strings.TrimPrefix(text+fmt.Sprintf("; the model answered at %.0f tokens/s", res.Speed), "; ")
		}
		version := res.Version
		if status != "ok" && status != "error" {
			version = ""
		}
		s.mu.Lock()
		again := c.again && ctx.Err() == nil
		c.again = false
		if again {
			c.running = false
			s.mu.Unlock()
			s.startCheck(name, true)
			return
		}
		s.mu.Unlock()
		s.writeCheck(context.WithoutCancel(ctx), subject, status, text, version, verdict, res.Speed)
		s.mu.Lock()
		c.running = false
		if status != "ok" {
			c.retry = s.Now().Add(selfCheckRetry)
		}
		s.mu.Unlock()
		if status != "error" && status != "checking" {
			s.Wake(context.WithoutCancel(ctx))
		}
	}()
	return true
}

func (s *Service) writeCheck(ctx context.Context, subject, status, text, version, verdict string, speed float64) {
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := health.Set(ctx, q, health.Of{}, subject, status, text); err != nil {
			return err
		}
		if status == "checking" {
			return nil
		}
		if err := q.SetHealthCheck(ctx, db.SetHealthCheckParams{Verdict: verdict, Speed: speed, Subject: subject}); err != nil {
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
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
