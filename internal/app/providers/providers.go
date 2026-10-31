// Package providers keeps the model providers of deck: the built-in ones, those the owner adds per workspace, which
// command runs on which, and their self-checks.
package providers

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"deck2/internal/docs"
	"deck2/internal/files"
	"deck2/internal/home"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Service keeps the providers. Wake lets turns that wait for a provider go on once it is ready; Ctx and Go are
// the life of deck, for the self-checks it runs in the background.
type Service struct {
	St   *store.Store
	Now  func() time.Time
	Home home.Layout
	// FilesMu keeps a snapshot from copying the base between the row of a file of a model and its blob.
	FilesMu          *sync.RWMutex
	Sandbox          sandbox.Sandbox
	ACPBinaries      map[string]string
	ACPCallLimit     time.Duration
	SelfCheckCeiling time.Duration
	// VideoPoll is how often a job of a video model is asked after, VideoCeiling how long deck waits for it.
	VideoPoll, VideoCeiling time.Duration
	Secret                  func(workspace int64, name string) (string, error)
	WriteSecret             func(workspace int64, name, value string) error
	Wake                    func(ctx context.Context)
	// Builtin are the providers deck was started with, the main one first; the owner adds ACP ones per workspace.
	Builtin []provider.Provider
	Ctx     context.Context
	// Go runs work beside the calls; false once deck stops.
	Go func(func(context.Context)) bool
	// Resolve finds a file an input points to for a process of a project.
	Resolve func(ctx context.Context, project, process int64, ref string) (files.Ref, error)
	// Reach connects to deck's MCP server as an agent turn does; a self-check fails when it does not answer.
	Reach func(ctx context.Context) error
	// Changed tells the clients a topic changed that no write of the store announced, such as a sign-in from deck.
	Changed func(topic string)

	mu    sync.Mutex
	check map[string]*checks
	// plain are the models whose API refused the reasoning an agent hands back, by address and model, while deck runs.
	plain sync.Map
	// found is set once the ACP agents were looked for.
	found bool
	// logins are the sign-ins of CLI agents from deck, by provider.
	logins map[string]*signIn
}

// Named is the provider of a workspace by name: a built-in one of deck or one the owner added.
func (s *Service) Named(ctx context.Context, projectID int64, name string) (provider.Provider, error) {
	p, err := s.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return s.Provider(ctx, p.WorkspaceID, name)
}

func (s *Service) Provider(ctx context.Context, workspace int64, name string) (provider.Provider, error) {
	if pv := s.builtinNamed(name); pv != nil {
		return pv, nil
	}
	row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, registry.NotFound("deck: no provider %q; add a model with an agent with deck.model_set", name)
	}
	if err != nil {
		return nil, err
	}
	m, c := row.Model, row.Connection
	if strings.ContainsAny(c.Url, "@?#") {
		return nil, registry.Invalid("deck: the url of connection %q holds a user, query or key; set it again with deck.connection_set and key", c.Name)
	}
	if m.Agent == "" {
		return nil, registry.Invalid("deck: model %q runs in no agent; set it again with deck.model_set and agent %s", name, strings.Join(s.agentNames(), " or "))
	}
	if c.Api == APICLI {
		cli, ok := s.builtinNamed(m.Agent).(provider.Modeled)
		if !ok {
			return nil, registry.Invalid("deck: model %q runs in %s, which this deck does not run", name, m.Agent)
		}
		effort, err := cliEffort(m.Options)
		if err != nil {
			return nil, err
		}
		return cli.With(name, m.Model, effort), nil
	}
	a, err := acp.New(acp.Config{Name: name, Agent: m.Agent, Binary: s.ACPBinaries[m.Agent], Model: m.Model,
		NoCloud: c.Local != 0, Home: s.Home, Sandbox: s.Sandbox, CallLimit: s.ACPCallLimit, Now: s.Now,
		CheckCeiling: time.Duration(m.CheckMinutes) * time.Minute, API: s.turnModel(workspace, m, c), Plain: s.plainOf(c.Url + " " + m.Model)})
	if err != nil {
		return nil, registry.Invalid("deck: %v", err)
	}
	return a, nil
}

// plainOf is the flag of a model whose API takes no reasoning back, kept while deck runs.
func (s *Service) plainOf(key string) *atomic.Bool {
	v, _ := s.plain.LoadOrStore(key, &atomic.Bool{})
	return v.(*atomic.Bool)
}

// named is a built-in provider, or one the owner added to any workspace; nil when there is none.
func (s *Service) named(ctx context.Context, name string) provider.Provider {
	if pv := s.builtinNamed(name); pv != nil {
		return pv
	}
	spaces, _ := s.St.Q.ListWorkspaces(ctx)
	for _, ws := range spaces {
		if pv, err := s.Provider(ctx, ws, name); err == nil {
			return pv
		}
	}
	return nil
}

