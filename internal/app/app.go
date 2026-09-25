package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"deck2/internal/ask"
	"deck2/internal/events"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/provider"
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
	bus := events.NewBus()
	asks := ask.NewBroker(st, bus)
	gw := gateway.New(gateway.Config{QuestionWait: cfg.QuestionWait, ConfirmWait: cfg.ConfirmWait}, st, asks, bus)
	runner := process.NewRunner(st, gw, bus, p, filepath.Join(cfg.Home, "runs"), cfg.MCPBase)
	a := &App{Cfg: cfg, Store: st, Bus: bus, Asks: asks, Gateway: gw, Runner: runner, Scheduler: trigger.New(st, runner)}
	asks.OnLateAnswer(a.lateAnswer)
	return a, nil
}

// Close interrupts running providers; their processes stay running and resume on the next start.
func (a *App) Close() error {
	a.Runner.Shutdown()
	return a.Store.Close()
}

// CreateProject validates the manifest and registers its triggers.
func (a *App) CreateProject(ctx context.Context, name string, raw []byte) (db.Project, error) {
	m, err := manifest.Parse(raw)
	if err != nil {
		return db.Project{}, err
	}
	p, err := a.Store.Q.CreateProject(ctx, db.CreateProjectParams{Name: name, Manifest: string(raw)})
	if err != nil {
		return db.Project{}, err
	}
	if err := a.Scheduler.Sync(ctx, p.ID, m); err != nil {
		return db.Project{}, err
	}
	a.Bus.Publish("projects")
	return p, nil
}

func (a *App) Manifest(ctx context.Context, projectID int64) (db.Project, *manifest.Manifest, error) {
	p, err := a.Store.Q.GetProject(ctx, projectID)
	if err != nil {
		return p, nil, err
	}
	m, err := manifest.Parse([]byte(p.Manifest))
	return p, m, err
}

// lateAnswer delivers an answer nobody was waiting for live; Deliver ignores processes that are not parked.
func (a *App) lateAnswer(ctx context.Context, q db.Ask) {
	if err := a.Runner.Deliver(ctx, q.ProcessID); err != nil {
		slog.Error("deliver answer", "process", q.ProcessID, "err", err)
	}
}

// Sweep expires confirmations older than ConfirmTTL and tells the waiting processes.
func (a *App) Sweep(ctx context.Context, now time.Time) {
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
