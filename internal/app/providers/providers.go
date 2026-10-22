// Package providers keeps the model providers of deck: the built-in ones, those the owner adds per workspace, which
// command runs on which, and their self-checks.
package providers

import (
	"context"
	"database/sql"
	"deck2/internal/docs"
	"deck2/internal/files"
	"deck2/internal/health"
	"deck2/internal/home"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Service keeps the providers. Wake lets turns that wait for a provider go on once it is ready; Ctx and Go are
// the life of deck, for the self-checks it runs in the background.
type Service struct {
	St               *store.Store
	Now              func() time.Time
	Home             home.Layout
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
	Go func(func()) bool
	// Resolve finds a file an input points to for a process of a project.
	Resolve func(ctx context.Context, project, process int64, ref string) (files.Ref, error)
	// Reach connects to deck's MCP server as an agent turn does; a self-check fails when it does not answer.
	Reach func(ctx context.Context) error
	// Words are deck's phrases in a language, for the prices of models.
	Words func(lang string) map[string]string

	mu    sync.Mutex
	check map[string]*checks
	// plain are the models whose API refused the reasoning an agent hands back, by address and model, while deck runs.
	plain sync.Map
	// found is set once the ACP agents were looked for.
	found bool
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

// checked are the providers with a self-check: the built-in ones and those the owner added to any workspace.
func (s *Service) checked(ctx context.Context) []provider.Provider {
	var out []provider.Provider
	seen := map[string]bool{}
	all := slices.Clone(s.Builtin)
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

// All lists every provider of a workspace, the built-in ones first, then the models that run in an agent.
func (s *Service) All(ctx context.Context, workspace int64) ([]provider.Provider, error) {
	rows, err := s.St.Q.ListModels(ctx, workspace)
	if err != nil {
		return nil, err
	}
	out := slices.Clone(s.Builtin)
	for _, r := range rows {
		if r.Model.Agent == "" {
			continue
		}
		if pv, err := s.Provider(ctx, workspace, r.Model.Name); err == nil {
			out = append(out, pv)
		}
	}
	return out, nil
}

// For picks the provider a command starts on: the first of its assignment that fits and is up, else the suggestion of
// the manifest, else the main provider, else any that fits; an assignment none of which fits refuses the start.
func (s *Service) For(ctx context.Context, projectID int64, cmd *manifest.Command) (provider.Provider, error) {
	ws, fits, assigned, err := s.fitting(ctx, projectID, cmd)
	if err != nil {
		return nil, err
	}
	for _, pv := range fits {
		if !assigned || s.Up(ctx, ws, pv.Name(), true) {
			return pv, nil
		}
	}
	return fits[0], nil
}

// Next is the provider of a command's assignment after the one that refused its turn, the first that fits and is up;
// empty when the owner assigned the command no other.
func (s *Service) Next(ctx context.Context, projectID int64, cmd *manifest.Command, after string) string {
	ws, fits, assigned, err := s.fitting(ctx, projectID, cmd)
	if err != nil || !assigned {
		return ""
	}
	if i := slices.IndexFunc(fits, func(pv provider.Provider) bool { return pv.Name() == after }); i >= 0 {
		fits = fits[i+1:]
	}
	for _, pv := range fits {
		if pv.Name() != after && s.Up(ctx, ws, pv.Name(), true) {
			return pv.Name()
		}
	}
	return ""
}

// Able are the providers of a workspace that have what a command requires, whether it is assigned to them or not.
func (s *Service) Able(ctx context.Context, workspace int64, cmd *manifest.Command) []string {
	all, _ := s.All(ctx, workspace)
	out := []string{}
	for _, pv := range all {
		if len(pv.Caps().Missing(cmd.Needs())) == 0 {
			out = append(out, pv.Name())
		}
	}
	return out
}

// Fits are the providers a command may start on that are up: those of its assignment in order, else all that fit.
func (s *Service) Fits(ctx context.Context, projectID int64, cmd *manifest.Command) []string {
	ws, fits, _, err := s.fitting(ctx, projectID, cmd)
	if err != nil {
		return nil
	}
	var out []string
	for _, pv := range fits {
		if s.Up(ctx, ws, pv.Name(), true) {
			out = append(out, pv.Name())
		}
	}
	return out
}

// fitting are the providers that run and have what a command requires, at least one: those of its assignment in
// order, else all with the suggestion of the manifest first; assigned says they come from an assignment.
func (s *Service) fitting(ctx context.Context, projectID int64, cmd *manifest.Command) (int64, []provider.Provider, bool, error) {
	p, err := s.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return 0, nil, false, err
	}
	place, requires := p.Name+"."+cmd.Name, cmd.Needs()
	names, err := Assigned(ctx, s.St.Q, p.WorkspaceID, p.ID, cmd.Name, RoleAgent)
	if err != nil {
		return 0, nil, false, err
	}
	var fits []provider.Provider
	if len(names) > 0 {
		var why []string
		for _, name := range names {
			pv, err := s.Provider(ctx, p.WorkspaceID, name)
			if err != nil {
				why = append(why, fmt.Sprintf("deck: %s is set to model %q, which does not run: %v; %s", place, name, err, s.change(ctx, p.WorkspaceID, requires)))
				continue
			}
			if missing := pv.Caps().Missing(requires); len(missing) > 0 {
				why = append(why, fmt.Sprintf("deck: model %s set for %s lacks %s; %s", name, place, strings.Join(missing, ", "), s.change(ctx, p.WorkspaceID, requires)))
				continue
			}
			fits = append(fits, pv)
		}
		if len(fits) == 0 {
			return 0, nil, false, registry.Invalid("%s", strings.Join(why, "; "))
		}
		return p.WorkspaceID, fits, true, nil
	}
	all, err := s.All(ctx, p.WorkspaceID)
	if err != nil {
		return 0, nil, false, err
	}
	if i := slices.IndexFunc(all, func(pv provider.Provider) bool { return pv.Name() == cmd.Provider }); i > 0 {
		suggested := all[i]
		all = append([]provider.Provider{suggested}, slices.Delete(all, i, i+1)...)
	}
	for _, pv := range all {
		if len(pv.Caps().Missing(requires)) == 0 {
			fits = append(fits, pv)
		}
	}
	if len(fits) == 0 {
		return 0, nil, false, registry.Invalid("deck: no provider fits %s, which requires %s; %s", place, strings.Join(requires, ", "), s.change(ctx, p.WorkspaceID, requires))
	}
	return p.WorkspaceID, fits, false, nil
}

// change says where the owner changes the model of a command: the models of the workspace that have what it
// requires, or with none of them, that one is added.
func (s *Service) change(ctx context.Context, workspace int64, requires []string) string {
	all, _ := s.All(ctx, workspace)
	var names []string
	for _, pv := range all {
		if len(pv.Caps().Missing(requires)) == 0 {
			names = append(names, pv.Name())
		}
	}
	if len(names) == 0 {
		return "add a model that has it in Settings → Models"
	}
	return "pick one that has it in the row of the command in the settings of the module: " + strings.Join(names, ", ")
}

// Up reports whether a turn may start on a provider: not in a self-check, a broken adapter or a limit; a lost login or a
// weak model holds no turn, but a new start, to pick, takes the next model that is up.
func (s *Service) Up(ctx context.Context, workspace int64, name string, pick bool) bool {
	h, err := s.healthOf(ctx, workspace, name)
	if err != nil {
		return true
	}
	switch h.Status {
	case health.Checking, health.Paused:
		return false
	case health.Error:
		return !pick && h.Verdict != "adapter"
	case health.NeedsLogin:
		return !pick
	}
	return true
}

// healthOf is the health row of a model by name: of its self-check when it is built in or runs in an agent, else of
// its last call in the workspace.
func (s *Service) healthOf(ctx context.Context, workspace int64, name string) (db.Health, error) {
	if row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name}); err == nil && row.Model.Agent == "" {
		return health.Get(ctx, s.St.Q, health.Of{Workspace: workspace}, health.Model(name))
	}
	return health.Get(ctx, s.St.Q, health.Of{}, health.Provider(name))
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
			registry.Everyone, s.modelsList).On("models", "assignments", "projects", "health"),
		s.assignmentCommand(),
		registry.Def("connection_set", "Set connection",
			"Adds or changes a connection models are reached through: its API, address and key.",
			registry.OwnerOnly, s.connectionSet),
		registry.Def("connection_remove", "Remove connection", "Removes a connection that has no models left.",
			registry.OwnerOnly, s.connectionRemove),
		s.modelSetCommand(),
		registry.Def("model_remove", "Remove model", "Removes a model; assignments that name it skip it.",
			registry.OwnerOnly, s.modelRemove),
		s.modelCallCommand(),
	}, s.webCommands()...)
}
