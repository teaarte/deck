package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"deck2/internal/app/asks"
	"deck2/internal/app/chat"
	"deck2/internal/app/documents"
	"deck2/internal/app/feed"
	appfiles "deck2/internal/app/files"
	"deck2/internal/app/folders"
	apphealth "deck2/internal/app/health"
	"deck2/internal/app/journal"
	"deck2/internal/app/owners"
	"deck2/internal/app/processes"
	"deck2/internal/app/project"
	"deck2/internal/app/providers"
	"deck2/internal/app/records"
	"deck2/internal/app/screens"
	"deck2/internal/app/secrets"
	"deck2/internal/app/settings"
	"deck2/internal/app/shots"
	"deck2/internal/app/signins"
	"deck2/internal/app/snapshots"
	"deck2/internal/app/themes"
	"deck2/internal/app/triggers"
	"deck2/internal/app/updates"
	"deck2/internal/app/work"
	"deck2/internal/ask"
	"deck2/internal/auth"
	"deck2/internal/events"
	"deck2/internal/files"
	"deck2/internal/gateway"
	"deck2/internal/health"
	"deck2/internal/home"
	"deck2/internal/manifest"
	"deck2/internal/mcphost"
	"deck2/internal/node"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
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
	Providers providers.Service
	Work      work.Service
	Feed      feed.Service
	Chats     chat.Service
	// Auth signs the owner's devices in; Owners keeps the devices, their pushes and the tokens.
	Auth   *auth.Auth
	Owners owners.Service
	// Nodes holds the connections of the nodes that serve deck the models of their machines.
	Nodes     *node.Hub
	Secrets   secrets.Service
	Settings  settings.Service
	Themes    themes.Service
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
	Screens   screens.Service
	Scripts   *script.Exec
	Tools     *tools.HTTP
	MCP       *tools.MCP
	Browser   *tools.Browser
	Shots     shots.Service
	Updates   updates.Service
	Host      *mcphost.Host

	ctx      context.Context
	cancel   context.CancelFunc
	bg       background
	stopOnce sync.Once
	// slowRunning is set while the loop pushes; dailyRunning, while it takes the daily snapshot.
	slowRunning, dailyRunning atomic.Bool
	mods                      modules
}

