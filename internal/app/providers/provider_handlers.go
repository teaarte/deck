package providers

import (
	"context"
	"deck2/internal/app/access"
	"encoding/json"
	"errors"

	"deck2/internal/health"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

type providerView struct {
	Name string `json:"name"`
	// Agent is the ACP agent a provider added with one runs.
	Agent string        `json:"agent"`
	Caps  provider.Caps `json:"caps"`
	URL   string        `json:"url"`
	Model string        `json:"model"`
	// Key is how a provider the owner added passes its API key: the secret it names, or none.
	Key     string `json:"key,omitempty"`
	Status  string `json:"status"`
	Error   string `json:"error"`
	Version string `json:"version"`
	// Verdict of a failed self-check: model when the model does not manage it, adapter when it broke.
	Verdict string  `json:"verdict"`
	Speed   float64 `json:"speed"`
	// Builtin is a provider deck was started with, which the owner cannot remove; SelfCheck is one deck checks.
	Builtin   bool `json:"builtin"`
	SelfCheck bool `json:"self_check"`
	// Fits and Missing tell whether the provider suits the command that was asked about.
	Fits    *bool    `json:"fits,omitempty"`
	Missing []string `json:"missing,omitempty"`
}

// providersOut are the providers of the workspace and, for a command asked about, what it needs and runs on.
type providersOut struct {
	Providers      []providerView `json:"providers"`
	Requires       []string       `json:"requires,omitempty"`
	Suggested      string         `json:"suggested,omitempty"`
	Chosen         string         `json:"chosen,omitempty"`
	Effective      string         `json:"effective,omitempty"`
	EffectiveError string         `json:"effective_error,omitempty"`
}

type providersListIn struct {
	Command string `opt:"" desc:"Agent command, project.command"`
}

func (s *Service) providersList(ctx context.Context, act registry.Actor, in providersListIn) (providersOut, error) {
	var out providersOut
	all, err := s.All(ctx, act.Workspace)
	if err != nil {
		return out, err
	}
	if in.Command != "" {
		p, cmd, err := s.agentCommand(ctx, act.Workspace, in.Command)
		if err != nil {
			return out, err
		}
		out.Requires, out.Suggested = cmd.Requires, cmd.Provider
		out.Chosen, _ = s.St.Q.GetCommandProvider(ctx, db.GetCommandProviderParams{ProjectID: p.ID, Command: cmd.Name})
		if pv, err := s.For(ctx, p.ID, cmd); err == nil {
			out.Effective = pv.Name()
		} else {
			out.EffectiveError = err.Error()
		}
	}
	out.Providers = make([]providerView, 0, len(all))
	for _, pv := range all {
		_, checked := pv.(provider.SelfChecker)
		v := providerView{Name: pv.Name(), Caps: pv.Caps(), Builtin: s.builtinNamed(pv.Name()) != nil, SelfCheck: checked}
		if o, ok := pv.(interface{ Endpoint() (string, string) }); ok {
			v.URL, v.Model = o.Endpoint()
		}
		if o, ok := pv.(interface{ AgentName() string }); ok {
			v.Agent = o.AgentName()
		}
		if raw, err := s.St.Q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: act.Workspace, Key: providerPrefix + pv.Name()}); err == nil && !v.Builtin {
			var d providerDef
			_ = json.Unmarshal([]byte(raw), &d)
			v.Key = "none"
			if d.KeySecret != "" {
				v.Key = "secret " + d.KeySecret
			}
		}
		if h, err := s.St.Q.GetHealth(ctx, process.HealthSubject(pv.Name())); err == nil {
			v.Status, v.Error, v.Version, v.Verdict, v.Speed = h.Status, h.Error, h.Version, h.Verdict, h.Speed
		}
		if in.Command != "" {
			v.Missing = pv.Caps().Missing(out.Requires)
			fits := len(v.Missing) == 0
			v.Fits = &fits
		}
		out.Providers = append(out.Providers, v)
	}
	return out, nil
}

type providerSetIn struct {
	Command  string `desc:"Agent command, project.command"`
	Provider string `opt:"" desc:"Provider name; empty for the default"`
}

func (s *Service) providerSet(ctx context.Context, act registry.Actor, in providerSetIn) (any, error) {
	p, cmd, err := s.agentCommand(ctx, act.Workspace, in.Command)
	if err != nil {
		return nil, err
	}
	if in.Provider != "" {
		pv, err := s.Provider(ctx, act.Workspace, in.Provider)
		if err != nil {
			return nil, err
		}
		if missing := pv.Caps().Missing(cmd.Requires); len(missing) > 0 {
			return nil, registry.Invalid("deck: provider %s does not fit %s: it lacks %v", in.Provider, in.Command, missing)
		}
	}
	if err := s.setPlace(ctx, p.ID, cmd.Name, in.Provider); err != nil {
		return nil, err
	}
	return map[string]any{"command": in.Command, "provider": in.Provider}, nil
}

