package app

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

type providerView struct {
	Name    string        `json:"name"`
	Caps    provider.Caps `json:"caps"`
	URL     string        `json:"url,omitempty"`
	Model   string        `json:"model,omitempty"`
	Status  string        `json:"status,omitempty"`
	Error   string        `json:"error,omitempty"`
	Version string        `json:"version,omitempty"`
	// Fits and Missing tell whether the provider suits the command that was asked about.
	Fits    *bool    `json:"fits,omitempty"`
	Missing []string `json:"missing,omitempty"`
}

func (a *App) providersList(ctx context.Context, act registry.Actor, in struct{ Command string }) (any, error) {
	all, err := a.providers(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := map[string]any{}
	var requires []string
	if in.Command != "" {
		cmd, err := a.agentCommand(ctx, act.Workspace, in.Command)
		if err != nil {
			return nil, err
		}
		requires = cmd.Requires
		out["requires"], out["suggested"] = cmd.Requires, cmd.Provider
		chosen, _ := a.Store.Q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: act.Workspace, Key: placePrefix + in.Command})
		out["chosen"] = chosen
		name, _, _ := strings.Cut(in.Command, ".")
		if p, err := a.Store.Q.GetProjectByName(ctx, name); err == nil {
			if pv, err := a.For(ctx, p.ID, cmd); err == nil {
				out["effective"] = pv.Name()
			} else {
				out["effective_error"] = err.Error()
			}
		}
	}
	views := make([]providerView, 0, len(all))
	for _, pv := range all {
		v := providerView{Name: pv.Name(), Caps: pv.Caps()}
		if o, ok := pv.(interface{ Endpoint() (string, string) }); ok {
			v.URL, v.Model = o.Endpoint()
		}
		if h, err := a.Store.Q.GetHealth(ctx, process.HealthSubject(pv.Name())); err == nil {
			v.Status, v.Error, v.Version = h.Status, h.Error, h.Version
		}
		if in.Command != "" {
			v.Missing = pv.Caps().Missing(requires)
			fits := len(v.Missing) == 0
			v.Fits = &fits
		}
		views = append(views, v)
	}
	out["providers"] = views
	return out, nil
}

func (a *App) providerSet(ctx context.Context, act registry.Actor, in struct{ Command, Provider string }) (any, error) {
	cmd, err := a.agentCommand(ctx, act.Workspace, in.Command)
	if err != nil {
		return nil, err
	}
	if in.Provider != "" {
		pv, err := a.provider(ctx, act.Workspace, in.Provider)
		if err != nil {
			return nil, err
		}
		if missing := pv.Caps().Missing(cmd.Requires); len(missing) > 0 {
			return nil, registry.Invalid("deck: provider %s does not fit %s: it lacks %v", in.Provider, in.Command, missing)
		}
	}
	if err := a.setPlace(ctx, act.Workspace, in.Command, in.Provider); err != nil {
		return nil, err
	}
	return map[string]any{"command": in.Command, "provider": in.Provider}, nil
}

func (a *App) providerAdd(ctx context.Context, act registry.Actor, in struct {
	Name, URL, Model string
	KeySecret        string `json:"key_secret"`
	NoCloud          bool   `json:"no_cloud"`
}) (any, error) {
	d := providerDef{URL: in.URL, Model: in.Model, KeySecret: in.KeySecret, NoCloud: in.NoCloud}
	if err := checkDef(in.Name, d, a.main.Name()); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(d)
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.SetSetting(ctx, db.SetSettingParams{WorkspaceID: act.Workspace, Key: providerPrefix + in.Name, Value: string(raw)})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"name": in.Name}, nil
}

func (a *App) providerRemove(ctx context.Context, act registry.Actor, in struct{ Name string }) (any, error) {
	var n int64
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		n, err = q.DeleteSetting(ctx, db.DeleteSettingParams{WorkspaceID: act.Workspace, Key: providerPrefix + in.Name})
		return err
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("deck: no provider %q to remove", in.Name)
	}
	return map[string]any{"removed": in.Name}, nil
}

func (a *App) providerCheck(ctx context.Context, act registry.Actor, in struct{ Provider string }) (any, error) {
	if _, ok := a.main.(provider.SelfChecker); !ok || in.Provider != a.main.Name() {
		return nil, registry.Invalid("deck: provider %q has no self-check", in.Provider)
	}
	if !a.startCheck(true) {
		return nil, errors.New("deck: a self-check is already running")
	}
	return map[string]any{"checking": in.Provider}, nil
}

func (a *App) llmCallsList(ctx context.Context, act registry.Actor, in struct {
	Project string
	Process int64
	Limit   int64
}) (any, error) {
	var pid int64
	if in.Project != "" || act.Kind == registry.Agent {
		p, err := a.scope(ctx, act, in.Project)
		if err != nil {
			return nil, err
		}
		pid = p.ID
	}
	if in.Limit <= 0 || in.Limit > 500 {
		in.Limit = 50
	}
	rows, err := a.Store.Q.ListLLMCalls(ctx, db.ListLLMCallsParams{WorkspaceID: act.Workspace, ProcessID: in.Process, ProjectID: pid, Lim: in.Limit})
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rows))
	for _, c := range rows {
		m := map[string]any{"id": c.ID, "process": c.ProcessID, "gen_ai.provider.name": c.GenAiProviderName,
			"gen_ai.request.model": c.GenAiRequestModel, "gen_ai.response.model": c.GenAiResponseModel,
			"gen_ai.usage.input_tokens": c.GenAiUsageInputTokens, "gen_ai.usage.output_tokens": c.GenAiUsageOutputTokens,
			"gen_ai.usage.cache_read.input_tokens":     c.GenAiUsageCacheReadInputTokens,
			"gen_ai.usage.cache_creation.input_tokens": c.GenAiUsageCacheCreationInputTokens,
			"duration_ms": c.DurationMs, "created_at": c.CreatedAt}
		if c.CostUsd.Valid {
			m["cost_usd"] = c.CostUsd.Float64
		}
		out = append(out, m)
	}
	return out, nil
}