// New starts deck with its built-in providers, the main one first; those of providers.Build when none.
func New(cfg Config, builtin ...provider.Provider) (*App, error) {
	cfg.defaults()
	if len(builtin) == 0 {
		builtin = providers.Build(cfg.Home, cfg.Sandbox)
	}
	h, err := home.Open(cfg.Home)
	if err != nil {
		return nil, err
	}
	cfg.Sandbox.Reap()
	st, err := store.Open(h.DB(), cfg.Now, snapshots.BeforeUpdate(h, cfg.Now))
	if err != nil {
		return nil, err
	}
	// Every service is a field of a, so each is built whole with the functions of the others.
	a := &App{}
	a.Cfg, a.Home, a.Store, a.Bus, a.Scripts = cfg, h, st, events.NewBus(), script.New(cfg.Sandbox, h)
	a.Scripts.Image, a.Scripts.Now = cfg.Image, cfg.Now
	registry.Declare(cfg.Commands, events.Topics)
	a.ctx, a.cancel = context.WithCancel(context.Background())
	a.bg.ctx = a.ctx
	st.Changed = a.Bus.Emit
	a.Asks = ask.NewBroker(st, cfg.Now, a.bg.Go)
	a.mods.a = a
	key, err := home.Key(h.PressKey())
	if err != nil {
		return nil, err
	}
	a.Registry = registry.New(a.mods.commands, key, cfg.Now)
	a.Gateway = gateway.New(gateway.Config{Now: cfg.Now, QuestionWait: cfg.QuestionWait, ConfirmWait: cfg.ConfirmWait, ConfirmTTL: a.Settings.ConfirmTTL, MarkedWeb: a.Settings.MarkedWeb,
		Models: func(act registry.Actor) http.Handler { return a.Work.Proxy(act, a.Gateway.Permit) }, Progress: cfg.Progress, Go: a.bg.Go},
		gateway.Deps{Store: st, Asks: a.Asks, Bus: a.Bus, Registry: a.Registry})
	oauth := &tools.OAuth{Secrets: a.Secrets.Read, Swap: a.Secrets.Swap}
	a.Tools = &tools.HTTP{GW: a.Gateway, Secrets: a.Secrets.Read, Home: h, OAuth: oauth, Health: a.Signins.ToolHealth, SignIn: a.Signins.ToolSignIn,
		Resolve: func(ctx context.Context, project, process int64, ref string) (files.Ref, error) {
			return files.Resolve(ctx, h, a.Store.Q, project, process, ref)
		}}
	a.Host = mcphost.New(mcphost.Config{Now: cfg.Now}, mcphost.Deps{Status: a.Signins.ServerStatus, Sandbox: cfg.Sandbox})
	a.MCP = &tools.MCP{GW: a.Gateway, Host: a.Host, Store: st, Secrets: a.Secrets.Read, Home: h}
	a.Browser = &tools.Browser{GW: a.Gateway, Host: a.Host, Store: st, Home: h, Argv: cfg.Browser}
	a.Runner = process.New(
		process.Config{Home: h, MCPBase: cfg.MCPBase, Now: cfg.Now, Hold: a.Files.Hold, AgentSlots: a.Settings.AgentSlots},
		process.Deps{Store: st, Gateway: a.Gateway, Bus: a.Bus, Scripts: a.Scripts,
			Hooks: []process.Hook{a.Chats.Hook(), a.Processes.Hook(), a.Triggers.Hook(), a.Answers.Hook()},
			For:   a.Work.For, Next: a.Work.Next, Ahead: a.Work.Ahead, Named: a.Providers.Named, Goes: a.Providers.Goes, Down: a.Providers.Down, Broke: a.Providers.Broke, Recheck: a.Providers.Recheck, Prompt: a.Documents.Prompt,
			Text: a.Documents.Text, Skills: a.Documents.Skills, Servers: a.Signins.Servers, Folder: a.Folders.Folder,
			Folders: a.Folders.Paths, ScriptsAt: a.Folders.ScriptsAt,
			Memory: a.Chats.Memory, Lang: a.Chats.Gives, Programs: a.Documents.Programs})
	a.Scheduler = trigger.New(st, a.Triggers.Fire, cfg.Now)

	a.Secrets = secrets.Service{St: st, Emit: a.Bus.Emit, Home: h}
	a.Files = appfiles.Service{St: st, Home: h, Now: cfg.Now, Held: a.Records.Held, Go: a.bg.Go, Invoke: a.Registry.Invoke}
	a.Journal = journal.Service{St: st, Now: cfg.Now, Groups: a.Work.Groups, Explain: a.Gateway.Explain}
	a.Settings = settings.Service{St: st, DefaultTTL: cfg.ConfirmTTL, Wake: a.Runner.Wake}
	a.Themes = themes.Service{St: st, Now: cfg.Now, Home: h, Invoke: a.Registry.Invoke, Language: a.Settings.Language,
		Picked: a.Settings.Theme, Pick: a.Settings.SetTheme, Keep: a.Files.Keep, Thumb: a.Files.Thumb, Drop: a.Files.Drop}
	a.Triggers = triggers.Service{St: st, Now: cfg.Now, Scheduler: a.Scheduler, Gateway: a.Gateway, Works: a.Work.Works, Incident: a.Processes.Incident, IsFailure: a.Processes.IsFailure}
	a.Documents = documents.Service{St: st, Now: cfg.Now, Sandbox: cfg.Sandbox, Missing: a.missingPrograms, Go: a.bg.Go,
		SaveManifest: a.Projects.Save, Noted: a.Feed.Noted, Home: h, Fits: a.Work.Fits, Language: a.Settings.Language,
		Saw: a.Runner.Saw, Seen: a.Runner.Seen}
	a.Snapshots = snapshots.Service{St: st, Emit: a.Bus.Emit, Home: h, Now: cfg.Now, Budget: cfg.SnapshotBudget, Still: a.Files.Still,
		Expire: a.Records.Expire}
	a.Nodes = node.NewHub(cfg.Now, func(device int64, online bool) {
		a.Owners.NodeChanged(device, online)
		if online {
			a.bg.Go(a.Runner.Wake)
		}
	})
	a.Nodes.Denied = func(device int64) { a.Owners.NodeChanged(device, true) }
	a.Folders = folders.Service{St: st, Home: h, Node: a.Providers.NodeNamed, Probe: a.Nodes.Probe, Scripts: a.Providers.Scripts,
		Online: func(device int64) bool { return a.Nodes.State(device).Online }}
	a.Providers = providers.Service{St: st, Now: cfg.Now, Home: h, Sandbox: cfg.Sandbox, ACPBinaries: cfg.ACPBinaries,
		Nodes: a.Nodes, NodeWait: cfg.NodeWait, NodeDenied: a.Nodes.Denied, Low: func(ctx context.Context, text string) {
			if err := health.Write(ctx, st, health.Of{}, health.Disk, health.Warning, text); err != nil {
				slog.Error("write health", "err", err)
			}
		},
		ACPCallLimit: cfg.ACPCallLimit, SelfCheckCeiling: cfg.SelfCheckCeiling, WriteKey: a.Secrets.WriteKey, Wake: a.Runner.Wake,
		TurnedOff: a.Settings.ProviderOff, TurnOff: a.Settings.SetProviderOff,
		Reach:   func(ctx context.Context) error { return a.Gateway.Reach(ctx, cfg.MCPBase) },
		Builtin: builtin, Ctx: a.ctx, Go: a.bg.Go, TurnModel: a.Work.TurnModel, Catalog: a.Work.Catalog, CheckFiles: a.Work.CheckFiles,
		KeepBig: a.Work.KeepBig, View: a.Work.ModelView, Unassign: a.Work.Unassign}
	a.Work = work.Service{St: st, Now: cfg.Now, Home: h, Hold: a.Files.Hold, VideoPoll: cfg.VideoPoll, VideoCeiling: cfg.VideoCeiling,
		Key: a.Secrets.Key, Resolve: a.Tools.Resolve, Invoke: a.Registry.Invoke, Journal: a.Gateway.Journal, Builtin: builtin, Provider: a.Providers.Provider,
		All: a.Providers.All, Off: a.Providers.Off, Up: a.Providers.Up, Health: a.Providers.Health, Exists: a.Providers.Exists, Free: a.Providers.Free, Spare: a.Providers.Spare,
		Installed: a.Providers.Installed, Node: a.nodeTransport,
		Online: func(device int64) bool { return a.Nodes.State(device).Online }, FolderNodes: a.Folders.Nodes, Go: a.bg.Go,
		Tools: func(ctx context.Context, p db.Project, cmd *manifest.Command) int {
			return a.Gateway.Count(ctx, &gateway.Session{ProjectID: p.ID, Workspace: p.WorkspaceID, Master: p.Builtin == 1, Commands: cmd.Executor.Calls})
		}}
	a.Answers = asks.Service{St: st, Now: cfg.Now, Broker: a.Asks, Registry: a.Registry, Gateway: a.Gateway, Deliver: a.Runner.Deliver,
		Said: a.Runner.Said, Go: a.bg.Go, Start: a.Runner.Start, Lang: a.Chats.LangOf,
		Shown: func(ctx context.Context, act registry.Actor, q db.Ask, answer string) (any, bool, error) {
			return a.Chats.AnswerShown(ctx, act, q, answer)
		}}
	a.Signins = signins.Service{St: st, Ctx: a.ctx, Broker: a.Asks, OAuth: oauth, Browser: a.Browser, MCP: a.MCP,
		Agent: a.Providers.Agent, Check: a.Providers.Check, Emit: a.Bus.Emit, Go: a.bg.Go}
	a.Chats = chat.Service{St: st, Emit: a.Bus.Emit, Now: cfg.Now, Asks: a.Asks, Runner: a.Runner, For: a.Work.For,
		Named: a.Providers.Named, Fits: a.Work.Fits, Able: a.Work.Able, Facts: a.Work.Facts, Go: a.bg.Go, Language: a.Settings.Language, Lookup: a.Registry.Lookup,
		Decisions: func(ctx context.Context, act registry.Actor, ps []int64, lang string) (any, error) {
			return a.Answers.Of(ctx, act, ps, lang)
		}}
	a.Feed = feed.Service{St: st, Now: cfg.Now,
		Home: h, Invoke: a.Registry.Invoke, Call: a.Gateway.CallAs, Waiting: a.Answers.Waiting, Hidden: a.Settings.PushHidden, Lang: a.Chats.Gives, Reads: a.Chats.LangOf}
	a.Processes = processes.Service{St: st, Home: h, Runner: a.Runner, Named: a.Providers.Named, Now: cfg.Now, Invoke: a.Registry.Invoke, Lookup: a.Registry.Lookup,
		Works: a.Work.Works}
	a.Health = apphealth.Service{St: st, Providers: a.Providers.All, Secret: a.Secrets.Has, Unassigned: a.Work.Unassigned, NoSearch: a.Work.NoSearch, Missing: a.Work.Missing,
		Works: a.Work.Works, Places: a.Work.Places, Go: a.bg.Go}
	a.Screens = screens.Service{St: st, Now: cfg.Now, Registry: a.Registry, Waiting: a.Answers.Waiting, Open: a.Processes.Open,
		Failing: a.Health.Failing, Failed: a.Processes.Failed, Unread: a.Feed.Unread, Works: a.Work.Works,
		ScreenFailed: a.Processes.Screen, ScreenDrawn: a.Processes.ScreenDrawn, File: a.Files.File, Language: a.Settings.Language,
		Theme: a.Themes.Current}
	a.Records = records.Service{St: st, Now: cfg.Now, Stop: a.Runner.Cancel}
	a.Auth = auth.New(auth.Config{Now: cfg.Now, Home: h, Origins: cfg.Origins}, auth.Deps{Store: st})
	a.Owners = owners.Service{St: st, Now: cfg.Now, MCPBase: cfg.MCPBase, Journal: a.Gateway.Journal, Code: a.Auth.Code, Kick: a.Auth.Kick,
		Origins: cfg.Origins.List, PushNew: a.Feed.PushNew, Node: a.Nodes.State, Probe: a.Nodes.Probe, Go: a.bg.Go}
	a.Shots = shots.Service{Host: a.Host, Argv: cfg.Browser, Home: h, Base: cfg.MCPBase, Lend: a.Owners.Lend, Return: a.Owners.Return,
		Themed: a.Themes.Exists}
	a.Updates = updates.Service{St: st, Now: cfg.Now, Source: cfg.Releases, Version: cfg.Version, Commit: cfg.Commit, Variant: cfg.Variant, Image: cfg.Image,
		Exe: cfg.Exe, Restart: cfg.Restart, Pending: h.Update(), Go: a.bg.Go,
		Snapshot: func(ctx context.Context) error { _, err := a.Snapshots.Snapshot(ctx); return err }}
	a.Projects = project.Service{St: st, Emit: a.Bus.Emit, Now: cfg.Now, Home: h, Registry: a.Registry,
		Gateway: a.Gateway, Scheduler: a.Scheduler, Scripts: a.Scripts, LogPath: a.Runner.LogPath, StopServer: a.Host.Stop, Snapshot: a.Snapshots.BeforeDelete, Keep: a.Files.Keep,
		PrepareLater: a.Documents.PrepareLater, Noted: a.Feed.Noted, CheckScreens: a.Screens.CheckScreens,
		Unassigned: a.Work.Unassigned, Missing: a.missingPrograms, Secret: a.Secrets.Has, DropSecrets: a.Secrets.Drop,
		Theme: a.Themes.Current, Saw: a.Runner.Saw, Seen: a.Runner.Seen}
	a.addBuiltins()
	a.Browser.Load()
	a.Asks.OnLateAnswer(a.Answers.LateAnswer)
	if err := a.Secrets.Move(a.ctx); err != nil {
		return nil, err
	}
	// Sessions that did not move go on as new sessions with what deck tells them; deck starts all the same.
	if err := a.Providers.MoveSessions(a.ctx); err != nil {
		slog.Error("move sessions", "err", err)
	}
	if err := a.Documents.SeedSkills(a.ctx); err != nil {
		return nil, err
	}
	if err := a.Projects.SeedMaster(a.ctx); err != nil {
		return nil, err
	}
	a.Projects.CheckManifests(a.ctx)
	a.Themes.Settle(a.ctx)
	a.Work.Prune(a.ctx, 0, nil)
	a.Work.KeepBigOptions(a.ctx)
	a.Work.Follow(a.Bus)
	a.Health.Follow(a.Bus)
	a.Answers.Follow(a.Bus)
	a.Answers.FollowClosed(a.Bus)
	a.Files.Follow(a.Bus)
	a.Owners.FollowNodes(a.Bus)
	a.Health.Derive(a.ctx, 0, nil)
	if err := a.Updates.Mark(a.ctx); err != nil {
		return nil, err
	}
	if err := st.Tx(a.ctx, func(ctx context.Context, q *db.Queries) error { return health.Clear(ctx, q, health.Of{}, health.HTTPS) }); err != nil {
		return nil, err
	}
	box, boxText := a.Documents.Box()
	status, text := script.Health()
	if box == health.Error {
		status, text = health.Error, fmt.Sprintf("%s No script runs while deck cannot build the box of foreign programs: see the row sandbox.", text)
	}
	if err := health.Write(a.ctx, a.Store, health.Of{}, health.Scripts, status, text); err != nil {
		return nil, err
	}
	if err := health.Write(a.ctx, a.Store, health.Of{}, health.Sandbox, box, boxText); err != nil {
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
		a.Nodes.Close()
		a.Runner.Shutdown()
		a.cancel()
		a.bg.stop()
	})
}

