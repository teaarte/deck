package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"
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

	manifestMu sync.Mutex
}

var projectName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,39}$`)

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
	a.Runner = process.NewRunner(st, a.Gateway, a.Bus, p, filepath.Join(cfg.Home, "runs"), cfg.MCPBase, a.Manifest)
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
	out, err := a.Registry.Invoke(ctx, registry.Actor{Kind: registry.Trigger, ProjectID: projectID}, p.Name+"."+command, raw)
	if err != nil {
		return 0, err
	}
	return out.(Started).Process, nil
}

type Started struct {
	Process int64 `json:"process"`
}

func (a *App) projectCommands(ctx context.Context, name string) ([]*registry.Command, error) {
	var ps []db.Project
	if name == "" {
		all, err := a.Store.Q.ListProjects(ctx)
		if err != nil {
			return nil, err
		}
		ps = all
	} else {
		p, err := a.Store.Q.GetProjectByName(ctx, name)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		ps = []db.Project{p}
	}
	var out []*registry.Command
	for _, p := range ps {
		m, err := a.Manifest(ctx, p.ID)
		if err != nil {
			slog.Error("project manifest", "project", p.Name, "err", err)
			continue
		}
		for _, c := range m.Commands {
			cmd := &registry.Command{
				Name: p.Name + "." + c.Name, Title: c.Title, Description: c.Description, Class: c.Class,
				Input: json.RawMessage(`{"type":"object"}`), Result: c.ResultSchema, ProjectID: p.ID,
				Callers: []string{registry.Owner, registry.Master, registry.Agent, registry.Trigger},
				Handler: a.startHandler(p.ID, c.Name),
			}
			if err := registry.Prepare(cmd); err != nil {
				slog.Error("project command", "command", cmd.Name, "err", err)
				continue
			}
			out = append(out, cmd)
		}
	}
	return out, nil
}

func (a *App) startHandler(projectID int64, command string) registry.Handler {
	return func(ctx context.Context, _ registry.Actor, raw json.RawMessage) (any, error) {
		input := map[string]any{}
		_ = json.Unmarshal(raw, &input)
		id, err := a.Runner.Start(ctx, projectID, command, input)
		if err != nil {
			return nil, err
		}
		return Started{Process: id}, nil
	}
}

// CreateProject validates the manifest, stores it as the first revision and registers its triggers.
func (a *App) CreateProject(ctx context.Context, act registry.Actor, name string, raw []byte, prompt string) (db.Project, error) {
	if !projectName.MatchString(name) || name == registry.Builtin {
		return db.Project{}, registry.Invalid("deck: project name %q must be lowercase letters, digits and '-', start with a letter, and not be %q", name, registry.Builtin)
	}
	if _, err := a.Store.Q.GetProjectByName(ctx, name); err == nil {
		return db.Project{}, registry.Invalid("deck: project %q already exists", name)
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		return db.Project{}, registry.Invalid("deck: %v", err)
	}
	var p db.Project
	err = a.Store.Tx(ctx, func(q *db.Queries) error {
		if p, err = q.CreateProject(ctx, db.CreateProjectParams{Name: name, Status: "enabled"}); err != nil {
			return err
		}
		if _, _, err := a.SaveDocument(ctx, q, DocRef{ProjectID: p.ID, Name: ManifestName}, KindManifest, string(raw), act.Author(), "create project"); err != nil {
			return err
		}
		if prompt != "" {
			if _, _, err := a.SaveDocument(ctx, q, DocRef{ProjectID: p.ID, Name: PromptName}, KindPrompt, prompt, act.Author(), "create project"); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return db.Project{}, err
	}
	if err := a.Scheduler.Sync(ctx, p.ID, m); err != nil {
		return db.Project{}, err
	}
	a.Bus.Publish("projects")
	return p, nil
}

// SaveManifest validates a manifest, stores it as a new revision and resyncs triggers.
func (a *App) SaveManifest(ctx context.Context, act registry.Actor, p db.Project, raw []byte, reason string) (int64, error) {
	m, err := manifest.Parse(raw)
	if err != nil {
		return 0, registry.Invalid("deck: %v", err)
	}
	a.manifestMu.Lock()
	defer a.manifestMu.Unlock()
	var n int64
	err = a.Store.Tx(ctx, func(q *db.Queries) error {
		_, n, err = a.SaveDocument(ctx, q, DocRef{ProjectID: p.ID, Name: ManifestName}, KindManifest, string(raw), act.Author(), reason)
		return err
	})
	if err != nil {
		return 0, err
	}
	if err := a.Scheduler.Sync(ctx, p.ID, m); err != nil {
		return n, err
	}
	a.Bus.Publish("projects")
	return n, nil
}

func (a *App) Manifest(ctx context.Context, projectID int64) (*manifest.Manifest, error) {
	raw, err := a.DocumentText(ctx, projectID, KindManifest, ManifestName)
	if err != nil {
		return nil, err
	}
	return manifest.Parse([]byte(raw))
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
