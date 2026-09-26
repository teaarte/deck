package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"deck2/internal/ask"
	"deck2/internal/events"
	"deck2/internal/files"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/mcphost"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
	"deck2/internal/script"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/tools"
	"deck2/internal/trigger"
)

type App struct {
	Cfg       Config
	Store     *store.Store
	Bus       *events.Bus
	Asks      *ask.Broker
	Registry  *registry.Registry
	Gateway   *gateway.Gateway
	Runner    *process.Runner
	Scheduler *trigger.Scheduler
	Scripts   *script.Exec
	Tools     *tools.HTTP
	MCP       *tools.MCP
	Browser   *tools.Browser
	Host      *mcphost.Host

	// main is the provider deck was started with; the owner adds OpenAI-compatible ones per workspace.
	main     provider.Provider
	checkMu  sync.Mutex
	check    checks
	ctx      context.Context
	cancel   context.CancelFunc
	secretMu sync.Mutex
	bg       sync.WaitGroup
	disk     time.Time
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
	a := &App{Cfg: cfg, Store: st, Bus: events.NewBus(), Scripts: script.New(cfg.Sandbox, cfg.Home), main: p}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	a.Asks = ask.NewBroker(st, a.Bus, cfg.Now)
	a.Registry = registry.New(a.projectCommands)
	a.addBuiltins()
	a.Gateway = gateway.New(gateway.Config{Now: cfg.Now, QuestionWait: cfg.QuestionWait, ConfirmWait: cfg.ConfirmWait, ConfirmTTL: cfg.ConfirmTTL,
		Projects: a}, st, a.Asks, a.Bus, a.Registry)
	a.Tools = tools.NewHTTP(a.Gateway, a.Secret, a.dirs())
	a.Tools.OAuth = &tools.OAuth{Secrets: a.Secret, Swap: a.SwapSecret}
	a.Tools.Health, a.Tools.SignIn = a.setHealth, a.toolSignIn
	a.Tools.Resolve = func(ctx context.Context, project, process int64, ref string) (files.Ref, error) {
		return a.dirs().Resolve(ctx, a.Store.Q, project, process, ref)
	}
	a.Host = mcphost.New(a.serverStatus)
	a.Host.Now, a.Host.Sandbox = cfg.Now, cfg.Sandbox
	if home, err := os.UserHomeDir(); err == nil {
		a.Host.Hide = append([]string{cfg.Home}, sandbox.Private(home)...)
	}
	a.MCP = &tools.MCP{GW: a.Gateway, Host: a.Host, Store: st, Secrets: a.Secret, Dirs: a.dirs(), Home: cfg.Home,
		Manifests: func(ctx context.Context, p db.Project) (*manifest.Manifest, error) { return loadManifest(ctx, st.Q, p) }}
	a.Browser = &tools.Browser{GW: a.Gateway, Host: a.Host, Dirs: a.dirs(), Home: cfg.Home, Argv: cfg.Browser, Manifests: a.Manifest}
	a.Browser.Load()
	a.Registry.SetExtra(a.Browser.Commands)
	a.Gateway.SetAutomation(a.Automation)
	a.Runner = process.NewRunner(st, a.Gateway, a.Bus, a, a.Scripts,
		process.Config{RunsDir: filepath.Join(cfg.Home, "runs"), MCPBase: cfg.MCPBase, Now: cfg.Now, Ceiling: cfg.Ceiling,
			ScriptCeiling: cfg.ScriptCeiling, FilesDir: filepath.Join(cfg.Home, "files"),
			Hidden: hidden(cfg.Home), PauseLimit: cfg.PauseLimit, PauseProbe: cfg.PauseProbe}, a)
	a.Runner.OnDone = a.afterTriggers
	a.Scheduler = trigger.New(st, a, cfg.Now, cfg.CatchUpSpread)
	a.Asks.OnLateAnswer(a.lateAnswer)
	if err := a.SeedSkills(a.ctx); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *App) dirs() files.Dirs {
	return files.Dirs{Files: filepath.Join(a.Cfg.Home, "files"), Runs: filepath.Join(a.Cfg.Home, "runs")}
}

