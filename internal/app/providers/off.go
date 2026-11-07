package providers

import (
	"context"

	"deck2/internal/health"
	"deck2/internal/models"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// offKey is the setting of a workspace that turns a built-in provider off.
func offKey(name string) string { return "off:" + name }

// base is the built-in provider a provider runs on: itself, or the CLI a model of deck's settings runs in; nil for
// any other.
func (s *Service) base(ctx context.Context, workspace int64, name string) provider.Provider {
	if pv := s.builtinNamed(name); pv != nil {
		return pv
	}
	row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name})
	if err != nil || row.Connection.Api != models.APICLI {
		return nil
	}
	return s.builtinNamed(row.Model.Agent)
}

// Off is why a provider of a workspace takes no work: health.Missing or health.Off; empty when it is on.
func (s *Service) Off(ctx context.Context, workspace int64, name string) string {
	if agent := s.acpAgent(ctx, workspace, name); agent != "" && !s.Installed(agent) {
		return health.Missing
	}
	pv := s.base(ctx, workspace, name)
	if pv == nil {
		return ""
	}
	if i, ok := pv.(provider.Installed); ok && !i.Installed() {
		return health.Missing
	}
	if v, err := s.St.Q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: workspace, Key: offKey(pv.Name())}); err == nil && v == "1" {
		return health.Off
	}
	return ""
}

// acpAgent is the ACP agent a model of deck's settings runs in, such as goose; empty for any other provider.
func (s *Service) acpAgent(ctx context.Context, workspace int64, name string) string {
	row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name})
	if err != nil || row.Connection.Api == models.APICLI {
		return ""
	}
	return row.Model.Agent
}

// resting says a built-in provider is checked by no workspace: its program is missing, or every workspace turned it
// off.
func (s *Service) resting(ctx context.Context, pv provider.Provider) bool {
	if i, ok := pv.(provider.Installed); ok && !i.Installed() {
		return true
	}
	spaces, _ := s.St.Q.ListWorkspaces(ctx)
	for _, ws := range spaces {
		if s.Off(ctx, ws, pv.Name()) == "" {
			return false
		}
	}
	return len(spaces) > 0
}

type providerSwitchIn struct {
	Provider string `desc:"A built-in provider: claude-code or codex"`
	Off      bool   `desc:"True turns it off, false on again"`
}

// providerSwitch turns a built-in provider off or on for the workspace: off, it is not checked and takes no work, and
// the models that run in it are not offered.
func (s *Service) providerSwitch(ctx context.Context, act registry.Actor, in providerSwitchIn) (any, error) {
	pv := s.builtinNamed(in.Provider)
	if _, ok := pv.(provider.SelfChecker); !ok {
		return nil, registry.Invalid("%q is no built-in provider; a model of an agent is removed in Settings → Models", in.Provider)
	}
	value := ""
	if in.Off {
		value = "1"
	}
	if err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.SetSetting(ctx, db.SetSettingParams{WorkspaceID: act.Workspace, Key: offKey(in.Provider), Value: value})
	}); err != nil {
		return nil, err
	}
	if in.Off && s.resting(ctx, pv) {
		if err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			return health.Clear(ctx, q, health.Of{}, health.Provider(in.Provider))
		}); err != nil {
			return nil, err
		}
	}
	if !in.Off {
		s.startCheck(in.Provider, true)
	}
	return map[string]any{"provider": in.Provider, "off": in.Off}, nil
}

func (s *Service) offCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("provider_switch", "Turn provider off",
			"Turns a built-in provider (claude-code, codex) off or on again: off, deck does not check it, gives it no work and offers none of its models; a provider whose program is not installed is off by itself.",
			registry.OwnerOnly, s.providerSwitch),
	}
}
