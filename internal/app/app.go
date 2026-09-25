package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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
	// Now is the deck clock; every deadline is written and checked by it.
	Now func() time.Time
	// Ceiling is how long a command agent may work unless its manifest says otherwise.
	Ceiling time.Duration
	// SilentAfter is how long a running process may go without output before it shows as silent.
	SilentAfter time.Duration
	// CatchUpSpread is the gap between runs missed during a sleep that start one after another.
	CatchUpSpread time.Duration
	// EmptyKept is how long a process with an empty result stays in the history.
	EmptyKept time.Duration
}

func (c *Config) defaults() {
	if c.Now == nil {
		c.Now = time.Now
	}
	if c.Ceiling <= 0 {
		c.Ceiling = time.Hour
	}
	if c.SilentAfter <= 0 {
		c.SilentAfter = 30 * time.Minute
	}
	if c.CatchUpSpread <= 0 {
		c.CatchUpSpread = time.Minute
	}
	if c.EmptyKept <= 0 {
		c.EmptyKept = 24 * time.Hour
	}
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
	cfg.defaults()
	if err := os.MkdirAll(filepath.Join(cfg.Home, "runs"), 0o700); err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(cfg.Home, "deck.db"))
	if err != nil {
		return nil, err
	}
	a := &App{Cfg: cfg, Store: st, Bus: events.NewBus()}
	a.Asks = ask.NewBroker(st, a.Bus, cfg.Now)
	a.Registry = registry.New(a.projectCommands)
	a.addBuiltins()
	a.Gateway = gateway.New(gateway.Config{Now: cfg.Now, QuestionWait: cfg.QuestionWait, ConfirmWait: cfg.ConfirmWait, ConfirmTTL: cfg.ConfirmTTL},
		st, a.Asks, a.Bus, a.Registry)
	a.Runner = process.NewRunner(st, a.Gateway, a.Bus, p,
		process.Config{RunsDir: filepath.Join(cfg.Home, "runs"), MCPBase: cfg.MCPBase, Now: cfg.Now, Ceiling: cfg.Ceiling}, a)
	a.Runner.OnDone = a.afterTriggers
	a.Scheduler = trigger.New(st, a, cfg.Now, cfg.CatchUpSpread)
	a.Asks.OnLateAnswer(a.lateAnswer)
	return a, nil
}

// Close interrupts running providers; their processes stay running and resume on the next start.
func (a *App) Close() error {
	a.Runner.Shutdown()
	return a.Store.Close()
}

// Fire starts the command of a trigger through the registry. Scheduled runs carry the trigger as their
// retry key, so a run does not start while the previous one is still going.
func (a *App) Fire(ctx context.Context, t db.TriggerState, input map[string]any) (int64, error) {
	if t.Kind == trigger.KindCron || t.Kind == trigger.KindOnce {
		input[manifest.RetryKey] = fmt.Sprintf("trigger:%d", t.ID)
	}
	return a.fire(ctx, t, input, 0)
}

func (a *App) fire(ctx context.Context, t db.TriggerState, input map[string]any, parent int64) (int64, error) {
	p, err := a.Store.Q.GetProject(ctx, t.ProjectID)
	if err != nil {
		return 0, err
	}
	raw, _ := json.Marshal(input)
	act := registry.Actor{Kind: registry.Trigger, Workspace: p.WorkspaceID, ProjectID: p.ID, ProcessID: parent, TriggerID: t.ID}
	out, err := a.Registry.Invoke(ctx, act, p.Name+"."+t.Command, raw)
	if err != nil {
		return 0, err
	}
	id := out.(Started).Process
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.SetTriggerLast(ctx, db.SetTriggerLastParams{LastProcessID: sql.NullInt64{Int64: id, Valid: true}, ID: t.ID})
	})
	return id, err
}

// afterTriggers starts the commands that run when a command of the same project finishes.
func (a *App) afterTriggers(ctx context.Context, done db.Process) {
	p, err := a.Store.Q.GetProject(ctx, done.ProjectID)
	if err != nil || p.Paused == 1 {
		return
	}
	m, err := loadManifest(ctx, a.Store.Q, p)
	if err != nil {
		slog.Error("after triggers", "project", p.Name, "err", err)
		return
	}
	rows, err := a.Store.Q.ListProjectTriggers(ctx, p.ID)
	if err != nil {
		return
	}
	for _, t := range m.Triggers {
		if t.After != done.Command {
			continue
		}
		row, ok := trigger.Find(rows, t)
		if !ok || row.Paused == 1 {
			continue
		}
		if _, err := a.fire(ctx, row, manifest.TriggerInput(t, done.ID), done.ID); err != nil {
			slog.Error("after trigger", "project", p.Name, "command", t.Command, "after", done.ID, "err", err)
		}
	}
}

// lateAnswer delivers an answer nobody was waiting for live; Deliver ignores processes that are not parked.
func (a *App) lateAnswer(ctx context.Context, q db.Ask) {
	if err := a.Runner.Deliver(ctx, q.ProcessID); err != nil {
		slog.Error("deliver answer", "process", q.ProcessID, "err", err)
	}
}

// Sweep checks every deadline stored in the base against now: deleted cards, expired confirmations,
// ceilings, silent processes and empty results kept long enough.
func (a *App) Sweep(ctx context.Context, now time.Time) {
	a.purgeCards(ctx, now)
	a.expireConfirms(ctx, now)
	a.stopAtCeiling(ctx, now)
	a.markSilent(ctx, now)
	a.purgeEmpty(ctx, now)
}

func (a *App) expireConfirms(ctx context.Context, now time.Time) {
	var expired []db.Ask
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		expired, err = q.ExpireStaleConfirms(ctx, store.Time(now))
		return err
	})
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

func (a *App) stopAtCeiling(ctx context.Context, now time.Time) {
	ids, err := a.Store.Q.ListCeilingReached(ctx, now.UnixMilli())
	if err != nil {
		slog.Error("ceilings", "err", err)
		return
	}
	for _, id := range ids {
		if err := a.Runner.Stop(ctx, id, "deck: the process reached its time ceiling"); err != nil {
			slog.Error("stop at ceiling", "process", id, "err", err)
		}
	}
}

func (a *App) markSilent(ctx context.Context, now time.Time) {
	var ids []int64
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		ids, err = q.MarkSilentProcesses(ctx, now.Add(-a.Cfg.SilentAfter).UnixMilli())
		return err
	})
	if err != nil {
		slog.Error("silent processes", "err", err)
	}
	if len(ids) > 0 {
		a.Bus.Publish("processes")
	}
}

func (a *App) purgeEmpty(ctx context.Context, now time.Time) {
	var ids []int64
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		ids, err = q.PurgeEmptyProcesses(ctx, store.Time(now.Add(-a.Cfg.EmptyKept)))
		return err
	})
	if err != nil {
		slog.Error("purge empty processes", "err", err)
		return
	}
	for _, id := range ids {
		_ = os.RemoveAll(filepath.Join(a.Cfg.Home, "runs", fmt.Sprint(id)))
	}
	if len(ids) > 0 {
		a.Bus.Publish("processes")
	}
}

// Loop runs the scheduler and the sweep by the deck clock until ctx ends; after a sleep of the machine
// the first tick finds every deadline that passed.
func (a *App) Loop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		a.Scheduler.Tick(ctx)
		a.Sweep(ctx, a.Cfg.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
