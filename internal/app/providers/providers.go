// Package providers keeps the model providers of deck: the built-in ones, those the owner adds per workspace, which
// command runs on which, and their self-checks.
package providers

import (
	"context"
	"database/sql"
	"deck2/internal/app/access"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"

	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const providerPrefix = "provider:"

// Service keeps the providers. Wake lets turns that wait for a provider go on once it is ready; Ctx and BG are
// the life of deck, for the self-checks it runs in the background.
type Service struct {
	St          *store.Store
	Bus         *events.Bus
	Now         func() time.Time
	Home        string
	Sandbox     sandbox.Sandbox
	ACPBinaries map[string]string
	// Rules are the setting of the old sandbox the ACP agents of a workspace run in.
	Rules            sandbox.Rules
	ACPCallLimit     time.Duration
	SelfCheckCeiling time.Duration
	Secret           func(workspace int64, name string) (string, error)
	Wake             func(ctx context.Context)
	// Builtin are the providers deck was started with, the main one first; the owner adds ACP ones per workspace.
	Builtin []provider.Provider
	Ctx     context.Context
	BG      *sync.WaitGroup

	mu    sync.Mutex
	check map[string]*checks
}

// providerDef is a provider the owner added to a workspace: an ACP agent on an OpenAI-compatible API.
type providerDef struct {
	URL       string `json:"url"`
	Model     string `json:"model"`
	KeySecret string `json:"key_secret,omitempty"`
	NoCloud   bool   `json:"no_cloud,omitempty"`
	// Agent is the ACP agent that runs the model inside the sandbox of deck.
	Agent string `json:"agent,omitempty"`
	// CheckMinutes bound its self-check; deck's own ceiling when zero.
	CheckMinutes int64 `json:"check_minutes,omitempty"`
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
	raw, err := s.St.Q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: workspace, Key: providerPrefix + name})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, registry.NotFound("deck: no provider %q; add it with deck.provider_add", name)
	}
	if err != nil {
		return nil, err
	}
	var d providerDef
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return nil, err
	}
	if strings.ContainsAny(d.URL, "@?#") {
		return nil, registry.Invalid("deck: the url of provider %q holds a user, query or key; add it again with deck.provider_add and key_secret", name)
	}
	key := func() (string, error) {
		if d.KeySecret == "" {
			return "", nil
		}
		return s.Secret(workspace, d.KeySecret)
	}
	if d.Agent == "" {
		return nil, registry.Invalid("deck: provider %q has no ACP agent; add it again with agent %s", name, strings.Join(acp.Names(), " or "))
	}
	return acp.New(acp.Config{Name: name, Agent: d.Agent, Binary: s.ACPBinaries[d.Agent], URL: d.URL, Model: d.Model,
		Key: key, NoCloud: d.NoCloud, Home: s.Home, Sandbox: s.Sandbox, CallLimit: s.ACPCallLimit, Now: s.Now,
		Open:         func() bool { return s.Rules.IsOpen(context.Background(), workspace) },
		CheckCeiling: time.Duration(d.CheckMinutes) * time.Minute})
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

// providers lists every provider of a workspace, the built-in ones first.
func (s *Service) All(ctx context.Context, workspace int64) ([]provider.Provider, error) {
	rows, err := s.St.Q.ListSettings(ctx, db.ListSettingsParams{WorkspaceID: workspace, Prefix: providerPrefix})
	if err != nil {
		return nil, err
	}
	out := slices.Clone(s.Builtin)
	for _, r := range rows {
		if pv, err := s.Provider(ctx, workspace, strings.TrimPrefix(r.Key, providerPrefix)); err == nil {
			out = append(out, pv)
		}
	}
	return out, nil
}

// For picks the provider a command starts on: the owner's setting, else the suggestion of the manifest,
// else the main provider, else any that fits. A setting that no longer fits refuses the start.
func (s *Service) For(ctx context.Context, projectID int64, cmd *manifest.Command) (provider.Provider, error) {
	p, err := s.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	place := p.Name + "." + cmd.Name
	chosen, err := s.St.Q.GetCommandProvider(ctx, db.GetCommandProviderParams{ProjectID: p.ID, Command: cmd.Name})
	if err == nil {
		pv, err := s.Provider(ctx, p.WorkspaceID, chosen)
		if err != nil {
			return nil, registry.Invalid("deck: %s is set to provider %q, which is gone; pick another with deck.provider_set", place, chosen)
		}
		if missing := pv.Caps().Missing(cmd.Requires); len(missing) > 0 {
			return nil, registry.Invalid("deck: provider %s set for %s lacks %s; pick another with deck.provider_set", chosen, place, strings.Join(missing, ", "))
		}
		return pv, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
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
	return nil, registry.Invalid("deck: no provider fits %s, which requires %s; add one with deck.provider_add", place, strings.Join(cmd.Requires, ", "))
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

// setPlace keeps the provider the owner chose for an agent command; empty goes back to the default.
func (s *Service) setPlace(ctx context.Context, project int64, command, name string) error {
	return s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if name == "" {
			return q.DeleteCommandProvider(ctx, db.DeleteCommandProviderParams{ProjectID: project, Command: command})
		}
		return q.SetCommandProvider(ctx, db.SetCommandProviderParams{ProjectID: project, Command: command, Provider: name})
	})
}

func checkDef(name string, d providerDef, builtin []string) error {
	switch {
	case !access.ProjectName.MatchString(name) || slices.Contains(builtin, name):
		return registry.Invalid("deck: provider name %q: lowercase letters, digits and '-', not %s", name, strings.Join(builtin, " or "))
	case !strings.HasPrefix(d.URL, "http://") && !strings.HasPrefix(d.URL, "https://"):
		return registry.Invalid("deck: provider url must be http or https, got %q", d.URL)
	case strings.ContainsAny(d.URL, "@?#"):
		return registry.Invalid("deck: provider url takes no user, query or key; keep the API key in a secret and name it in key_secret")
	case strings.TrimSpace(d.Model) == "":
		return registry.Invalid("deck: provider needs a model")
	case d.KeySecret != "" && !access.ValidSecret(d.KeySecret):
		return registry.Invalid("deck: bad secret name %q", d.KeySecret)
	case d.CheckMinutes < 0 || d.CheckMinutes > 60:
		return registry.Invalid("deck: check_minutes is 1 to 60, or 0 for deck's own ceiling")
	case acp.Agents[d.Agent].Start == nil:
		return registry.Invalid("deck: no ACP agent %q; deck knows %s", d.Agent, strings.Join(acp.Names(), ", "))
	}
	return nil
}