// background is the work deck runs beside the calls, in the life of deck; once deck stops, none starts.
type background struct {
	ctx     context.Context
	mu      sync.Mutex
	wg      sync.WaitGroup
	stopped bool
}

// Go runs f beside the calls unless deck stops, and reports whether it runs.
func (b *background) Go(f func(ctx context.Context)) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.stopped {
		return false
	}
	b.wg.Go(func() { f(b.ctx) })
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
	a.bg.Go(func(ctx context.Context) { a.Loop(ctx, every) })
	a.bg.Go(a.Owners.Nodes)
}

// missingPrograms are the scripts of a project whose programs deck does not find, by script name; scripts that run on
// a node are not judged by deck's machine: the node refuses a run without its program.
func (a *App) missingPrograms(ctx context.Context, projectID int64, m *manifest.Manifest) map[string]error {
	if p, err := a.Folders.ScriptsAt(ctx, projectID); err != nil || p.On != nil {
		return map[string]error{}
	}
	return a.Scripts.Check(m)
}

// nodeTransport carries the calls of a connection over the connection of its node, named in a failure by its name.
func (a *App) nodeTransport(device int64) http.RoundTripper {
	name := fmt.Sprint(device)
	if d, err := a.Store.Q.NodeByID(context.Background(), device); err == nil {
		name = d.Name
	}
	return a.Nodes.Transport(device, name)
}

