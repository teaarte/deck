package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/provider/openai"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

const (
	placePrefix    = "place:"
	providerPrefix = "provider:"
)

// providerDef is an OpenAI-compatible provider the owner added to a workspace.
type providerDef struct {
	URL       string `json:"url"`
	Model     string `json:"model"`
	KeySecret string `json:"key_secret,omitempty"`
	NoCloud   bool   `json:"no_cloud,omitempty"`
	// Agent runs the model in an ACP agent inside the sandbox of deck instead of deck's own tool loop.
	Agent string `json:"agent,omitempty"`
}

// Named is the provider of a workspace by name: a built-in one of deck or one the owner added.
func (a *App) Named(ctx context.Context, projectID int64, name string) (provider.Provider, error) {
	p, err := a.Store.Q.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	return a.provider(ctx, p.WorkspaceID, name)
}

func (a *App) provider(ctx context.Context, workspace int64, name string) (provider.Provider, error) {
	if pv := a.builtinNamed(name); pv != nil {
		return pv, nil
	}
	raw, err := a.Store.Q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: workspace, Key: providerPrefix + name})
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
		return a.Secret(workspace, d.KeySecret)
	}
	if d.Agent != "" {
		return acp.New(acp.Config{Name: name, Agent: d.Agent, Binary: a.Cfg.ACPBinaries[d.Agent], URL: d.URL, Model: d.Model,
			Key: key, NoCloud: d.NoCloud, Home: a.Cfg.Home, Sandbox: a.Cfg.Sandbox, CallLimit: a.Cfg.ACPCallLimit, Now: a.Cfg.Now})
	}
	return openai.New(openai.Config{Name: name, URL: d.URL, Model: d.Model, NoCloud: d.NoCloud, Now: a.Cfg.Now,
		Sessions: filepath.Join(a.Cfg.Home, "sessions"), Key: key}), nil
}

// named is a built-in provider, or one the owner added to any workspace; nil when there is none.
func (a *App) named(ctx context.Context, name string) provider.Provider {
	if pv := a.builtinNamed(name); pv != nil {
		return pv
	}
	spaces, _ := a.Store.Q.ListWorkspaces(ctx)
	for _, ws := range spaces {
		if pv, err := a.provider(ctx, ws, name); err == nil {
			return pv
		}
	}
	return nil
}

// checked are the providers with a self-check: the built-in ones and those the owner added to any workspace.
func (a *App) checked(ctx context.Context) []provider.Provider {
	var out []provider.Provider
	seen := map[string]bool{}
	all := slices.Clone(a.builtin)
	spaces, _ := a.Store.Q.ListWorkspaces(ctx)
	for _, ws := range spaces {
		added, _ := a.providers(ctx, ws)
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
func (a *App) providers(ctx context.Context, workspace int64) ([]provider.Provider, error) {
	rows, err := a.Store.Q.ListSettings(ctx, db.ListSettingsParams{WorkspaceID: workspace, Prefix: providerPrefix})
	if err != nil {
		return nil, err
	}
	out := slices.Clone(a.builtin)
	for _, r := range rows {
		if pv, err := a.provider(ctx, workspace, strings.TrimPrefix(r.Key, providerPrefix)); err == nil {
			out = append(out, pv)
		}
	}
	return out, nil
}

// For picks the provider a command starts on: the owner's setting, else the suggestion of the manifest,
// else the main provider, else any that fits. A setting that no longer fits refuses the start.
func (a *App) For(ctx context.Context, projectID int64, cmd *manifest.Command) (provider.Provider, error) {
	p, err := a.Store.Q.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	place := p.Name + "." + cmd.Name
	chosen, err := a.Store.Q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: p.WorkspaceID, Key: placePrefix + place})
	if err == nil {
		pv, err := a.provider(ctx, p.WorkspaceID, chosen)
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
	all, err := a.providers(ctx, p.WorkspaceID)
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

func (a *App) builtinNamed(name string) provider.Provider {
	for _, pv := range a.builtin {
		if pv.Name() == name {
			return pv
		}
	}
	return nil
}

// Ready holds turns on a built-in provider while its self-check runs after a change of version or is red.
func (a *App) Ready(ctx context.Context, name string) bool {
	if _, ok := a.named(ctx, name).(provider.SelfChecker); !ok {
		return true
	}
	h, err := a.Store.Q.GetHealth(ctx, process.HealthSubject(name))
	return err != nil || h.Status != "checking" && h.Status != "error"
}

// agentCommand finds an agent command of the workspace by its place, project.command.
func (a *App) agentCommand(ctx context.Context, workspace int64, place string) (*manifest.Command, error) {
	name, command, _ := strings.Cut(place, ".")
	p, err := a.Store.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: name})
	if err != nil {
		return nil, registry.NotFound("deck: no project %q", name)
	}
	m, err := docs.Manifest(ctx, a.Store.Q, p)
	if err != nil {
		return nil, err
	}
	cmd := m.Command(command)
	if cmd == nil || cmd.Executor.Type != process.KindAgent {
		return nil, registry.Invalid("deck: %s is not an agent command; only agent commands call a model", place)
	}
	return cmd, nil
}

func (a *App) setPlace(ctx context.Context, workspace int64, place, name string) error {
	return a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if name == "" {
			_, err := q.DeleteSetting(ctx, db.DeleteSettingParams{WorkspaceID: workspace, Key: placePrefix + place})
			return err
		}
		return q.SetSetting(ctx, db.SetSettingParams{WorkspaceID: workspace, Key: placePrefix + place, Value: name})
	})
}

func checkDef(name string, d providerDef, builtin []string) error {
	switch {
	case !projectName.MatchString(name) || slices.Contains(builtin, name):
		return registry.Invalid("deck: provider name %q: lowercase letters, digits and '-', not %s", name, strings.Join(builtin, " or "))
	case !strings.HasPrefix(d.URL, "http://") && !strings.HasPrefix(d.URL, "https://"):
		return registry.Invalid("deck: provider url must be http or https, got %q", d.URL)
	case strings.ContainsAny(d.URL, "@?#"):
		return registry.Invalid("deck: provider url takes no user, query or key; keep the API key in a secret and name it in key_secret")
	case strings.TrimSpace(d.Model) == "":
		return registry.Invalid("deck: provider needs a model")
	case d.KeySecret != "" && !secretName.MatchString(d.KeySecret):
		return registry.Invalid("deck: bad secret name %q", d.KeySecret)
	case d.Agent != "" && acp.Agents[d.Agent].Start == nil:
		return registry.Invalid("deck: no ACP agent %q; deck knows %s", d.Agent, strings.Join(acp.Names(), ", "))
	}
	return nil
}
