package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"deck2/internal/app/board"
	"deck2/internal/app/chat"
	"deck2/internal/app/devices"
	"deck2/internal/app/providers"
	"deck2/internal/app/suggest"
	"deck2/internal/app/tokens"
	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/files"
	"deck2/internal/gateway"
	"deck2/internal/home"
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
	Cfg Config
	// Home is the layout of the folder of deck.
	Home      home.Layout
	Store     *store.Store
	Bus       *events.Bus
	Asks      *ask.Broker
	Registry  *registry.Registry
	Gateway   *gateway.Gateway
	Runner    *process.Runner
	Scheduler *trigger.Scheduler
	Board     *board.Service
	Providers *providers.Service
	Devices   *devices.Service
	Chats     *chat.Service
	Suggest   *suggest.Service
	Tokens    *tokens.Service
	Scripts   *script.Exec
	Tools     *tools.HTTP
	MCP       *tools.MCP
	Browser   *tools.Browser
	Host      *mcphost.Host
	Manifests *docs.Manifests

	ctx      context.Context
	cancel   context.CancelFunc
	secretMu sync.Mutex
	bg       sync.WaitGroup
	disk     time.Time
	// filesMu keeps stored files from coming or going while a snapshot copies the base and files/.
	filesMu sync.RWMutex
	// preparedCmds are the registry commands of each manifest revision.
	preparedMu   sync.Mutex
	preparedCmds map[preparedKey][]*registry.Command
	// cleaned and snapTried are when the daily cleanup ran and a daily snapshot was last tried.
	cleaned, snapTried time.Time
}

// New starts deck with its main provider p and further built-in ones.
func New(cfg Config, p provider.Provider, more ...provider.Provider) (*App, error) {
	cfg.defaults()
	h := home.Layout(cfg.Home)
	if old := h.Old(); len(old) > 0 {
		return nil, fmt.Errorf("deck: %s holds a deck of an older layout (%s); move it away and start again, deck makes state/ and work/", cfg.Home, strings.Join(old, ", "))
	}
	for _, dir := range []string{h.State(), h.Runs()} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, err
		}
	}
	st, err := store.Open(h.DB())
	if err != nil {
		return nil, err
	}
	a := &App{Cfg: cfg, Home: h, Store: st, Bus: events.NewBus(), Scripts: script.New(cfg.Sandbox, cfg.Home), Manifests: &docs.Manifests{St: st}}
	a.ctx, a.cancel = context.WithCancel(context.Background())
	st.Changed = func(c store.Change) {
		a.Bus.Emit(events.Event{Workspace: c.Workspace, Topic: c.Topic, Project: c.Project})
	}
	a.Asks = ask.NewBroker(st, a.Bus, cfg.Now)
	a.Registry = registry.New(a.projectCommands)
	a.Registry.SetPublish(a.Bus.Publish)
	a.Board = &board.Service{St: st, Bus: a.Bus, Now: cfg.Now, Fire: a.fire}
	a.Providers = &providers.Service{St: st, Bus: a.Bus, Now: cfg.Now, Home: cfg.Home, Sandbox: cfg.Sandbox, ACPBinaries: cfg.ACPBinaries,
		ACPCallLimit: cfg.ACPCallLimit, SelfCheckCeiling: cfg.SelfCheckCeiling, Secret: a.Secret, Wake: func(ctx context.Context) { a.Runner.Wake(ctx) },
		Builtin: append([]provider.Provider{p}, more...), Ctx: a.ctx, BG: &a.bg}
	a.Suggest = &suggest.Service{St: st, Bus: a.Bus, Now: cfg.Now, Registry: a.Registry, Asks: a.Asks, Days: a.suggestDays}
	a.Chats = &chat.Service{St: st, Bus: a.Bus, Now: cfg.Now, Asks: a.Asks, For: a.For}
	a.Devices = &devices.Service{St: st, Bus: a.Bus, Now: cfg.Now, Ctx: a.ctx, STT: cfg.STT, Files: a.dirs(), RemoveFiles: a.removeFiles,
		Secret: a.Secret, ReadSecret: a.readSecret, WriteSecret: a.writeSecret, SecretsDir: a.secretsDir,
		LockSecrets: a.secretMu.Lock, UnlockSecrets: a.secretMu.Unlock,
		Invoke: func(ctx context.Context, act registry.Actor, name string, in json.RawMessage) (any, error) {
			return a.Registry.Invoke(ctx, act, name, in)
		}}
	a.Tokens = &tokens.Service{St: st, MCPBase: cfg.MCPBase}
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
	a.Browser = &tools.Browser{GW: a.Gateway, Host: a.Host, Store: st, Dirs: a.dirs(), Home: cfg.Home, Argv: cfg.Browser, Ctx: a.ctx}
	a.Browser.Load()
	a.Registry.SetExtra(a.Browser.Commands)
	a.Gateway.SetAutomation(a.Tokens.Automation)
	a.Runner = process.NewRunner(st, a.Gateway, a.Bus, a, a.Scripts,
		process.Config{RunsDir: h.Runs(), MCPBase: cfg.MCPBase, Now: cfg.Now, FilesDir: h.Files(), FilesMu: &a.filesMu,
			Hidden: hidden(h), PauseLimit: cfg.PauseLimit, AgentSlots: a.agentSlots}, a)
	a.Chats.Runner, a.Suggest.Gateway, a.Tokens.Journal = a.Runner, a.Gateway, a.Gateway.Journal
	a.Runner.Hooks = []process.Hook{a.Chats.NewHook(), errorsHook{a: a}, afterHook{a: a}}
	a.Scheduler = trigger.New(st, a, cfg.Now)
	a.Asks.OnLateAnswer(a.lateAnswer)
	if err := a.SeedSkills(a.ctx); err != nil {
		return nil, err
	}
	if err := a.SeedMaster(a.ctx); err != nil {
		return nil, err
	}
	a.Devices.WatchPush()
	return a, nil
}

