package providers

import (
	"context"
	"deck2/internal/app/access"
	"errors"

	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

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

func (s *Service) llmCallsList(ctx context.Context, act registry.Actor, in llmCallsListIn) ([]map[string]any, error) {
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
		m := map[string]any{"id": c.ID, "process": c.ProcessID.Int64, "command": c.Command, "gen_ai.provider.name": c.GenAiProviderName,
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
		registry.Def("provider_check", "Check provider",
			"Runs the self-check of a provider's adapter again; its health shows the outcome.",
			registry.OwnerOnly, s.providerCheck),
		registry.Read("models_list", "Models",
			"Lists the connections models are reached through, the models with their kind and price, and which models do which work.",
			registry.OwnerSide, s.modelsList).On("models", "projects", "health"),
		registry.Def("assignment_set", "Assign models",
			"Sets the models that do a kind of work in the workspace, a project or a command: the first, then the fallbacks a new turn or call takes when it is down.",
			registry.OwnerOnly, s.assignmentSet),
		registry.Def("connection_set", "Set connection",
			"Adds or changes a connection models are reached through: its API, address and key, and its daily limit.",
			registry.OwnerOnly, s.connectionSet).Enum("api", "openai", "google", "anthropic", "raw"),
		registry.Def("connection_remove", "Remove connection", "Removes a connection that has no models left.",
			registry.OwnerOnly, s.connectionRemove),
		registry.Def("model_set", "Set model",
			"Adds or changes a model of a connection with its kind and price; what is left out comes from the catalog of the connection.",
			registry.OwnerOnly, s.modelSet).Enum("agent", acp.Names()...),
		registry.Def("model_remove", "Remove model", "Removes a model; assignments that name it skip it.",
			registry.OwnerOnly, s.modelRemove),
		s.modelRequestCommand(), s.modelCallCommand(),
		registry.Read("llm_calls_list", "Model calls",
			"Lists model calls with their provider, model, tokens and duration, newest first.",
			registry.Everyone, s.llmCallsList).On("usage"),
	}
}
