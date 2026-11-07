// Package providers keeps who exists to do the work of deck: the built-in providers, the connections and models the
// owner adds per workspace, which of them are off or up, and their self-checks.
package providers

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"deck2/internal/home"
	"deck2/internal/models"
	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/provider/remote"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Service keeps the providers. Wake lets turns that wait for a provider go on once it is ready; Ctx and Go are
// the life of deck, for the self-checks it runs in the background.
type Service struct {
	St               *store.Store
	Now              func() time.Time
	Home             home.Layout
	Sandbox          *sandbox.Sandbox
	ACPBinaries      map[string]string
	ACPCallLimit     time.Duration
	SelfCheckCeiling time.Duration
	// WriteKey keeps the API key of a connection.
	WriteKey func(workspace int64, connection, key string) error
	Wake     func(ctx context.Context)
	// Builtin are the providers deck was started with, the main one first; the owner adds ACP ones per workspace.
	Builtin []provider.Provider
	Ctx     context.Context
	// Go runs work beside the calls; false once deck stops.
	Go func(func(context.Context)) bool
	// Reach connects to deck's MCP server as an agent turn does; a self-check fails when it does not answer.
	Reach func(ctx context.Context) error
	// TurnModel is deck's proxy of a model of an agent turn.
	TurnModel func(workspace int64, m db.Model, c db.Connection) func(provider.ModelHooks) http.Handler
	// Catalog reads the price and kind of a model from the list of its connection.
	Catalog func(ctx context.Context, workspace int64, c db.Connection, model string) (db.AddModelParams, bool)
	// CheckFiles refuses options that name a file which is no file of the model; KeepBig keeps their long values
	// as files of the model; View is a model as its form shows it.
	CheckFiles func(ctx context.Context, workspace int64, model string, o map[string]any) error
	KeepBig    func(ctx context.Context, workspace int64, m db.Model) (string, error)
	View       func(ctx context.Context, m db.Model, connection string) any
	// Unassign takes a model out of every assignment of a workspace, in the transaction of its removal.
	Unassign func(ctx context.Context, q *db.Queries, workspace int64, name string) error
	// Nodes are the connections of the nodes, whose agents run turns there; NodeWait is how long a turn whose node
	// went away waits for it, Low hears that files a turn brought back from a node fill deck's disk.
	Nodes    remote.Hub
	NodeWait time.Duration
	Low      func(ctx context.Context, text string)
	// NodeDenied hears that a node refused a folder its owner did not allow.
	NodeDenied func(device int64)

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
	if base, node, ok := provider.AtNode(name); ok {
		return s.onNode(ctx, workspace, base, node)
	}
	row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, registry.NotFound("no provider %q; add a model with an agent with deck.model_set", name)
	}
	if err != nil {
		return nil, err
	}
	m, c := row.Model, row.Connection
	if strings.ContainsAny(c.Url, "@?#") {
		return nil, registry.Invalid("the url of connection %q holds a user, query or key; set it again with deck.connection_set and key", c.Name)
	}
	if m.Agent == "" {
		return nil, registry.Invalid("model %q runs in no agent; set it again with deck.model_set and agent %s", name, strings.Join(s.agentNames(), " or "))
	}
	if c.Api == models.APICLI {
		cli, ok := s.builtinNamed(m.Agent).(provider.Modeled)
		if !ok {
			return nil, registry.Invalid("model %q runs in %s, which this deck does not run", name, m.Agent)
		}
		effort, err := cliEffort(m.Options)
		if err != nil {
			return nil, err
		}
		return cli.With(name, m.Model, effort), nil
	}
	a, err := s.agent(acp.Config{Name: name, Agent: m.Agent, Model: m.Model,
		NoCloud: c.Local != 0, CallLimit: s.ACPCallLimit, Now: s.Now,
		CheckCeiling: time.Duration(m.CheckMinutes) * time.Minute, API: s.TurnModel(workspace, m, c)})
	if err != nil {
		return nil, registry.Invalid("%v", err)
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
	return append(out, s.onNodes(ctx, workspace, out)...), nil
}

// Agent is a built-in provider by name, on a node too, such as claude-code@Mac; nil when there is none.
func (s *Service) Agent(name string) provider.Provider {
	if base, _, ok := provider.AtNode(name); ok && s.builtinNamed(base) != nil {
		return s.named(s.Ctx, name)
	}
	return s.builtinNamed(name)
}

func (s *Service) builtinNamed(name string) provider.Provider {
	for _, pv := range s.Builtin {
		if pv.Name() == name {
			return pv
		}
	}
	return nil
}

// Commands are the commands of providers: their self-checks, switches, connections and models.
func (s *Service) Commands() []*registry.Command {
	return slices.Concat(s.checkCommands(), s.offCommands(), s.connectionCommands(), s.modelCommands())
}