func (a *App) dirs() files.Dirs {
	return files.Dirs{Files: a.Home.Files(), Runs: a.Home.Runs()}
}

// hidden are what an agent may not read or write under every path of home, since macOS links /tmp and /var: all
// of home but the runs and projects of work, then those but its own, and in its own project all but the data.
func hidden(h home.Layout) func(project, process int64) []string {
	roots := []home.Layout{h}
	if real, err := filepath.EvalSymlinks(string(h)); err == nil && real != string(h) {
		roots = append(roots, home.Layout(real))
	}
	return func(project, process int64) []string {
		var out []string
		for _, r := range roots {
			out = append(out, r.State()+"/**")
			top, _ := os.ReadDir(string(r))
			for _, e := range top {
				switch {
				case e.Name() == "work" || e.Name() == "state":
				case e.IsDir():
					out = append(out, filepath.Join(string(r), e.Name())+"/**")
				default:
					out = append(out, filepath.Join(string(r), e.Name()))
				}
			}
			out = append(out, others(r.Runs(), fmt.Sprint(process))...)
			out = append(out, others(r.Projects(), fmt.Sprint(project))...)
			names := []string{"mcp"}
			entries, _ := os.ReadDir(r.Project(project))
			for _, e := range entries {
				if e.Name() != "data" && !slices.Contains(names, e.Name()) {
					names = append(names, e.Name())
				}
			}
			for _, n := range names {
				out = append(out, filepath.Join(r.Project(project), n)+"/**")
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

// Memory is what a fresh session of a process gets of the memory.
func (a *App) Memory(ctx context.Context, p db.Process) (string, error) {
	return a.Chats.Memory(ctx, p)
}

// PumpChats starts the replies to the owner's messages that wait since deck stopped.
func (a *App) PumpChats(ctx context.Context) { a.Chats.PumpChats(ctx) }

// Named, For and Ready are the providers the runner asks for.
func (a *App) Named(ctx context.Context, projectID int64, name string) (provider.Provider, error) {
	return a.Providers.Named(ctx, projectID, name)
}

func (a *App) For(ctx context.Context, projectID int64, cmd *manifest.Command) (provider.Provider, error) {
	return a.Providers.For(ctx, projectID, cmd)
}

func (a *App) Ready(ctx context.Context, name string) bool { return a.Providers.Ready(ctx, name) }

// Checks runs the self-checks of providers that are due.
func (a *App) Checks(ctx context.Context, now time.Time) { a.Providers.Checks(ctx, now) }

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
	a.Board.Purge(ctx, now)
	a.expireConfirms(ctx, now)
	a.Suggest.Expire(ctx, now)
	a.stopAtCeiling(ctx, now)
	a.Runner.Unpause(ctx, now)
	a.markSilent(ctx, now)
	a.purgeEmpty(ctx, now)
	a.checkDisk(ctx, now)
	if spaces, err := a.Store.Q.ListWorkspaces(ctx); err == nil {
		for _, ws := range spaces {
			a.Devices.PushNew(ctx, ws)
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
	_, err := store.TxGet(a.Store, ctx, func(ctx context.Context, q *db.Queries) ([]int64, error) {
		return q.MarkSilentProcesses(ctx, now.Add(-silentAfter).UnixMilli())
	})
	if err != nil {
		slog.Error("silent processes", "err", err)
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
		_ = os.RemoveAll(a.Home.Run(id))
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
		a.Providers.Checks(ctx, a.Cfg.Now())
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}
