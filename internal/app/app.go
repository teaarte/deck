package app

import (
	"context"
	"deck2/internal/app/failures"
	apphealth "deck2/internal/app/health"
	"deck2/internal/app/processes"
	"deck2/internal/app/screens"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"deck2/internal/app/asks"
	"deck2/internal/app/chat"
	"deck2/internal/app/devices"
	"deck2/internal/app/documents"
	appfiles "deck2/internal/app/files"
	"deck2/internal/app/folders"
	"deck2/internal/app/journal"
	"deck2/internal/app/project"
	"deck2/internal/app/providers"
	"deck2/internal/app/records"
	"deck2/internal/app/secrets"
	"deck2/internal/app/settings"
	"deck2/internal/app/signins"
	"deck2/internal/app/snapshots"
	"deck2/internal/app/tokens"
	"deck2/internal/app/triggers"
	"deck2/internal/ask"
	"deck2/internal/events"
	"deck2/internal/files"
	"deck2/internal/gateway"
	"deck2/internal/health"
	"deck2/internal/home"
	"deck2/internal/mcphost"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
	"deck2/internal/script"
	"deck2/internal/store"
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
	Providers providers.Service
	Devices   devices.Service
	Chats     chat.Service
	Tokens    tokens.Service
	Secrets   secrets.Service
	Settings  settings.Service
	Folders   folders.Service
	Files     appfiles.Service
	Journal   journal.Service
	Snapshots snapshots.Service
	Triggers  triggers.Service
	Documents documents.Service
	// Answers are what waits for the owner and their answers; Asks is the broker processes wait on.
	Answers   asks.Service
	Signins   signins.Service
	Projects  project.Service
	Records   records.Service
	Health    apphealth.Service
	Processes processes.Service
	Failures  failures.Service
	Screens   screens.Service
	Scripts   *script.Exec
	Tools     *tools.HTTP
	MCP       *tools.MCP
	Browser   *tools.Browser
	Host      *mcphost.Host

	ctx      context.Context
	cancel   context.CancelFunc
	bg       background
	stopOnce sync.Once
	// slowRunning is set while the loop pushes and takes the daily snapshot.
	slowRunning atomic.Bool
}

