package app

import (
	"context"
	"deck2/internal/app/failures"
	apphealth "deck2/internal/app/health"
	"deck2/internal/app/processes"
	"fmt"
	"os"
	"strings"
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
	Providers *providers.Service
	Devices   *devices.Service
	Chats     *chat.Service
	Tokens    *tokens.Service
	Secrets   *secrets.Service
	Settings  *settings.Service
	Folders   *folders.Service
	Files     *appfiles.Service
	Journal   *journal.Service
	Snapshots *snapshots.Service
	Triggers  *triggers.Service
	Documents *documents.Service
	// Answers are what waits for the owner and their answers; Asks is the broker processes wait on.
	Answers   *asks.Service
	Signins   *signins.Service
	Projects  *project.Service
	Saver     *project.Saver
	Records   *records.Service
	Health    *apphealth.Service
	Processes *processes.Service
	Failures  *failures.Service
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
	// Every service is allocated first, so each is then built whole with the functions of the others.
	a := allocated()
	a.Cfg, a.Home, a.Store, a.Bus, a.Scripts = cfg, h, st, events.NewBus(), script.New(cfg.Sandbox, h)
	registry.Declare(cfg.Commands)
	a.ctx, a.cancel = context.WithCancel(context.Background())
	st.Changed = func(c store.Change) {
		a.Bus.Emit(events.Event{Workspace: c.Workspace, Topic: c.Topic, Project: c.Project})
	}
	a.Asks = ask.NewBroker(st, cfg.Now)
	a.Registry = registry.New(a.Saver.ProjectCommands)
	a.Gateway = gateway.New(gateway.Config{Now: cfg.Now, QuestionWait: cfg.QuestionWait, ConfirmWait: cfg.ConfirmWait, ConfirmTTL: a.Settings.ConfirmTTL,
		Models: a.Providers.Route, Progress: cfg.Progress}, st, a.Asks, a.Bus, a.Registry)
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
		process.Deps{For: a.Providers.For, Named: a.Providers.Named, Up: a.Providers.Up, Broke: a.Providers.Broke, Recheck: a.Providers.Recheck, Prompt: a.Saver.Prompt,
			Text: a.Saver.Text, Skills: a.Documents.Skills, Servers: a.Signins.Servers, Folder: a.Folders.Folder,
			Memory: a.Chats.Memory, Programs: a.Documents.Programs, DrainFiles: a.Files.Drain})
	a.Runner.Hooks = []process.Hook{a.Chats.NewHook(), failures.Hook{S: a.Failures}, triggers.After{S: a.Triggers}, asks.Unheard{S: a.Answers}}
	a.Scheduler = trigger.New(st, a.Triggers, cfg.Now)

	*a.Secrets = secrets.Service{St: st, Bus: a.Bus, Dir: h.Secrets}
	*a.Folders = folders.Service{St: st, Home: h}
	*a.Files = appfiles.Service{St: st, Home: h, Now: cfg.Now, Held: a.Records.Held}
	*a.Journal = journal.Service{St: st, Now: cfg.Now, Backups: h.Backups()}
	*a.Settings = settings.Service{St: st, DefaultTTL: cfg.ConfirmTTL, Wake: a.Runner.Wake}
	*a.Triggers = triggers.Service{St: st, Scheduler: a.Scheduler, Gateway: a.Gateway, Registry: a.Registry, Model: a.Providers.CommandModel}
	*a.Documents = documents.Service{St: st, Now: cfg.Now, Sandbox: cfg.Sandbox, Ctx: a.ctx, Go: a.bg.Go,
		SaveManifest: a.Saver.SaveCall, Changed: a.Devices.Changed}
	*a.Snapshots = snapshots.Service{St: st, Bus: a.Bus, Home: h, Now: cfg.Now, Budget: cfg.SnapshotBudget, LockFiles: a.Files.Mu.Lock, UnlockFiles: a.Files.Mu.Unlock,
		DrainFiles: a.Files.Drain, Expire: a.Records.Expire}
	*a.Providers = providers.Service{St: st, Now: cfg.Now, Home: h, Sandbox: cfg.Sandbox, ACPBinaries: cfg.ACPBinaries,
		ACPCallLimit: cfg.ACPCallLimit, SelfCheckCeiling: cfg.SelfCheckCeiling, VideoPoll: cfg.VideoPoll,
		VideoCeiling: cfg.VideoCeiling, Secret: a.Secrets.Read, WriteSecret: a.Secrets.Write,
		Wake: a.Runner.Wake, Resolve: a.Tools.Resolve,
		Reach:   func(ctx context.Context) error { return a.Gateway.Reach(ctx, cfg.MCPBase) },
		Builtin: append([]provider.Provider{p}, more...), Ctx: a.ctx, Go: a.bg.Go}
	*a.Answers = asks.Service{St: st, Now: cfg.Now, Broker: a.Asks, Registry: a.Registry, Gateway: a.Gateway, Deliver: a.Runner.Deliver,
		AnswerGrant: a.Saver.AnswerGrant, SaveManifest: a.Saver.SaveCall}
	*a.Signins = signins.Service{St: st, Ctx: a.ctx, Broker: a.Asks, OAuth: oauth, Browser: a.Browser, MCP: a.MCP}
	*a.Chats = chat.Service{St: st, Bus: a.Bus, Now: cfg.Now, Asks: a.Asks, Runner: a.Runner, For: a.Providers.For,
		Named: a.Providers.Named, Fits: a.Providers.Fits, DrainFiles: a.Files.Drain, File: a.Files.File}
	*a.Devices = devices.Service{St: st, Bus: a.Bus, Now: cfg.Now, DrainFiles: a.Files.Drain,
		Home: h, Invoke: a.Registry.Invoke, DeckScreen: deckScreen}
	*a.Failures = failures.Service{St: st, Now: cfg.Now, Runner: a.Runner}
	*a.Processes = processes.Service{St: st, Runner: a.Runner, Named: a.Providers.Named, Now: cfg.Now, Invoke: a.Registry.Invoke}
	*a.Health = apphealth.Service{St: st, Providers: a.Providers.All, Secret: a.Secrets.Has, Unassigned: a.Providers.Unassigned}
	*a.Records = records.Service{St: st, Now: cfg.Now, DrainFiles: a.Files.Drain, Stop: a.Runner.Stop}
	*a.Tokens = tokens.Service{St: st, MCPBase: cfg.MCPBase, Journal: a.Gateway.Journal}
	*a.Saver = project.Saver{St: st, Bus: a.Bus, Now: cfg.Now, Home: h, Registry: a.Registry,
		Gateway: a.Gateway, Runner: a.Runner, Scheduler: a.Scheduler, Broker: a.Asks, Scripts: a.Scripts, Tools: a.Tools,
		MCP: a.MCP, PrepareLater: a.Documents.PrepareLater, Changed: a.Devices.Changed, CheckScreens: a.checkScreens,
		Unassigned: a.Providers.Unassigned}
	*a.Projects = project.Service{St: st, Home: h, Registry: a.Registry, Gateway: a.Gateway,
		Runner: a.Runner, Scheduler: a.Scheduler, Host: a.Host, Save: a.Saver, Folders: a.Folders.List,
		Snapshot: a.Snapshots.BeforeDelete, DrainFiles: a.Files.Drain}
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
	return a, nil
}

// allocated is an App with every service allocated and none built.
func allocated() *App {
	return &App{Projects: &project.Service{}, Saver: &project.Saver{}, Secrets: &secrets.Service{}, Folders: &folders.Service{},
		Files: &appfiles.Service{}, Journal: &journal.Service{}, Triggers: &triggers.Service{}, Documents: &documents.Service{},
		Snapshots: &snapshots.Service{}, Settings: &settings.Service{}, Providers: &providers.Service{}, Answers: &asks.Service{},
		Signins: &signins.Service{}, Chats: &chat.Service{}, Devices: &devices.Service{}, Tokens: &tokens.Service{},
		Records: &records.Service{}, Health: &apphealth.Service{}, Processes: &processes.Service{}, Failures: &failures.Service{}}
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

// Start runs the loop of deck every interval until Stop.
func (a *App) Start(every time.Duration) {
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

// TailnetHealth records the state of deck on the owner's tailnet: the sign-in link while it waits for one.
func (a *App) TailnetHealth(status, text string) {
	health.Write(context.Background(), a.Store, health.Of{}, "tailscale", status, text)
}
