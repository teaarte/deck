package providers

import (
	"context"
	"errors"

	"deck2/internal/provider"
	"deck2/internal/provider/acp"
	"deck2/internal/registry"
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
		if s.Ctx.Err() != nil {
			return nil, errors.New("deck: deck is stopping")
		}
		return nil, errors.New("deck: a self-check is already running")
	}
	return map[string]any{"checking": in.Provider}, nil
}

// Commands are the commands of providers.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Def("provider_check", "Check provider",
			"Runs the self-check of a provider's adapter again; its health shows the outcome.",
			registry.OwnerOnly, s.providerCheck),
		registry.Read("models_list", "Models",
			"Lists the connections models are reached through, the models with their kind and price, and which models do which work.",
			registry.OwnerSide, s.modelsList).On("models", "assignments", "projects", "health"),
		registry.Def("assignment_set", "Assign models",
			"Sets the models that do a kind of work in the workspace, a project or a command: the first, then the fallbacks a new turn or call takes when it is down.",
			registry.OwnerOnly, s.assignmentSet),
		registry.Def("connection_set", "Set connection",
			"Adds or changes a connection models are reached through: its API, address and key.",
			registry.OwnerOnly, s.connectionSet),
		registry.Def("connection_remove", "Remove connection", "Removes a connection that has no models left.",
			registry.OwnerOnly, s.connectionRemove),
		registry.Def("model_set", "Set model",
			"Adds or changes a model of a connection with its kind and price; what is left out comes from the catalog of the connection. A module or the master proposes a model it needs this way, and the owner adds it with one button.",
			registry.Everyone, s.modelSet).Enum("agent", acp.Names()...).As(registry.External),
		registry.Def("model_remove", "Remove model", "Removes a model; assignments that name it skip it.",
			registry.OwnerOnly, s.modelRemove),
		s.modelRequestCommand(), s.modelCallCommand(),
	}
}
