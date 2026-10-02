package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"deck2/internal/ask"
	"deck2/internal/docs"
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

	// builtin are the providers deck was started with, the main one first; the owner adds OpenAI-compatible
	// ones per workspace.
	builtin  []provider.Provider
	checkMu  sync.Mutex
	check    map[string]*checks
	ctx      context.Context
	cancel   context.CancelFunc
	secretMu sync.Mutex
	bg       sync.WaitGroup
	disk     time.Time
	boardMu  sync.Mutex
	// filesMu keeps stored files from going while a snapshot copies the base and files/.
	filesMu sync.RWMutex
	// cleaned and snapTried are when the daily cleanup ran and a daily snapshot was last tried.
	cleaned, snapTried time.Time
	chatMu             sync.Mutex
	chat               chatQueue
	// chatStart keeps a reply starting apart from a topic being deleted.
	chatStart sync.Mutex
	// pushMu lets one look for what to push run at a time.
	pushMu sync.Mutex
}

// New starts deck with its main provider p and further built-in ones.
func New(cfg Config, p provider.Provider, more ...provider.Provider) (*App, error) {
	cfg.defaults()
	if err := os.MkdirAll(filepath.Join(cfg.Home, "runs"), 0o700); err != nil {
		return nil, err
	}
	st, err := store.Open(filepath.Join(cfg.Home, "deck.db"))
	if err != nil {
		return nil, err
	}
	a := &App{Cfg: cfg, Store: st, Bus: events.NewBus(), Scripts: script.New(cfg.Sandbox, cfg.Home),
		builtin: append([]provider.Provider{p}, more...), check: map[string]*checks{}}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	a.Asks = ask.NewBroker(st, a.Bus, cfg.Now)
	a.Registry = registry.New(a.projectCommands)
	a.addBuiltins()
	a.Gateway = gateway.New(gateway.Config{Now: cfg.Now, QuestionWait: cfg.QuestionWait, ConfirmWait: cfg.ConfirmWait, ConfirmTTL: a.confirmTTL}, st, a.Asks, a.Bus, a.Registry)
	a.Tools = tools.NewHTTP(a.Gateway, a.Secret, a.dirs())
	a.Tools.OAuth = &tools.OAuth{Secrets: a.Secret, Swap: a.SwapSecret}
	a.Tools.Health, a.Tools.SignIn = a.toolHealth, a.toolSignIn
	a.Tools.Resolve = func(ctx context.Context, project, process int64, ref string) (files.Ref, error) {
		return a.dirs().Resolve(ctx, a.Store.Q, project, process, ref)
	}
	a.Host = mcphost.New(a.serverStatus)
	a.Host.Now, a.Host.Sandbox = cfg.Now, cfg.Sandbox
	a.Host.Hide = sandbox.Hidden(cfg.Home)
	a.MCP = &tools.MCP{GW: a.Gateway, Host: a.Host, Store: st, Secrets: a.Secret, Dirs: a.dirs(), Home: cfg.Home}
	a.Browser = &tools.Browser{GW: a.Gateway, Host: a.Host, Store: st, Dirs: a.dirs(), Home: cfg.Home, Argv: cfg.Browser}
	a.Browser.Load()
	a.Registry.SetExtra(a.Browser.Commands)
	a.Gateway.SetAutomation(a.Automation)
	a.Runner = process.NewRunner(st, a.Gateway, a.Bus, a, a.Scripts,
		process.Config{RunsDir: filepath.Join(cfg.Home, "runs"), MCPBase: cfg.MCPBase, Now: cfg.Now, FilesDir: filepath.Join(cfg.Home, "files"),
			Hidden: hidden(cfg.Home), PauseLimit: cfg.PauseLimit, AgentSlots: a.agentSlots}, a)
	a.Runner.Hooks = []process.Hook{chatHook{a: a}, errorsHook{a: a}, afterHook{a: a}}
	a.Scheduler = trigger.New(st, a, cfg.Now)
	a.Asks.OnLateAnswer(a.lateAnswer)
	if err := a.SeedSkills(a.ctx); err != nil {
		return nil, err
	}
	if err := a.SeedMaster(a.ctx); err != nil {
		return nil, err
	}
	a.watchPush()
	return a, nil
}