// hidden are what an agent may not read or write under every path of home, since macOS links /tmp and /var:
// everything of deck but its own run and the data of its project, including runs and projects created later.
func hidden(home string) func(project, process int64) []string {
	homes := []string{home}
	if real, err := filepath.EvalSymlinks(home); err == nil && real != home {
		homes = append(homes, real)
	}
	return func(project, process int64) []string {
		var out []string
		for _, h := range homes {
			out = append(out, filepath.Join(h, "deck.db")+"*", filepath.Join(h, "token"), filepath.Join(h, "mcp", "*.json"),
				filepath.Join(h, "mcp", "*.tmp"), filepath.Join(h, "mcp", "stderr-*"))
			for _, d := range []string{"secrets", "sessions", "files", "backups", "browser"} {
				out = append(out, filepath.Join(h, d)+"/**")
			}
			out = append(out, others(filepath.Join(h, "runs"), fmt.Sprint(process))...)
			out = append(out, others(filepath.Join(h, "projects"), fmt.Sprint(project))...)
			own := filepath.Join(h, "projects", fmt.Sprint(project))
			names := []string{"env", "mcp"}
			entries, _ := os.ReadDir(own)
			for _, e := range entries {
				if e.Name() != "data" && !slices.Contains(names, e.Name()) {
					names = append(names, e.Name())
				}
			}
			for _, n := range names {
				out = append(out, filepath.Join(own, n)+"/**")
			}
		}
		return out
	}
}

// others are patterns for every folder of dir with a decimal name other than own: other lengths, then each
// other digit at each position.
func others(dir, own string) []string {
	out := []string{filepath.Join(dir, strings.Repeat("?", len(own)+1)+"*") + "/**"}
	for n := 1; n < len(own); n++ {
		out = append(out, filepath.Join(dir, strings.Repeat("?", n))+"/**")
	}
	for i := range own {
		for c := '0'; c <= '9'; c++ {
			if byte(c) != own[i] {
				out = append(out, filepath.Join(dir, own[:i]+string(c)+strings.Repeat("?", len(own)-i-1))+"/**")
			}
		}
	}
	return out
}

// Close interrupts running providers; their processes stay running and resume on the next start.
func (a *App) Close() error {
	a.cancel()
	a.bg.Wait()
	a.Runner.Shutdown()
	a.Host.Close()
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
	out, err := a.Gateway.CallAs(ctx, act, p.Name+"."+t.Command, raw)
	if err != nil {
		return 0, err
	}
	started, ok := out.(Started)
	if !ok {
		return 0, nil
	}
	id := started.Process
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
	if !q.ProcessID.Valid {
		return
	}
	if err := a.Runner.Deliver(ctx, q.ProcessID.Int64); err != nil {
		slog.Error("deliver answer", "process", q.ProcessID.Int64, "err", err)
	}
}

// Sweep checks every deadline against now: deleted cards, expired confirmations, ceilings, pauses on a usage
// limit, silent processes, empty results kept long enough, the disk, and the daily cleanup and snapshot.
func (a *App) Sweep(ctx context.Context, now time.Time) {
	a.purgeCards(ctx, now)
	a.expireConfirms(ctx, now)
	a.stopAtCeiling(ctx, now)
	a.Runner.Unpause(ctx, now)
	a.markSilent(ctx, now)
	a.purgeEmpty(ctx, now)
	a.checkDisk(ctx, now)
	a.daily(ctx, now)
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
		if q.Kind == ask.KindCall {
			a.Gateway.Record(ctx, q, "expired", "the owner did not answer in time")
		}
		if seen[q.ProcessID.Int64] {
			continue
		}
		seen[q.ProcessID.Int64] = true
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

// Loop runs the scheduler, the sweep and the self-checks by the deck clock until ctx ends; after a sleep of
// the machine the first tick finds every deadline that passed.
func (a *App) Loop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		a.Scheduler.Tick(ctx)
		a.Sweep(ctx, a.Cfg.Now())
		a.Checks(ctx, a.Cfg.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
