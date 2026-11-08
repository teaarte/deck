package providers

import (
	"context"
	"slices"
	"strings"
	"time"

	"deck2/internal/models"
	"deck2/internal/provider"
	"deck2/internal/provider/remote"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

// NodeNamed is the node of a workspace by its name.
func (s *Service) NodeNamed(ctx context.Context, workspace int64, name string) (int64, error) {
	nodes, err := s.St.Q.ListNodes(ctx)
	if err != nil {
		return 0, err
	}
	var found []int64
	for _, d := range nodes {
		if d.WorkspaceID == workspace && !d.RevokedAt.Valid && d.Name == name {
			found = append(found, d.ID)
		}
	}
	switch len(found) {
	case 0:
		return 0, registry.NotFound("no node %q among the devices; pair one with deck pair --node", name)
	case 1:
		return found[0], nil
	}
	return 0, registry.Invalid("%d nodes are named %q; rename one in Devices", len(found), name)
}

// onNode is the agent base of a workspace run on its node: claude-code@Mac, a model of a CLI or of an ACP agent @Mac.
func (s *Service) onNode(ctx context.Context, workspace int64, base, name string) (provider.Provider, error) {
	if strings.Contains(base, "@") || s.Nodes == nil {
		return nil, registry.NotFound("no provider %q", base+"@"+name)
	}
	device, err := s.NodeNamed(ctx, workspace, name)
	if err != nil {
		return nil, err
	}
	pv, err := s.Provider(ctx, workspace, base)
	if err != nil {
		return nil, err
	}
	agent := s.agentOf(ctx, workspace, base)
	if agent == "" {
		return nil, registry.NotFound("no provider %q: %s runs in no agent", base+"@"+name, base)
	}
	// What a node runs is known while it is connected; away, its turns wait for it.
	if st := s.Nodes.State(device); st.Online && !slices.Contains(st.Agents, agent) {
		return nil, registry.Invalid("node %s does not run the agent %s: on its machine allow it with deck -join %s --install --allow %s",
			name, agent, st.Address, agent)
	}
	cfg := remote.Config{Spec: remote.Spec{Agent: base, Name: base}, Base: pv, Machine: s.machine(device, name)}
	if row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: base}); err == nil {
		m, c := row.Model, row.Connection
		cfg.Spec.Agent, cfg.Spec.Model = m.Agent, m.Model
		if c.Api == models.APICLI {
			if cfg.Spec.Effort, err = cliEffort(m.Options); err != nil {
				return nil, err
			}
		} else {
			cfg.Spec.ACP, cfg.Spec.NoCloud, cfg.Spec.CallLimit = true, c.Local != 0, s.ACPCallLimit
			cfg.Spec.CheckCeiling = time.Duration(m.CheckMinutes) * time.Minute
			cfg.Model = s.TurnModel(workspace, m, c)
		}
	}
	return remote.New(cfg), nil
}

// machine is the node device named name, as its turns and scripts reach it.
func (s *Service) machine(device int64, name string) remote.Machine {
	return remote.Machine{Node: name, Device: device, Hub: s.Nodes, Home: s.Home, Wait: s.NodeWait, Low: s.Low, Denied: s.NodeDenied}
}

// Scripts runs scripts on the node device named name.
func (s *Service) Scripts(device int64, name string) *remote.Scripts {
	return &remote.Scripts{Machine: s.machine(device, name)}
}

// agentOf is the agent a provider of a workspace runs in: a built-in CLI by its name, a model by its agent.
func (s *Service) agentOf(ctx context.Context, workspace int64, name string) string {
	if s.builtinNamed(name) != nil {
		return name
	}
	if row, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name}); err == nil {
		return row.Model.Agent
	}
	return ""
}

// onNodes are the providers of a workspace on its nodes that are connected: each of base whose agent the node runs.
func (s *Service) onNodes(ctx context.Context, workspace int64, base []provider.Provider) []provider.Provider {
	nodes, err := s.St.Q.ListNodes(ctx)
	if err != nil || s.Nodes == nil {
		return nil
	}
	var out []provider.Provider
	for _, d := range nodes {
		st := s.Nodes.State(d.ID)
		if d.WorkspaceID != workspace || d.RevokedAt.Valid || !st.Online || strings.Contains(d.Name, "@") {
			continue
		}
		for _, pv := range base {
			if !slices.Contains(st.Agents, s.agentOf(ctx, workspace, pv.Name())) {
				continue
			}
			if on, err := s.onNode(ctx, workspace, pv.Name(), d.Name); err == nil {
				out = append(out, on)
			}
		}
	}
	return out
}

// away says a provider runs on a node that is not connected now.
func (s *Service) away(ctx context.Context, workspace int64, name string) bool {
	_, node, ok := provider.AtNode(name)
	if !ok || s.Nodes == nil {
		return false
	}
	device, err := s.NodeNamed(ctx, workspace, node)
	return err != nil || !s.Nodes.State(device).Online
}

// awayAnywhere says a provider runs on a node not connected now, in whichever workspace has it.
func (s *Service) awayAnywhere(ctx context.Context, name string) bool {
	if _, _, ok := provider.AtNode(name); !ok {
		return false
	}
	spaces, _ := s.St.Q.ListWorkspaces(ctx)
	for _, ws := range spaces {
		if _, err := s.Provider(ctx, ws, name); err == nil {
			return s.away(ctx, ws, name)
		}
	}
	return true
}
