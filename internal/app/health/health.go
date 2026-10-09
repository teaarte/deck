// Package health lists the health of what deck runs or reaches in a workspace, worst first.
package health

import (
	"context"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/health"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
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
	// Seen marks a failing check the owner read since it last changed; a project is seen when all its are.
	Seen bool `json:"seen"`
}

// healthList shows what the machine runs, the providers, commands, tools and MCP servers of the actor's
// workspace, and per project the worst of those with its error, or a manifest that does not load; worst first.
type healthListIn struct {
	Project string `type:"project" opt:"" desc:"Only the commands, tools and servers of this project, and the project"`
	Machine bool   `opt:"" desc:"Only what the machine runs: providers, the disk, the sandbox, the tailnet"`
}

func (s *Service) list(ctx context.Context, act registry.Actor, in healthListIn) ([]healthRow, error) {
	hs, err := s.St.Q.ListHealth(ctx, store.ID(act.Workspace))
	if err != nil {
		return nil, err
	}
	ps, err := s.St.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	pvs, err := s.Providers(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	mine, checked := map[string]bool{}, map[string]bool{}
	for _, pv := range pvs {
		_, checked[process.HealthSubject(pv.Name())] = pv.(provider.SelfChecker)
		mine[process.HealthSubject(pv.Name())] = true
	}
	models, err := s.St.Q.ListModels(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	for _, m := range models {
		mine[process.HealthSubject(m.Model.Name)] = true
	}
	worst := map[string]*healthRow{}
	for _, p := range ps {
		worst[p.Name] = &healthRow{Subject: "project:" + p.Name, Status: health.OK}
		if _, err := docs.Manifest(ctx, s.St.Q, p); err != nil {
			worst[p.Name].Status, worst[p.Name].Error = health.Error, "manifest: "+err.Error()
		}
	}
	out := make([]healthRow, 0, len(hs)+len(ps))
	shown := func(project string) bool {
		return (in.Project == "" || project == in.Project) && (!in.Machine || project == "")
	}
	unseen := map[string]bool{}
	for _, hr := range hs {
		h := hr.Health
		seen := h.SeenAt.Valid && h.SeenAt.String >= h.UpdatedAt
		if h.Status == health.Error && !seen && h.ProjectID.Valid {
			unseen[hr.Project] = true
		}
		if strings.HasPrefix(h.Subject, "provider:") && !mine[h.Subject] || !shown(hr.Project) {
			if w := worst[hr.Project]; h.ProjectID.Valid && w != nil && health.Worse(h.Status, w.Status) {
				w.Status, w.Error, w.UpdatedAt = h.Status, h.Subject+": "+h.Error, h.UpdatedAt
			}
			continue
		}
		out = append(out, healthRow{Subject: h.Subject, Status: h.Status, Error: h.Error, Version: h.Version, Verdict: h.Verdict,
			Speed: h.Speed, UpdatedAt: h.UpdatedAt, SelfCheck: checked[h.Subject], Seen: seen})
		if w := worst[hr.Project]; h.ProjectID.Valid && w != nil && health.Worse(h.Status, w.Status) {
			w.Status, w.Error, w.UpdatedAt = h.Status, h.Subject+": "+h.Error, h.UpdatedAt
		}
	}
	for _, p := range ps {
		if shown(p.Name) && !in.Machine {
			w := *worst[p.Name]
			w.Seen = w.Status == health.Error && !unseen[p.Name] && !strings.HasPrefix(w.Error, "manifest: ")
			out = append(out, w)
		}
	}
	slices.SortStableFunc(out, func(x, y healthRow) int {
		switch {
		case health.Worse(x.Status, y.Status):
			return -1
		case health.Worse(y.Status, x.Status):
			return 1
		}
		return 0
	})
	return out, nil
}

// Service lists health rows.
type Service struct {
	St *store.Store
	// Providers are the providers of a workspace.
	Providers func(ctx context.Context, workspace int64) ([]provider.Provider, error)
}

// Commands are the commands of health.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Read("health_list", "Health",
			"Lists the health of providers and other dependencies with the last error, worst first.",
			registry.OwnerSide, s.list).On("health"),
		registry.Def("health_seen", "Mark a failing check read",
			"Marks a failing check read: it stops counting among the failing until its status or error changes.",
			registry.OwnerOnly, s.seen),
	}
}

type seenIn struct {
	Subject string `desc:"Subject of a failing health row, such as tool:web.fetch"`
}

// seen marks a failing check read.
func (s *Service) seen(ctx context.Context, act registry.Actor, in seenIn) (any, error) {
	n, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return q.SeeHealth(ctx, db.SeeHealthParams{Subject: in.Subject, WorkspaceID: store.ID(act.Workspace)})
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("deck: no failing check %s", in.Subject)
	}
	return map[string]any{"ok": true}, nil
}
