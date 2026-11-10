package providers

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"deck2/internal/fault"
	"deck2/internal/health"
	"deck2/internal/models"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/registry"
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
// while it is red; a model of a CLI spends turns of the subscription, so it is checked on a button and a broken turn.
func (s *Service) Checks(ctx context.Context, now time.Time) {
	s.mu.Lock()
	found := s.found
	s.found = true
	s.mu.Unlock()
	if !found {
		s.Go(func(ctx context.Context) {
			for _, name := range acp.Names() {
				s.discover(ctx, name)
			}
		})
	}
	for _, pv := range s.Builtin {
		if _, ok := pv.(provider.SelfChecker); ok && s.resting(ctx, pv) {
			_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
				return health.Clear(ctx, q, health.Of{}, health.Provider(pv.Name()))
			})
		}
	}
	// A model of an ACP agent not on this machine is not installed rather than red: its checks before go.
	spaces, _ := s.St.Q.ListWorkspaces(ctx)
	for _, ws := range spaces {
		rows, _ := s.St.Q.ListModels(ctx, ws)
		for _, r := range rows {
			if r.Model.Agent != "" && r.Connection.Api != models.APICLI && !s.Installed(r.Model.Agent) {
				s.clearCheck(ctx, r.Model.Name)
			}
		}
	}
	for _, pv := range s.checked(ctx) {
		if s.idle(ctx, pv.Name()) {
			s.unstick(ctx, pv.Name())
			continue
		}
		if s.onCLI(ctx, pv.Name()) {
			continue
		}
		s.mu.Lock()
		c := s.checksOf(pv.Name())
		due := !c.started || !c.retry.IsZero() && !now.Before(c.retry)
		s.mu.Unlock()
		if due {
			s.startCheck(pv.Name(), false)
		}
	}
}

// uninstalled says a provider is a model of an ACP agent not on this machine, and takes away its checks before.
func (s *Service) uninstalled(ctx context.Context, pv provider.Provider) bool {
	a, ok := pv.(interface{ AgentName() string })
	if !ok || s.Installed(a.AgentName()) {
		return false
	}
	s.clearCheck(ctx, pv.Name())
	return true
}

func (s *Service) clearCheck(ctx context.Context, name string) {
	_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return health.Clear(ctx, q, health.Of{}, health.Provider(name))
	})
}