// Sweep checks every deadline against now, then pushes and runs the daily cleanup, snapshot and look for an update.
func (a *App) Sweep(ctx context.Context, now time.Time) {
	a.deadlines(ctx, now)
	a.slow(ctx, now)
	a.daily(ctx, now)
}

// deadlines are what is due by now: confirmations, ceilings, pauses, silent processes, results handed in by processes
// that still run, empty results kept long enough, files whose rows are gone, the disk.
func (a *App) deadlines(ctx context.Context, now time.Time) {
	a.Answers.ExpireAsks(ctx, now)
	a.Answers.Settle(ctx)
	a.Answers.Heard(ctx)
	a.Runner.Due(ctx, now)
	a.Processes.Due(ctx, now)
	a.Files.Drain(ctx)
	a.Snapshots.CheckDisk(ctx, now)
}

// slow is the work of the loop that waits on the network or the disk: pushes, within pushTimeout, and the light
// copies of pictures.
func (a *App) slow(ctx context.Context, now time.Time) {
	pctx, cancel := context.WithTimeout(ctx, pushTimeout)
	if spaces, err := a.Store.Q.ListWorkspaces(pctx); err == nil {
		for _, ws := range spaces {
			a.Feed.PushNew(pctx, ws)
		}
	}
	cancel()
	a.Themes.Thumbs(ctx)
	a.Files.Thumbs(ctx)
	a.Answers.Consent(ctx, now)
}