// checked are the providers with a self-check: the built-in ones some workspace uses and those the owner added.
func (s *Service) checked(ctx context.Context) []provider.Provider {
	var out []provider.Provider
	seen := map[string]bool{}
	var all []provider.Provider
	for _, pv := range s.Builtin {
		if !s.resting(ctx, pv) {
			all = append(all, pv)
		}
	}
	spaces, _ := s.St.Q.ListWorkspaces(ctx)
	for _, ws := range spaces {
		added, _ := s.All(ctx, ws)
		all = append(all, added...)
	}
	for _, pv := range all {
		if _, ok := pv.(provider.SelfChecker); ok && !seen[pv.Name()] {
			seen[pv.Name()] = true
			out = append(out, pv)
		}
	}
	return out
}

// All lists every provider of a workspace that is on, the built-in ones first, then the models that run in an agent.
func (s *Service) All(ctx context.Context, workspace int64) ([]provider.Provider, error) {
	rows, err := s.St.Q.ListModels(ctx, workspace)
	if err != nil {
		return nil, err
	}
	var out []provider.Provider
	for _, pv := range s.Builtin {
		if s.Off(ctx, workspace, pv.Name()) == "" {
			out = append(out, pv)
		}
	}
	for _, r := range rows {
		if r.Model.Agent == "" || s.Off(ctx, workspace, r.Model.Name) != "" {
			continue
		}
		if pv, err := s.Provider(ctx, workspace, r.Model.Name); err == nil {
			out = append(out, pv)
		}
	}
	return out, nil
}

func (s *Service) builtinNamed(name string) provider.Provider {
	for _, pv := range s.Builtin {
		if pv.Name() == name {
			return pv
		}
	}
	return nil
}

// agentCommand finds an agent command of the workspace by its place, project.command.
func (s *Service) agentCommand(ctx context.Context, workspace int64, place string) (db.Project, *manifest.Command, error) {
	name, command, _ := strings.Cut(place, ".")
	p, err := s.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: name})
	if err != nil {
		return p, nil, registry.NotFound("deck: no project %q", name)
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return p, nil, err
	}
	cmd := m.Command(command)
	if cmd == nil || cmd.Executor.Type != process.KindAgent {
		return p, nil, registry.Invalid("deck: %s is not an agent command; only agent commands call a model", place)
	}
	return p, cmd, nil
}

type providerCheckIn struct {
	Provider string `desc:"Provider name"`
}

func (s *Service) providerCheck(ctx context.Context, act registry.Actor, in providerCheckIn) (any, error) {
	pv, err := s.Provider(ctx, act.Workspace, in.Provider)
	if err != nil {
		return nil, err
	}
	if _, ok := pv.(provider.SelfChecker); !ok {
		return nil, registry.Invalid("deck: provider %q has no self-check", in.Provider)
	}
	if !s.startCheck(in.Provider, true) {
		if s.Ctx.Err() != nil {
			return nil, errors.New("deck: deck is stopping")
		}
		return nil, errors.New("deck: a self-check is already running")
	}
	return map[string]any{"checking": in.Provider}, nil
}

// Commands are the commands of providers.
func (s *Service) Commands() []*registry.Command {
	return append([]*registry.Command{
		registry.Def("provider_check", "Check provider",
			"Runs the self-check of a provider's adapter again; its health shows the outcome.",
			registry.OwnerOnly, s.providerCheck),
		registry.Read("models_list", "Models",
			"Lists the connections models are reached through, the models with their kind and price, and which models do which work.",
			registry.Everyone, s.modelsList).On("models", "assignments", "projects", "health", "settings"),
		registry.Def("agent_login", "Sign in to agent",
			"Starts the sign-in of a built-in CLI agent (claude-code, codex) on this machine by a link: gives the link and the code to enter there, or that the page of the link gives a code to hand in with agent_login_code; the self-check runs once it is signed in.",
			registry.OwnerOnly, s.agentLogin),
		registry.Def("agent_login_code", "Sign-in code",
			"Hands the sign-in of a CLI agent started with agent_login the code the page of its link gave.",
			registry.OwnerOnly, s.agentLoginCode),
		registry.Read("agent_logins", "Sign-ins",
			"Lists the sign-ins of CLI agents started from deck that wait for the owner, with their link and code, and those that failed.",
			registry.OwnerOnly, s.agentLogins).On("health"),
		registry.Def("provider_off", "Turn provider off",
			"Turns a built-in provider (claude-code, codex) off or on again: off, deck does not check it, gives it no work and offers none of its models; a provider whose program is not installed is off by itself.",
			registry.OwnerOnly, s.providerOff),
		s.assignmentCommand(),
		registry.Def("connection_set", "Set connection",
			"Adds or changes a connection models are reached through: its API, address and key.",
			registry.OwnerOnly, s.connectionSet),
		registry.Def("connection_remove", "Remove connection", "Removes a connection that has no models left.",
			registry.OwnerOnly, s.connectionRemove),
		s.modelSetCommand(),
		registry.Def("model_remove", "Remove model", "Removes a model and takes it out of the assignments that name it.",
			registry.OwnerOnly, s.modelRemove),
		s.modelCallCommand(),
	}, s.webCommands()...)
}