// Broke checks a provider whose turn failed or lost its login, unless it was checked within selfCheckRetry; a red
// one or one without a login is then checked once that time is over.
func (s *Service) Broke(ctx context.Context, name string) {
	h, err := health.Get(ctx, s.St.Q, health.Of{}, health.Provider(name))
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
	if !ok || s.awayAnywhere(s.Ctx, name) {
		return false
	}
	if s.uninstalled(s.Ctx, sc.(provider.Provider)) {
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
	subject := health.Provider(name)
	started := s.Go(func(ctx context.Context) {
		h, _ := health.Get(ctx, s.St.Q, health.Of{}, subject)
		if force || h.Status == health.Error || h.Status == health.Checking {
			s.writeCheck(ctx, name, health.Checking, "the self-check is running", "", "", 0)
		}
		ceiling := s.SelfCheckCeiling
		if c, ok := sc.(interface{ CheckCeiling() time.Duration }); ok && c.CheckCeiling() > 0 {
			ceiling = c.CheckCeiling()
		}
		// The ceiling counts from when the agent is ready and again from the first answer of its model; a start
		// slowed by a busy machine has agentStart.
		cctx, cancel := context.WithCancel(ctx)
		timer := time.AfterFunc(max(ceiling, agentStart), cancel)
		var ready, answered sync.Once
		cctx = provider.WithMarks(cctx, provider.Marks{Ready: func() { ready.Do(func() { timer.Reset(ceiling) }) },
			Answered: func() { answered.Do(func() { timer.Reset(ceiling) }) }})
		var res provider.Check
		unreached := s.Reach(cctx)
		if unreached == nil {
			res = sc.SelfCheck(cctx)
		}
		timer.Stop()
		cancel()
		of := cmp.Or(res.Version, "an unknown version")
		status, text, verdict := health.OK, "", ""
		switch {
		case ctx.Err() != nil:
			status, text = health.Checking, "deck stopped during the self-check"
		case unreached != nil:
			status, text, verdict = health.Error, fmt.Sprintf("agents do not reach deck's MCP server, so they would start without deck's tools: %s", unreached), "adapter"
		case res.Held == fault.ModelLimit:
			status, text = health.Paused, fmt.Sprintf("the self-check hit the usage limit; %s", errText(res.Err))
		case res.Held == fault.ModelLogin:
			status, text = health.NeedsLogin, fmt.Sprintf("the self-check found no valid login; %s", errText(res.Err))
		case res.Err != nil && res.Slow:
			status, text, verdict = health.Warning, fmt.Sprintf("the model did not finish the self-check of %s within %s: %s", of, ceiling, res.Err), "model"
		case res.Err != nil && res.ModelFailed:
			status, text, verdict = health.Error, fmt.Sprintf("the model does not manage the self-check of %s: %s; pick a stronger model", of, res.Err), "model"
		case res.Err != nil:
			status, text, verdict = health.Error, fmt.Sprintf("self-check of %s failed: %s", of, res.Err), "adapter"
		}
		if t, ok := sc.(provider.Tested); ok && status == health.OK && provider.Untested(res.Version, t.TestedVersion()) {
			status, text = health.Warning, fmt.Sprintf("deck is tested with version %s of this CLI, here runs %s: it works, but may differ", t.TestedVersion(), res.Version)
		}
		if res.Speed > 0 {
			if text == "" {
				text = fmt.Sprintf("the model answered at %.0f tokens/s", res.Speed)
			} else {
				text = fmt.Sprintf("%s; the model answered at %.0f tokens/s", text, res.Speed)
			}
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

// agentStart is how long an agent of a self-check may take to start before the ceiling of its model counts.
const agentStart = time.Minute

func (s *Service) writeCheck(ctx context.Context, name, status, text, version, verdict string, speed float64) {
	subject := health.Provider(name)
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if err := process.ProviderHealth(ctx, q, s.Now(), name, status, text); err != nil {
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

// idle says a provider is a model no assignment names, which deck does not check by itself.
func (s *Service) idle(ctx context.Context, name string) bool {
	spaces, _ := s.St.Q.ListWorkspaces(ctx)
	for _, ws := range spaces {
		if _, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: ws, Name: name}); err == nil {
			used, err := assigned(ctx, s.St.Q, ws, name)
			return err == nil && !used
		}
	}
	return false
}

// unstick clears the health of a check a stop cut short, which no check of an idle model ends.
func (s *Service) unstick(ctx context.Context, name string) {
	s.mu.Lock()
	running := s.checksOf(name).running
	s.mu.Unlock()
	if h, err := health.Get(ctx, s.St.Q, health.Of{}, health.Provider(name)); err == nil && h.Status == health.Checking && !running {
		s.clearCheck(ctx, name)
	}
}

// onCLI reports whether a provider is a model of an agent CLI on the owner's subscription, on a node too.
func (s *Service) onCLI(ctx context.Context, name string) bool {
	if base, _, ok := provider.AtNode(name); ok {
		name = base
	}
	spaces, _ := s.St.Q.ListWorkspaces(ctx)
	for _, ws := range spaces {
		if row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: ws, Name: name}); err == nil {
			return row.Connection.Api == models.APICLI
		}
	}
	return false
}

type providerCheckIn struct {
	Provider string `desc:"Provider name"`
}

func (s *Service) providerCheck(ctx context.Context, act registry.Actor, in providerCheckIn) (any, error) {
	pv, err := s.Provider(ctx, act.Workspace, in.Provider)
	if err != nil {
		return nil, err
	}
	if _, ok := pv.(provider.SelfChecker); !ok {
		return nil, registry.Invalid("provider %q has no self-check", in.Provider)
	}
	if !s.startCheck(in.Provider, true) {
		if s.Ctx.Err() != nil {
			return nil, errors.New("deck: deck is stopping")
		}
		return nil, errors.New("deck: a self-check is already running")
	}
	return map[string]any{"checking": in.Provider}, nil
}

func (s *Service) checkCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("provider_check", "Check provider",
			"Runs the self-check of a provider's adapter again; its health shows the outcome.",
			registry.OwnerOnly, s.providerCheck),
	}
}

// Check runs the self-check of a provider again, as the owner asked for it.
func (s *Service) Check(name string) { s.startCheck(name, true) }