// daily is the work of the loop that may take minutes once a day: the cleanup and snapshot, and the look for an
// update; it runs beside slow, so a push does not wait for a snapshot.
func (a *App) daily(ctx context.Context, now time.Time) {
	a.Snapshots.Daily(ctx, now)
	a.Updates.Daily(ctx, now)
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
		a.aside(ctx, &a.slowRunning, now, a.slow)
		a.aside(ctx, &a.dailyRunning, now, a.daily)
		a.Providers.Checks(ctx, now)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// aside runs work in the background unless the last run of it still goes; running says it does.
func (a *App) aside(ctx context.Context, running *atomic.Bool, now time.Time, work func(context.Context, time.Time)) {
	if !running.CompareAndSwap(false, true) {
		return
	}
	if !a.bg.Go(func(context.Context) {
		defer running.Store(false)
		work(ctx, now)
	}) {
		running.Store(false)
	}
}

// Plain records that the owner reached deck at an address it does not sign in at, such as plain http from another
// machine: a passkey needs https, so the health row says how to put https in front of deck.
func (a *App) Plain(address string, port int) {
	text := fmt.Sprintf("deck is opened at %s, an address it does not sign the owner in at: a passkey needs https; on this machine run tailscale serve --bg %d and tailscale serve --bg --https=8443 %d, or put deck behind a domain with Caddy, then start deck with -public and that https address", address, port, port+1)
	if err := health.Write(context.Background(), a.Store, health.Of{}, health.HTTPS, health.Warning, text); err != nil {
		slog.Error("write health", "err", err)
	}
}

// Proxied says in health whether -proxy names the gateway of deck's network, as the compose of before did: deck takes
// no X-Forwarded-For from it.
func (a *App) Proxied(gateway bool) {
	err := a.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		if !gateway {
			return health.Clear(ctx, q, health.Of{}, health.Proxy)
		}
		_, err := health.Set(ctx, q, a.Store.Now(), health.Of{}, health.Proxy, health.Warning, "DECK_PROXY names gateway, and deck takes X-Forwarded-For from it no more: any container of the host comes from it; drop gateway from DECK_PROXY in .env or take compose.yaml of this release, and the clients of the host's tailscale serve count as one address")
		return err
	})
	if err != nil {
		slog.Error("write health", "err", err)
	}
}

