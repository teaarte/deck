package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"deck2/internal/ask"
	"deck2/internal/events"
	"deck2/internal/gateway"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/trigger"
)

type Config struct {
	Home         string
	MCPBase      string
	QuestionWait time.Duration
	ConfirmWait  time.Duration
	ConfirmTTL   time.Duration
}

type App struct {
	Cfg       Config
	Store     *store.Store
	Bus       *events.Bus
	Asks      *ask.Broker
	Registry  *registry.Registry
	Gateway   *gateway.Gateway
	Runner    *process.Runner
	Scheduler *trigger.Scheduler
}

func New(cfg Config, p provider.Provider) (*App, error) {
	if err := os.MkdirAll(filepath.Join(cfg.Home, "runs"), 0o700); err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(cfg.Home, "deck.db"))
	if err != nil {
		return nil, err
	}
	a := &App{Cfg: cfg, Store: st, Bus: events.NewBus()}
	a.Asks = ask.NewBroker(st, a.Bus)
	a.Registry = registry.New(a.projectCommands)
	a.addBuiltins()
	a.Gateway = gateway.New(gateway.Config{QuestionWait: cfg.QuestionWait, ConfirmWait: cfg.ConfirmWait}, st, a.Asks, a.Bus, a.Registry)
	a.Runner = process.NewRunner(st, a.Gateway, a.Bus, p, filepath.Join(cfg.Home, "runs"), cfg.MCPBase, a)
	a.Scheduler = trigger.New(st, a)
	a.Asks.OnLateAnswer(a.lateAnswer)
	return a, nil
}

// Close interrupts running providers; their processes stay running and resume on the next start.
func (a *App) Close() error {
	a.Runner.Shutdown()
	return a.Store.Close()
}

// Start runs a project command on behalf of a trigger, through the registry.
func (a *App) Start(ctx context.Context, projectID int64, command string, input map[string]any) (int64, error) {
	p, err := a.Store.Q.GetProject(ctx, projectID)
	if err != nil {
		return 0, err
	}
	raw, _ := json.Marshal(input)
	act := registry.Actor{Kind: registry.Trigger, Workspace: p.WorkspaceID, ProjectID: projectID}
	out, err := a.Registry.Invoke(ctx, act, p.Name+"."+command, raw)
	if err != nil {
		return 0, err
	}
	return out.(Started).Process, nil
}

// lateAnswer delivers an answer nobody was waiting for live; Deliver ignores processes that are not parked.
func (a *App) lateAnswer(ctx context.Context, q db.Ask) {
	if err := a.Runner.Deliver(ctx, q.ProcessID); err != nil {
		slog.Error("deliver answer", "process", q.ProcessID, "err", err)
	}
}

// Sweep removes cards deleted long enough ago, expires confirmations older than ConfirmTTL and tells the
// waiting processes.
func (a *App) Sweep(ctx context.Context, now time.Time) {
	a.purgeCards(ctx, now)
	if a.Cfg.ConfirmTTL <= 0 {
		return
	}
	expired, err := a.Store.Q.ExpireStaleConfirms(ctx, now.Add(-a.Cfg.ConfirmTTL).UTC().Format("2006-01-02T15:04:05.000Z"))
	if err != nil {
		slog.Error("expire confirmations", "err", err)
		return
	}
	seen := map[int64]bool{}
	for _, q := range expired {
		if seen[q.ProcessID] {
			continue
		}
		seen[q.ProcessID] = true
		a.lateAnswer(ctx, q)
	}
	if len(expired) > 0 {
		a.Bus.Publish("asks")
	}
}

// Loop runs the scheduler and the confirmation sweep until ctx ends.
func (a *App) Loop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		a.Scheduler.Tick(ctx)
		a.Sweep(ctx, time.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
