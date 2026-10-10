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
	// broke is set when a turn turned the provider red during a running check, whose green is then stale.
	broke bool
	last  time.Time
	retry time.Time
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

// Broke checks a provider whose turn failed or lost its login, unless it was checked within selfCheckRetry; a red
// one or one without a login is then checked once that time is over.
func (s *Service) Broke(ctx context.Context, name string) {
	h, err := s.St.Q.GetHealth(ctx, process.HealthSubject(name))
	red := err == nil && (h.Status == health.Error || h.Status == health.NeedsLogin)
	s.mu.Lock()
	c := s.checksOf(name)
	recent := !c.last.IsZero() && s.Now().Before(c.last.Add(selfCheckRetry))
	switch {
	case red && c.running:
		c.broke = true
	case recent && red && c.retry.IsZero():
		c.retry = c.last.Add(selfCheckRetry)
	}
	s.mu.Unlock()
	if !recent {
		s.startCheck(name, false)
	}
}

// Recheck runs the self-check of a provider at a time, unless one is due sooner.
func (s *Service) Recheck(name string, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c := s.checksOf(name); c.retry.IsZero() || at.Before(c.retry) {
		c.retry = at
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
	c.running, c.started, c.last, c.retry, c.broke = true, true, s.Now(), time.Time{}, false
	s.mu.Unlock()
	ctx := s.Ctx
	subject := process.HealthSubject(name)
	started := s.Go(func() {
		h, _ := s.St.Q.GetHealth(ctx, subject)
		if force || h.Status == health.Error || h.Status == health.Checking {
			s.writeCheck(ctx, name, health.Checking, "the self-check is running", "", "", 0)
		}
		ceiling := s.SelfCheckCeiling
		if c, ok := sc.(interface{ CheckCeiling() time.Duration }); ok && c.CheckCeiling() > 0 {
			ceiling = c.CheckCeiling()
		}
		cctx, cancel := context.WithTimeout(ctx, ceiling)
		var res provider.Check
		unreached := s.Reach(cctx)
		if unreached == nil {
			res = sc.SelfCheck(cctx)
		}
		cancel()
		of := cmp.Or(res.Version, "an unknown version")
		status, text, verdict := health.OK, "", ""
		switch {
		case ctx.Err() != nil:
			status, text = health.Checking, "deck stopped during the self-check"
		case unreached != nil:
			status, text, verdict = health.Error, "agents do not reach deck's MCP server, so they would start without deck's tools: "+unreached.Error(), "adapter"
		case res.RateLimited:
			status, text = health.Paused, "the self-check hit the usage limit; "+errText(res.Err)
		case res.NeedsLogin:
			status, text = health.NeedsLogin, "the self-check found no valid login; "+errText(res.Err)
		case res.Err != nil && res.Slow:
			status, text, verdict = health.Warning, fmt.Sprintf("the model did not finish the self-check of %s within %s: %s", of, ceiling, res.Err), "model"
		case res.Err != nil && res.ModelFailed:
			status, text, verdict = health.Error, fmt.Sprintf("the model does not manage the self-check of %s: %s; pick a stronger model", of, res.Err), "model"
		case res.Err != nil:
			status, text, verdict = health.Error, fmt.Sprintf("self-check of %s failed: %s", of, res.Err), "adapter"
		}
		if res.Speed > 0 {
			text = strings.TrimPrefix(text+fmt.Sprintf("; the model answered at %.0f tokens/s", res.Speed), "; ")
		}
		version := res.Version
		if status != health.OK && status != health.Error && status != health.Warning {
			version = ""
		}
		s.mu.Lock()
		again := c.again && ctx.Err() == nil
		stale := c.broke && ctx.Err() == nil && (status == health.OK || status == health.Warning)
		c.again = false
		if again || stale {
			c.running = false
			s.mu.Unlock()
			s.startCheck(name, again)
			return
		}
		s.mu.Unlock()
		s.writeCheck(context.WithoutCancel(ctx), name, status, text, version, verdict, res.Speed)
		if a, ok := sc.(interface{ AgentName() string }); ok && ctx.Err() == nil {
			s.agentRow(ctx, a.AgentName(), res.Version, versionErr(res))
		}
		s.mu.Lock()
		c.running = false
		if status != health.OK && status != health.Warning {
			c.retry = s.Now().Add(selfCheckRetry)
		}
		s.mu.Unlock()
		if status != health.Error && status != health.Checking {
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

func (s *Service) writeCheck(ctx context.Context, name, status, text, version, verdict string, speed float64) {
	subject := process.HealthSubject(name)
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := process.ProviderHealth(ctx, q, name, status, text); err != nil {
			return err
		}
		if status == health.Checking {
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