// Tailnet says at the start whether deck joins the tailnet itself: on it, the log and a warning of health say that
// the built-in Tailscale goes away and how to move to the host's; off it, the rows of the tailnet go.
func (a *App) Tailnet(name string, port int) {
	ctx := context.Background()
	if name == "" {
		err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return errors.Join(health.Clear(ctx, q, health.Of{}, health.Tailscale), health.Clear(ctx, q, health.Of{}, health.BuiltinTailscale))
		})
		if err != nil {
			slog.Error("clear health", "err", err)
		}
		return
	}
	text := fmt.Sprintf("the built-in Tailscale goes away in the next releases: install Tailscale on the host and run tailscale serve --bg %d and tailscale serve --bg --https=8443 %d, start deck with -public and that https address (DECK_DOMAIN in .env of deck's compose), then pair the phone again at the new address", port, port+1)
	slog.Warn(text)
	if err := health.Write(ctx, a.Store, health.Of{}, health.BuiltinTailscale, health.Warning, text); err != nil {
		slog.Error("write health", "err", err)
	}
}

// TailnetHealth records the state of deck on the owner's tailnet: the sign-in link while it waits for one, its https
// address once it is on it.
func (a *App) TailnetHealth(status, text, address string) {
	a.Feed.Tailnet.Store(&address)
	if _, known := a.Auth.Origins().Named(address); address != "" && !known {
		a.Auth.Origins().Add(address, address+":8443")
		// The tailnet's https address is often the first a browser pairs at.
		if links, err := a.Owners.Welcome(context.Background(), a.Auth.Signed(context.Background())); err == nil {
			for _, l := range links {
				slog.Info("pair the first device", "link", l)
			}
		}
	}
	if err := health.Write(context.Background(), a.Store, health.Of{}, health.Tailscale, status, text); err != nil {
		slog.Error("write health", "err", err)
	}
}