type providerAddIn struct {
	Name      string `desc:"Provider name: lowercase letters, digits and '-'"`
	URL       string `desc:"Base URL of the OpenAI-compatible API, such as http://localhost:11434/v1"`
	Model     string `desc:"Model name"`
	KeySecret string `json:"key_secret" opt:"" desc:"Name of the secret with the API key"`
	NoCloud   bool   `json:"no_cloud" opt:"" desc:"The model runs on the owner's own hardware"`
	Agent     string `desc:"The ACP agent that runs the model inside the sandbox of deck with a shell and files"`
}

func (s *Service) providerAdd(ctx context.Context, act registry.Actor, in providerAddIn) (any, error) {
	d := providerDef{URL: in.URL, Model: in.Model, KeySecret: in.KeySecret, NoCloud: in.NoCloud, Agent: in.Agent}
	var builtin []string
	for _, pv := range s.Builtin {
		builtin = append(builtin, pv.Name())
	}
	if err := checkDef(in.Name, d, builtin); err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(d)
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		spaces, err := q.ListWorkspaces(ctx)
		if err != nil {
			return err
		}
		for _, ws := range spaces {
			if _, err := q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: ws, Key: providerPrefix + in.Name}); ws != act.Workspace && err == nil {
				return registry.Invalid("deck: another workspace has a provider %q; its health and self-check go by name, so pick another", in.Name)
			}
		}
		if err := health.Set(ctx, q, health.Of{}, process.HealthSubject(in.Name), "checking", "the self-check of the new provider is about to run"); err != nil {
			return err
		}
		return q.SetSetting(ctx, db.SetSettingParams{WorkspaceID: act.Workspace, Key: providerPrefix + in.Name, Value: string(raw)})
	})
	if err != nil {
		return nil, err
	}
	s.startCheck(in.Name, true)
	return map[string]any{"name": in.Name}, nil
}

type providerRemoveIn struct {
	Name string `desc:"Provider name"`
}

func (s *Service) providerRemove(ctx context.Context, act registry.Actor, in providerRemoveIn) (any, error) {
	var n int64
	err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		active, err := q.CountActiveOnProvider(ctx, db.CountActiveOnProviderParams{Provider: in.Name, WorkspaceID: act.Workspace})
		if err != nil {
			return err
		}
		if active > 0 {
			return registry.Invalid("deck: %d processes still run on %s; stop them or let them finish first", active, in.Name)
		}
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
		return nil, errors.New("deck: a self-check is already running")
	}
	return map[string]any{"checking": in.Provider}, nil
}

type llmCallsListIn struct {
	Project string `type:"project" opt:"" desc:"Project name; without it an agent of a project gets its own, the owner and the master every project"`
	Process int64  `type:"process" opt:"" desc:"Only calls of this process"`
	Limit   int64  `opt:"" desc:"At most this many"`
}

func (s *Service) llmCallsList(ctx context.Context, act registry.Actor, in llmCallsListIn) (any, error) {
	pid, err := access.Filter(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	in.Limit = registry.Limit(in.Limit, 50, 500)
	rows, err := s.St.Q.ListLLMCalls(ctx, db.ListLLMCallsParams{WorkspaceID: act.Workspace, ProcessID: in.Process, ProjectID: pid, Lim: in.Limit})
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

// Commands are the commands of providers.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Read("providers_list", "Providers",
			"Lists model providers with what they can do and their health; with a command, which fit it and which it runs on.",
			registry.OwnerSide, s.providersList).On("health"),
		registry.Def("provider_set", "Choose provider",
			"Sets the provider an agent command runs on from its next start; empty goes back to the default.",
			registry.OwnerOnly, s.providerSet).Changes("health"),
		registry.Def("provider_add", "Add provider",
			"Adds or changes a provider of this workspace: an ACP agent on an OpenAI-compatible API, such as Ollama, OpenRouter or DeepSeek.",
			registry.OwnerOnly, s.providerAdd).Enum("agent", acp.Names()...).Changes("health"),
		registry.Def("provider_remove", "Remove provider",
			"Removes a provider the owner added; commands set to it must get another.",
			registry.OwnerOnly, s.providerRemove).Changes("health"),
		registry.Def("provider_check", "Check provider",
			"Runs the self-check of a provider's adapter again; its health shows the outcome.",
			registry.OwnerOnly, s.providerCheck),
		registry.Def("llm_calls_list", "Model calls",
			"Lists model calls with their provider, model, tokens and duration, newest first.",
			registry.Everyone, s.llmCallsList),
	}
}