// New starts deck with its main provider p and further built-in ones.
func New(cfg Config, p provider.Provider, more ...provider.Provider) (*App, error) {
	cfg.defaults()
	h, err := home.Open(cfg.Home)
	if err != nil {
		return nil, err
	}
	st, err := store.Open(h.DB())
	if err != nil {
		return nil, err
	}
	// Every service is a field of a, so each is built whole with the functions of the others.
	a := &App{}
	a.Cfg, a.Home, a.Store, a.Bus, a.Scripts = cfg, h, st, events.NewBus(), script.New(cfg.Sandbox, h)
	registry.Declare(cfg.Commands, store.Topics())
	a.ctx, a.cancel = context.WithCancel(context.Background())
	st.Changed = func(c store.Change) {
		if c.Topic == store.Outbox {
			a.Files.Kick()
			return
		}
		a.Bus.Emit(events.Event{Workspace: c.Workspace, Topic: c.Topic, Project: c.Project})
	}
	a.Asks = ask.NewBroker(st, cfg.Now, a.bg.Go)
	a.Registry = registry.New(a.Projects.ProjectCommands)
	a.Gateway = gateway.New(gateway.Config{Now: cfg.Now, QuestionWait: cfg.QuestionWait, ConfirmWait: cfg.ConfirmWait, ConfirmTTL: a.Settings.ConfirmTTL,
		Models: a.Providers.Route, Progress: cfg.Progress, Go: a.bg.Go}, st, a.Asks, a.Bus, a.Registry)
	a.Gateway.SetAutomation(a.Tokens.Automation)
	oauth := &tools.OAuth{Secrets: a.Secrets.Read, Swap: a.Secrets.Swap}
	a.Tools = tools.NewHTTP(a.Gateway, a.Secrets.Read, h)
	a.Tools.OAuth, a.Tools.Health, a.Tools.SignIn = oauth, a.Signins.ToolHealth, a.Signins.ToolSignIn
	a.Tools.Resolve = func(ctx context.Context, project, process int64, ref string) (files.Ref, error) {
		return files.Resolve(ctx, h, a.Store.Q, project, process, ref)
	}
	a.Host = mcphost.New(a.Signins.ServerStatus)
	a.Host.Now, a.Host.Sandbox, a.Host.Home = cfg.Now, cfg.Sandbox, h
	a.Host.Web = gateway.Egress
	a.MCP = &tools.MCP{GW: a.Gateway, Host: a.Host, Store: st, Secrets: a.Secrets.Read, Home: h}
	a.Browser = &tools.Browser{GW: a.Gateway, Host: a.Host, Store: st, Home: h, Argv: cfg.Browser, Ctx: a.ctx}
	a.Runner = process.NewRunner(st, a.Gateway, a.Bus, a.Scripts,
		process.Config{Home: h, MCPBase: cfg.MCPBase, Now: cfg.Now, FilesMu: &a.Files.Mu,
			Hidden: func() []string { return sandbox.Agent(h) }, AgentSlots: a.Settings.AgentSlots},
		process.Deps{For: a.Providers.For, Next: a.Providers.Next, Named: a.Providers.Named, Goes: a.Providers.Goes, Broke: a.Providers.Broke, Recheck: a.Providers.Recheck, Prompt: a.Documents.Prompt,
			Text: a.Documents.Text, Skills: a.Documents.Skills, Servers: a.Signins.Servers, Folder: a.Folders.Folder,
			Memory: a.Chats.Memory, Programs: a.Documents.Programs})
	a.Runner.Hooks = []process.Hook{a.Chats.NewHook(), failures.Hook{S: &a.Failures}, triggers.After{S: &a.Triggers}, asks.Unheard{S: &a.Answers}}
	a.Scheduler = trigger.New(st, a.Triggers.Fire, cfg.Now)

	a.Secrets = secrets.Service{St: st, Bus: a.Bus, Dir: h.Secrets}
	a.Folders = folders.Service{St: st, Home: h}
	a.Files = appfiles.Service{St: st, Home: h, Now: cfg.Now, Held: a.Records.Held, Go: a.bg.Go, Ctx: a.ctx}
	a.Journal = journal.Service{St: st, Now: cfg.Now}
	a.Settings = settings.Service{St: st, DefaultTTL: cfg.ConfirmTTL, Wake: a.Runner.Wake, Words: screens.Phrases}
	a.Triggers = triggers.Service{St: st, Now: cfg.Now, Scheduler: a.Scheduler, Gateway: a.Gateway, Works: a.Providers.Works, Words: screens.Phrases}
	a.Documents = documents.Service{St: st, Now: cfg.Now, Sandbox: cfg.Sandbox, Ctx: a.ctx, Go: a.bg.Go,
		SaveManifest: a.Projects.Save, Changed: a.Devices.Changed}
	a.Snapshots = snapshots.Service{St: st, Bus: a.Bus, Home: h, Now: cfg.Now, Budget: cfg.SnapshotBudget, LockFiles: a.Files.Mu.Lock, UnlockFiles: a.Files.Mu.Unlock,
		Expire: a.Records.Expire}
	a.Providers = providers.Service{St: st, Words: screens.Phrases, Now: cfg.Now, Home: h, Sandbox: cfg.Sandbox, ACPBinaries: cfg.ACPBinaries,
		ACPCallLimit: cfg.ACPCallLimit, SelfCheckCeiling: cfg.SelfCheckCeiling, VideoPoll: cfg.VideoPoll,
		VideoCeiling: cfg.VideoCeiling, Secret: a.Secrets.Read, WriteSecret: a.Secrets.Write,
		Wake: a.Runner.Wake, Resolve: a.Tools.Resolve,
		Reach:   func(ctx context.Context) error { return a.Gateway.Reach(ctx, cfg.MCPBase) },
		Builtin: append([]provider.Provider{p}, more...), Ctx: a.ctx, Go: a.bg.Go}
	a.Answers = asks.Service{St: st, Now: cfg.Now, Broker: a.Asks, Registry: a.Registry, Gateway: a.Gateway, Deliver: a.Runner.Deliver,
		MergeGrant: a.Projects.MergeGrant, AddToList: a.Projects.AddToList}
	a.Signins = signins.Service{St: st, Ctx: a.ctx, Broker: a.Asks, OAuth: oauth, Browser: a.Browser, MCP: a.MCP}
	a.Chats = chat.Service{St: st, Bus: a.Bus, Now: cfg.Now, Asks: a.Asks, Runner: a.Runner, For: a.Providers.For,
		Named: a.Providers.Named, Fits: a.Providers.Fits, Able: a.Providers.Able, Go: a.bg.Go, Words: screens.Phrases}
	a.Devices = devices.Service{St: st, Now: cfg.Now,
		Home: h, Invoke: a.Registry.Invoke, DeckScreen: screens.DeckScreen, Waiting: a.Answers.Waiting}
	a.Failures = failures.Service{St: st, Now: cfg.Now, Runner: a.Runner}
	a.Processes = processes.Service{St: st, Home: h, Runner: a.Runner, Named: a.Providers.Named, Now: cfg.Now, Invoke: a.Registry.Invoke, Lookup: a.Registry.Lookup,
		Words: screens.Phrases}
	a.Health = apphealth.Service{St: st, Providers: a.Providers.All, Secret: a.Secrets.Has, Unassigned: a.Providers.Unassigned, NoSearch: a.Providers.NoSearch,
		Works: a.Providers.Works, Places: a.Providers.Places}
	a.Screens = screens.Service{St: st, Registry: a.Registry, Waiting: a.Answers.Waiting, Open: a.Processes.Open,
		Failing: a.Health.Failing, Failed: a.Processes.Failed, Unread: a.Devices.Unread, Works: a.Providers.Works,
		ScreenFailed: a.Failures.Screen, ScreenDrawn: a.Failures.ScreenDrawn, File: a.Files.File}
	a.Records = records.Service{St: st, Now: cfg.Now, Stop: a.Runner.Cancel}
	a.Tokens = tokens.Service{St: st, MCPBase: cfg.MCPBase, Journal: a.Gateway.Journal}
	a.Projects = project.Service{St: st, Bus: a.Bus, Now: cfg.Now, Home: h, Registry: a.Registry,
		Gateway: a.Gateway, Runner: a.Runner, Scheduler: a.Scheduler, Broker: a.Asks, Scripts: a.Scripts, Tools: a.Tools,
		MCP: a.MCP, Host: a.Host, Folders: a.Folders.List, Snapshot: a.Snapshots.BeforeDelete, Keep: a.Files.Keep,
		PrepareLater: a.Documents.PrepareLater, Changed: a.Devices.Changed, CheckScreens: a.Screens.CheckScreens,
		Unassigned: a.Providers.Unassigned}
	a.addBuiltins()
	a.Browser.Load()
	a.Registry.SetExtra(a.Browser.Commands)
	a.Asks.OnLateAnswer(a.Answers.LateAnswer)
	if err := a.Documents.SeedSkills(a.ctx); err != nil {
		return nil, err
	}
	if err := a.Projects.SeedMaster(a.ctx); err != nil {
		return nil, err
	}
	a.Projects.CheckManifests(a.ctx)
	a.Health.Watch(a.Bus)
	a.Health.Derive(a.ctx, 0)
	status, text := script.Runtimes().Health()
	if err := health.Write(a.ctx, a.Store, health.Of{}, health.Scripts, status, text); err != nil {
		return nil, err
	}
	return a, nil
}

