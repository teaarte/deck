package app

import (
	"context"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
)

// healthRow is the health of something deck runs or reaches: a provider, a command, a tool, a server or a project.
type healthRow struct {
	Subject string `json:"subject"`
	Status  string `json:"status"`
	Error   string `json:"error"`
	Version string `json:"version"`
	// Verdict of a failed self-check: model when the model does not manage it, adapter when it broke.
	Verdict   string  `json:"verdict,omitempty"`
	Speed     float64 `json:"speed,omitempty"`
	UpdatedAt string  `json:"updated_at"`
	// SelfCheck marks a provider deck can check again.
	SelfCheck bool `json:"self_check"`
}

// healthList shows what the machine runs, the providers, commands, tools and MCP servers of the actor's
// workspace, and per project the worst of those with its error, or a manifest that does not load; worst first.
type healthListIn struct {
	Project string `type:"project" opt:"" desc:"Only the commands, tools and servers of this project, and the project"`
	Machine bool   `opt:"" desc:"Only what the machine runs: providers, the disk, the sandbox, the tailnet"`
}

func (a *App) healthList(ctx context.Context, act registry.Actor, in healthListIn) ([]healthRow, error) {
	hs, err := a.Store.Q.ListHealth(ctx, store.ID(act.Workspace))
	if err != nil {
		return nil, err
	}
	ps, err := a.Store.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	pvs, err := a.Providers.All(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	mine, checked := map[string]bool{}, map[string]bool{}
	for _, pv := range pvs {
		_, checked[process.HealthSubject(pv.Name())] = pv.(provider.SelfChecker)
		mine[process.HealthSubject(pv.Name())] = true
	}
	worst := map[string]*healthRow{}
	for _, p := range ps {
		worst[p.Name] = &healthRow{Subject: "project:" + p.Name, Status: "ok"}
		if _, err := docs.Manifest(ctx, a.Store.Q, p); err != nil {
			worst[p.Name].Status, worst[p.Name].Error = "error", "manifest: "+err.Error()
		}
	}
	rank := map[string]int{"ok": 0, "checking": 1, "paused": 1, "needs_login": 1, "warning": 1, "error": 2}
	out := make([]healthRow, 0, len(hs)+len(ps))
	shown := func(project string) bool {
		return (in.Project == "" || project == in.Project) && (!in.Machine || project == "")
	}
	for _, hr := range hs {
		h := hr.Health
		if strings.HasPrefix(h.Subject, "provider:") && !mine[h.Subject] || !shown(hr.Project) {
			if w := worst[hr.Project]; h.ProjectID.Valid && w != nil && rank[h.Status] > rank[w.Status] {
				w.Status, w.Error, w.UpdatedAt = h.Status, h.Subject+": "+h.Error, h.UpdatedAt
			}
			continue
		}
		out = append(out, healthRow{Subject: h.Subject, Status: h.Status, Error: h.Error, Version: h.Version, Verdict: h.Verdict,
			Speed: h.Speed, UpdatedAt: h.UpdatedAt, SelfCheck: checked[h.Subject]})
		if w := worst[hr.Project]; h.ProjectID.Valid && w != nil && rank[h.Status] > rank[w.Status] {
			w.Status, w.Error, w.UpdatedAt = h.Status, h.Subject+": "+h.Error, h.UpdatedAt
		}
	}
	for _, p := range ps {
		if shown(p.Name) && !in.Machine {
			out = append(out, *worst[p.Name])
		}
	}
	order := map[string]int{"error": 0, "needs_login": 1, "paused": 1, "warning": 1, "checking": 2, "ok": 3}
	slices.SortStableFunc(out, func(x, y healthRow) int { return order[x.Status] - order[y.Status] })
	return out, nil
}

func (a *App) healthCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("health_list", "Health",
			"Lists the health of providers and other dependencies with the last error, worst first.",
			registry.OwnerSide, a.healthList).On("health"),
	}
}
