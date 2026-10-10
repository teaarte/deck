// Package providers keeps the model providers of deck: the built-in ones, those the owner adds per workspace, which
// command runs on which, and their self-checks.
package providers

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
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

// Service keeps the providers. Wake lets turns that wait for a provider go on once it is ready; Ctx and BG are
// the life of deck, for the self-checks it runs in the background.
type Service struct {
	St               *store.Store
	Now              func() time.Time
	Home             home.Layout
	Sandbox          sandbox.Sandbox
	ACPBinaries      map[string]string
	ACPCallLimit     time.Duration
	SelfCheckCeiling time.Duration
	Secret           func(workspace int64, name string) (string, error)
	WriteSecret      func(workspace int64, name, value string) error
	Wake             func(ctx context.Context)
	// Builtin are the providers deck was started with, the main one first; the owner adds ACP ones per workspace.
	Builtin []provider.Provider
	Ctx     context.Context
	// Go runs work beside the calls; false once deck stops.
	Go func(func()) bool
	// Resolve finds a file an input points to for a process of a project.
	Resolve func(ctx context.Context, project, process int64, ref string) (files.Ref, error)

	mu    sync.Mutex
	check map[string]*checks
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
		return nil, registry.Invalid("deck: model %q runs in no agent; set it again with deck.model_set and agent %s", name, strings.Join(acp.Names(), " or "))
	}
	a, err := acp.New(acp.Config{Name: name, Agent: m.Agent, Binary: s.ACPBinaries[m.Agent], Model: m.Model,
		NoCloud: c.Local != 0, Home: s.Home, Sandbox: s.Sandbox, CallLimit: s.ACPCallLimit, Now: s.Now,
		CheckCeiling: time.Duration(m.CheckMinutes) * time.Minute, API: s.turnModel(workspace, m, c)})
	if err != nil {
		return nil, registry.Invalid("deck: %v", err)
	}
	return a, nil
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

// CommandModel is the model an agent command of a project starts on and the models assigned to it; empty for a
// command that is no agent.
func (s *Service) CommandModel(ctx context.Context, projectID int64, command string) (string, []string) {
	p, err := s.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return "", nil
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return "", nil
	}
	cmd := m.Command(command)
	if cmd == nil || cmd.Executor.Type != "agent" {
		return "", nil
	}
	assigned, _ := Assigned(ctx, s.St.Q, p.WorkspaceID, p.ID, command, RoleAgent)
	if pv, err := s.For(ctx, projectID, cmd); err == nil {
		return pv.Name(), assigned
	}
	return "", assigned
}

// For picks the provider a command starts on: the first of its assignment that fits and is up, else the suggestion of
// the manifest, else the main provider, else any that fits; an assignment none of which fits refuses the start.
func (s *Service) For(ctx context.Context, projectID int64, cmd *manifest.Command) (provider.Provider, error) {
	p, err := s.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	place := p.Name + "." + cmd.Name
	names, err := Assigned(ctx, s.St.Q, p.WorkspaceID, p.ID, cmd.Name, RoleAgent)
	if err != nil {
		return nil, err
	}
	if len(names) > 0 {
		var fits []provider.Provider
		var why []string
		for _, name := range names {
			pv, err := s.Provider(ctx, p.WorkspaceID, name)
			if err != nil {
				why = append(why, fmt.Sprintf("deck: %s is set to model %q, which does not run: %v; assign another with deck.assignment_set", place, name, err))
				continue
			}
			if missing := pv.Caps().Missing(cmd.Requires); len(missing) > 0 {
				why = append(why, fmt.Sprintf("deck: provider %s set for %s lacks %s; assign another with deck.assignment_set", name, place, strings.Join(missing, ", ")))
				continue
			}
			fits = append(fits, pv)
		}
		if len(fits) == 0 {
			return nil, registry.Invalid("%s", strings.Join(why, "; "))
		}
		for _, pv := range fits {
			if s.up(ctx, pv.Name()) {
				return pv, nil
			}
		}
		return fits[0], nil
	}
	all, err := s.All(ctx, p.WorkspaceID)
	if err != nil {
		return nil, err
	}
	if i := slices.IndexFunc(all, func(pv provider.Provider) bool { return pv.Name() == cmd.Provider }); i > 0 {
		suggested := all[i]
		all = append([]provider.Provider{suggested}, slices.Delete(all, i, i+1)...)
	}
	for _, pv := range all {
		if len(pv.Caps().Missing(cmd.Requires)) == 0 {
			return pv, nil
		}
	}
	return nil, registry.Invalid("deck: no provider fits %s, which requires %s; add a model with an agent with deck.model_set", place, strings.Join(cmd.Requires, ", "))
}

// up reports whether a provider's health lets a new turn start on it rather than on a fallback: not red, not
// waiting for a login, not paused by a limit.
func (s *Service) up(ctx context.Context, name string) bool {
	h, err := s.St.Q.GetHealth(ctx, process.HealthSubject(name))
	return err != nil || h.Status != "error" && h.Status != "needs_login" && h.Status != "paused"
}

func (s *Service) builtinNamed(name string) provider.Provider {
	for _, pv := range s.Builtin {
		if pv.Name() == name {
			return pv
		}
	}
	return nil
}

// Ready holds turns on a built-in provider while its self-check runs or finds the adapter broken (a lost login
// waits by its own ask); a model that fails or is slow holds none.
func (s *Service) Ready(ctx context.Context, name string) bool {
	if _, ok := s.named(ctx, name).(provider.SelfChecker); !ok {
		return true
	}
	h, err := s.St.Q.GetHealth(ctx, process.HealthSubject(name))
	return err != nil || h.Status != "checking" && (h.Status != "error" || h.Verdict == "model")
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