// Close interrupts running providers; their processes stay running and resume on the next start.
func (a *App) Close() error {
	a.Stop()
	a.Host.Close()
	return a.Store.Close()
}

// Stop ends the turns of the runner, then the loop and the background work; deck still answers, so a stop
// takes it before the server goes.
func (a *App) Stop() {
	a.stopOnce.Do(func() {
		a.Runner.Shutdown()
		a.cancel()
		a.bg.stop()
	})
}

// background is the work deck runs beside the calls; once deck stops, none starts.
type background struct {
	mu      sync.Mutex
	wg      sync.WaitGroup
	stopped bool
}

// Go runs f beside the calls unless deck stops, and reports whether it runs.
func (b *background) Go(f func()) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return false
	}
	b.wg.Go(f)
	return true
}

func (b *background) stop() {
	b.mu.Lock()
	b.stopped = true
	b.mu.Unlock()
	b.wg.Wait()
}

// Run starts deck's work: the schedule of the saved triggers, the self-checks, the turns a stop left and the replies
// to messages left unanswered, then the loop every interval until Stop.
func (a *App) Run(ctx context.Context, every time.Duration) {
	if err := a.Projects.Reconcile(ctx); err != nil {
		slog.Error("reconcile triggers", "err", err)
	}
	a.Providers.Checks(ctx, a.Cfg.Now())
	if err := a.Runner.ResumeInterrupted(ctx); err != nil {
		slog.Error("resume interrupted", "err", err)
	}
	a.Chats.PumpChats(ctx)
	a.bg.Go(func() { a.Loop(a.ctx, every) })
}

