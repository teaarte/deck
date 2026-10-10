// Package health lists the health of what deck runs or reaches in a workspace, worst first.
package health

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// healthRow is the health of something deck runs or reaches: a provider, a command, a tool, a server or a project.
type healthRow struct {
	Subject string `json:"subject"`
	// Project is the project the row is of, none for the machine and the workspace.
	Project string `json:"project,omitempty"`
	Status  string `json:"status"`
	Error   string `json:"error"`
	Version string `json:"version"`
	// Verdict of a failed self-check: model when the model does not manage it, adapter when it broke.
	Verdict   string  `json:"verdict,omitempty"`
	Speed     float64 `json:"speed,omitempty"`
	UpdatedAt string  `json:"updated_at"`
	// SelfCheck marks a provider deck can check again.
	SelfCheck bool `json:"self_check"`
	// Seen marks a failing check the owner read since it last changed.
	Seen bool `json:"seen"`
}

// healthList shows what the machine runs, the providers, commands, tools and MCP servers of the actor's
// workspace, and a project whose manifest does not load; worst first.
type healthListIn struct {
	Project string `type:"project" opt:"" desc:"Only the commands, tools and servers of this project"`
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
	out := make([]healthRow, 0, len(hs)+len(ps))
	shown := func(project string) bool {
		return (in.Project == "" || project == in.Project) && (!in.Machine || project == "")
	}
	for _, p := range ps {
		if !shown(p.Name) {
			continue
		}
		m, err := docs.Manifest(ctx, s.St.Q, p)
		if err != nil {
			out = append(out, healthRow{Subject: "project:" + p.Name, Project: p.Name, Status: health.Error, Error: "manifest: " + err.Error()})
			continue
		}
		for name, text := range s.Unassigned(ctx, p.WorkspaceID, p.ID, m) {
			row := healthRow{Subject: "command:" + name, Project: p.Name, Status: health.Warning, Error: text}
			if c := m.Command(name); c != nil && c.Executor.Type == "agent" && p.Status == store.ProjectEnabled {
				row.Status, row.Seen = health.Error, s.read(ctx, act.Workspace, row)
			}
			out = append(out, row)
		}
		if p.Status != store.ProjectEnabled {
			continue
		}
		for _, name := range m.Keys() {
			if s.Secret(p.WorkspaceID, name) {
				continue
			}
			row := healthRow{Subject: "secret:" + name, Project: p.Name, Status: health.Error,
				Error: "not set, so the tools that send it fail; set it in Settings → Connections → Other secrets"}
			row.Seen = s.read(ctx, act.Workspace, row)
			out = append(out, row)
		}
	}
	for _, hr := range hs {
		h := hr.Health
		if strings.HasPrefix(h.Subject, "provider:") && !mine[h.Subject] || !shown(hr.Project) {
			continue
		}
		out = append(out, healthRow{Subject: h.Subject, Project: hr.Project, Status: h.Status, Error: h.Error, Version: h.Version,
			Verdict: h.Verdict, Speed: h.Speed, UpdatedAt: h.UpdatedAt, SelfCheck: checked[h.Subject], Seen: h.SeenAt.Valid && h.SeenAt.String >= h.UpdatedAt})
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

// Failing are the failing checks of a workspace the owner has not read, by project, the machine's under "".
func (s *Service) Failing(ctx context.Context, act registry.Actor) (map[string]int64, error) {
	rows, err := s.list(ctx, act, healthListIn{})
	if err != nil {
		return nil, err
	}
	out := map[string]int64{}
	for _, r := range rows {
		if r.Status == health.Error && !r.Seen {
			out[r.Project]++
		}
	}
	return out, nil
}

// Service lists health rows.
type Service struct {
	St *store.Store
	// Providers are the providers of a workspace.
	Providers func(ctx context.Context, workspace int64) ([]provider.Provider, error)
	// Secret reports whether a secret of a workspace is set.
	Secret func(workspace int64, name string) bool
	// Unassigned are the commands of a manifest no model takes, with what the owner does.
	Unassigned func(ctx context.Context, workspace, project int64, m *manifest.Manifest) map[string]string
	Bus        *events.Bus
}

// Commands are the commands of health.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Read("health_list", "Health",
			"Lists the health of providers and other dependencies with the last error, worst first.",
			registry.OwnerSide, s.list).On("health", "secrets", "projects", "models", "assignments"),
		registry.Def("health_seen", "Mark a failing check read",
			"Marks a failing check read: it stops counting among the failing until its status or error changes.",
			registry.OwnerOnly, s.seen),
	}
}

type seenIn struct {
	Subject string `desc:"Subject of a failing health row, such as tool:web.fetch"`
}

// seen marks a failing check read: a stored row by its time, one deck works out on each read, such as a secret not
// set, by a mark of its text until the secret is set.
func (s *Service) seen(ctx context.Context, act registry.Actor, in seenIn) (any, error) {
	n, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return q.SeeHealth(ctx, db.SeeHealthParams{Subject: in.Subject, WorkspaceID: store.ID(act.Workspace)})
	})
	if err != nil {
		return nil, err
	}
	if n > 0 {
		return map[string]any{"ok": true}, nil
	}
	rows, err := s.list(ctx, act, healthListIn{})
	if err != nil {
		return nil, err
	}
	i := slices.IndexFunc(rows, func(r healthRow) bool {
		return r.Subject == in.Subject && r.Status == health.Error && r.UpdatedAt == ""
	})
	if i < 0 {
		return nil, registry.NotFound("deck: no failing check %s", in.Subject)
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.SetMark(ctx, db.SetMarkParams{WorkspaceID: act.Workspace, Name: seenMark + in.Subject, Value: rows[i].Error})
	})
	if err != nil {
		return nil, err
	}
	s.Bus.Emit(events.Event{Workspace: act.Workspace, Topic: "health"})
	return map[string]any{"ok": true}, nil
}

// SecretSet forgets that the owner read that a secret is not set: once it has a value, its going again is news.
func (s *Service) SecretSet(workspace int64, name string) {
	err := s.St.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		return q.DeleteMark(ctx, db.DeleteMarkParams{WorkspaceID: workspace, Name: seenMark + "secret:" + name})
	})
	if err != nil {
		slog.Error("health: forget a read secret", "secret", name, "err", err)
	}
}

// seenMark starts the name of the mark of a worked-out check the owner read; its value is the text read.
const seenMark = "health-seen:"

// read reports whether the owner read a worked-out check as it is now.
func (s *Service) read(ctx context.Context, workspace int64, row healthRow) bool {
	v, err := s.St.Q.GetMark(ctx, db.GetMarkParams{WorkspaceID: workspace, Name: seenMark + row.Subject})
	return err == nil && v == row.Error
}