func (a *App) dirs() files.Dirs {
	return files.Dirs{Files: filepath.Join(a.Cfg.Home, "files"), Runs: filepath.Join(a.Cfg.Home, "runs")}
}

// hidden are what an agent may not read or write under every path of home, since macOS links /tmp and /var:
// all of deck but its own run and project data, later ones too; the adapters add the owner's credential stores.
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
			dirs := []string{"secrets", "sessions", "files", "backups", "browser", "tailscale", "codex", "acp"}
			top, _ := os.ReadDir(h)
			for _, e := range top {
				if e.IsDir() && !slices.Contains(append(dirs, "runs", "projects", "mcp"), e.Name()) {
					dirs = append(dirs, e.Name())
				}
			}
			for _, d := range dirs {
				out = append(out, filepath.Join(h, d)+"/**")
			}
			out = append(out, others(filepath.Join(h, "runs"), fmt.Sprint(process))...)
			out = append(out, others(filepath.Join(h, "projects"), fmt.Sprint(project))...)
			own := filepath.Join(h, "projects", fmt.Sprint(project))
			names := []string{"mcp"}
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

// others are patterns for every folder of dir with a decimal name other than own.
func others(dir, own string) []string {
	var out []string
	for _, n := range provider.Others("", own, "") {
		out = append(out, filepath.Join(dir, n)+"/**")
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
func (a *App) Fire(ctx context.Context, t db.Trigger, input map[string]any) (int64, error) {
	if t.Kind == trigger.KindCron || t.Kind == trigger.KindOnce {
		input[manifest.RetryKey] = fmt.Sprintf("trigger:%d", t.ID)
	}
	return a.fire(ctx, t, input, 0)
}

func (a *App) fire(ctx context.Context, t db.Trigger, input map[string]any, parent int64) (int64, error) {
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
	started, _ := out.(Started)
	return started.Process, nil
}

// afterTriggers starts the commands that run when a command of the same project finishes.
// afterHook fires the triggers that follow a command which finished done with a result by its schema.
type afterHook struct {
	process.NoHook
	a *App
}

func (h afterHook) Closed(ctx context.Context, p db.Process) {
	if p.State == process.StateDone && p.NotBySchema == 0 && p.Result != process.Empty {
		h.a.afterTriggers(ctx, p)
	}
}

func (a *App) afterTriggers(ctx context.Context, done db.Process) {
	if done.TriggerID.Valid {
		a.Bus.Publish("triggers")
	}
	p, err := a.Store.Q.GetProject(ctx, done.ProjectID)
	if err != nil || p.Paused == 1 {
		return
	}
	m, err := docs.Manifest(ctx, a.Store.Q, p)
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
	a.expireSuggestions(ctx, now)
	a.stopAtCeiling(ctx, now)
	a.Runner.Unpause(ctx, now)
	a.markSilent(ctx, now)
	a.purgeEmpty(ctx, now)
	a.checkDisk(ctx, now)
	if spaces, err := a.Store.Q.ListWorkspaces(ctx); err == nil {
		for _, ws := range spaces {
			a.pushNew(ctx, ws)
		}
	}
	a.daily(ctx, now)
}

func (a *App) expireConfirms(ctx context.Context, now time.Time) {
	expired, err := store.TxGet(a.Store, ctx, func(ctx context.Context, q *db.Queries) ([]db.Ask, error) {
		return q.ExpireStaleConfirms(ctx, store.Time(now))
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
	ids, err := store.TxGet(a.Store, ctx, func(ctx context.Context, q *db.Queries) ([]int64, error) {
		return q.MarkSilentProcesses(ctx, now.Add(-silentAfter).UnixMilli())
	})
	if err != nil {
		slog.Error("silent processes", "err", err)
	}
	if len(ids) > 0 {
		a.Bus.Publish("processes")
	}
}

func (a *App) purgeEmpty(ctx context.Context, now time.Time) {
	ids, err := store.TxGet(a.Store, ctx, func(ctx context.Context, q *db.Queries) ([]int64, error) {
		return q.PurgeEmptyProcesses(ctx, store.Time(now.Add(-emptyKept)))
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
		if a.Scheduler.Tick(ctx) > 0 {
			a.Bus.Publish("triggers")
		}
		a.Sweep(ctx, a.Cfg.Now())
		a.Checks(ctx, a.Cfg.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
