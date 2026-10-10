package providers

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"deck2/internal/health"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/store/db"
)

// selfCheckRetry repeats a self-check that holds turns or was cut short, and is the least time between self-checks a
// broken turn starts.
const (
	selfCheckRetry = 30 * time.Minute
	versionTimeout = 30 * time.Second
)

type checks struct {
	running bool
	// again is set when the provider changed during a running check, which then checks it once more.
	again   bool
	started bool
	last    time.Time
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

// Checks finds the ACP agents and runs the self-check of each provider that has one at the first call, and again
// while it is red; only the Loop calls it, so tests run self-checks on purpose.
func (s *Service) Checks(ctx context.Context, now time.Time) {
	s.mu.Lock()
	found := s.found
	s.found = true
	s.mu.Unlock()
	if !found {
		s.Go(func() {
			for _, name := range acp.Names() {
				s.discover(s.Ctx, name)
			}
		})
	}
	for _, pv := range s.checked(ctx) {
		s.mu.Lock()
		c := s.checksOf(pv.Name())
		due := !c.started || !c.retry.IsZero() && !now.Before(c.retry)
		s.mu.Unlock()
		if due {
			s.startCheck(pv.Name(), false)
		}
	}
}

// Broke checks a provider whose turn failed, unless it was checked within selfCheckRetry.
func (s *Service) Broke(ctx context.Context, name string) {
	s.mu.Lock()
	c := s.checksOf(name)
	recent := !c.last.IsZero() && s.Now().Before(c.last.Add(selfCheckRetry))
	s.mu.Unlock()
	if !recent {
		s.startCheck(name, false)
	}
}

// startCheck runs a self-check in the background unless one is running; one the owner asked for or of a red
// provider holds its turns until it ends. One cut short by a limit or a login keeps no version, so it reruns.
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
	c.running, c.started, c.last, c.retry = true, true, s.Now(), time.Time{}
	s.mu.Unlock()
	ctx := s.Ctx
	subject := process.HealthSubject(name)
	started := s.Go(func() {
		h, _ := s.St.Q.GetHealth(ctx, subject)
		if force || h.Status == "error" || h.Status == "checking" {
			s.writeCheck(ctx, subject, "checking", "the self-check is running", "", "", 0)
		}
		ceiling := s.SelfCheckCeiling
		if c, ok := sc.(interface{ CheckCeiling() time.Duration }); ok && c.CheckCeiling() > 0 {
			ceiling = c.CheckCeiling()
		}
		cctx, cancel := context.WithTimeout(ctx, ceiling)
		res := sc.SelfCheck(cctx)
		cancel()
		of := cmp.Or(res.Version, "an unknown version")
		status, text, verdict := "ok", "", ""
		switch {
		case ctx.Err() != nil:
			status, text = "checking", "deck stopped during the self-check"
		case res.RateLimited:
			status, text = "paused", "the self-check hit the usage limit; "+errText(res.Err)
		case res.NeedsLogin:
			status, text = "needs_login", "the self-check found no valid login; "+errText(res.Err)
		case res.Err != nil && res.Slow:
			status, text, verdict = "warning", fmt.Sprintf("the model did not finish the self-check of %s within %s: %s", of, ceiling, res.Err), "model"
		case res.Err != nil && res.ModelFailed:
			status, text, verdict = "error", fmt.Sprintf("the model does not manage the self-check of %s: %s; pick a stronger model", of, res.Err), "model"
		case res.Err != nil:
			status, text, verdict = "error", fmt.Sprintf("self-check of %s failed: %s", of, res.Err), "adapter"
		}
		if res.Speed > 0 {
			text = strings.TrimPrefix(text+fmt.Sprintf("; the model answered at %.0f tokens/s", res.Speed), "; ")
		}
		version := res.Version
		if status != "ok" && status != "error" && status != "warning" {
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
		if a, ok := sc.(interface{ AgentName() string }); ok && ctx.Err() == nil {
			s.agentRow(ctx, a.AgentName(), res.Version, versionErr(res))
		}
		s.mu.Lock()
		c.running = false
		if status != "ok" && status != "warning" {
			c.retry = s.Now().Add(selfCheckRetry)
		}
		s.mu.Unlock()
		if status != "error" && status != "checking" {
			s.Wake(context.WithoutCancel(ctx))
		}
	})
	if !started {
		s.mu.Lock()
		c.running = false
		s.mu.Unlock()
	}
	return started
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