// Sweep checks every deadline against now, then pushes and runs the daily cleanup and snapshot.
func (a *App) Sweep(ctx context.Context, now time.Time) {
	a.deadlines(ctx, now)
	a.slow(ctx, now)
}

// deadlines are what is due by now: expired confirmations, ceilings, pauses on a usage limit,
// silent processes, empty results kept long enough, files whose rows are gone, the disk.
func (a *App) deadlines(ctx context.Context, now time.Time) {
	a.Answers.ExpireConfirms(ctx, now)
	a.Runner.Due(ctx, now)
	a.Files.Drain(ctx)
	a.Snapshots.CheckDisk(ctx, now)
}

// slow is the work of the loop that waits on the network or the disk: pushes, within pushTimeout, and the daily
// cleanup and snapshot.
func (a *App) slow(ctx context.Context, now time.Time) {
	pctx, cancel := context.WithTimeout(ctx, pushTimeout)
	if spaces, err := a.Store.Q.ListWorkspaces(pctx); err == nil {
		for _, ws := range spaces {
			a.Devices.PushNew(pctx, ws)
		}
	}
	cancel()
	a.Snapshots.Daily(ctx, now)
}

const pushTimeout = 2 * time.Minute

// Loop runs the scheduler and the deadlines by the deck clock until ctx ends, pushes, snapshots and self-checks
// beside it; after a sleep of the machine the first tick finds every deadline that passed.
func (a *App) Loop(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		a.Scheduler.Tick(ctx)
		now := a.Cfg.Now()
		a.deadlines(ctx, now)
		if a.slowRunning.CompareAndSwap(false, true) {
			a.bg.Go(func() {
				defer a.slowRunning.Store(false)
				a.slow(ctx, now)
			})
		}
		a.Providers.Checks(ctx, now)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// TailnetHealth records the state of deck on the owner's tailnet: the sign-in link while it waits for one, its https
// address once it is on it.
func (a *App) TailnetHealth(status, text, address string) {
	a.Devices.Tailnet.Store(&address)
	if err := health.Write(context.Background(), a.Store, health.Of{}, health.Tailscale, status, text); err != nil {
		slog.Error("write health", "err", err)
	}
}
