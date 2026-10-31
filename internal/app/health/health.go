// Package health lists the health of what deck runs or reaches in a workspace, worst first.
package health

import (
	"context"
	"slices"
	"strings"
	"sync"

	"deck2/internal/app/access"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/words"
)

// healthRow is the health of something deck runs or reaches: a provider, a command, a tool, a server or a project.
type healthRow struct {
	Subject string `json:"subject"`
	// Project is the project the row is of, none for the machine and the workspace.
	Project string `json:"project,omitempty"`
	Status  string `json:"status" enum:"ok|warning|error|checking|paused|needs_login|missing"`
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
	// Kind is what the row is of, the start of its subject: provider, model, command, trigger, secret, ….
	Kind string `json:"kind" enum:"provider|agent|model|command|trigger|tool|mcp|secret|screen|project|retention|site|scripts|sandbox|tailscale|snapshot|disk|outbox|push|search|master|browser|update"`
}

// shownRow is a row of health with the work it stands for: the model of the command of a row of a command or of its
// schedule, with Trigger its cron trigger; Places the work of the projects the model of a row does.
type shownRow struct {
	healthRow
	Command string `json:"command,omitempty"`
	provider.Work
	Trigger int64    `json:"trigger,omitempty"`
	Places  []string `json:"places,omitempty"`
}

// healthList shows what the machine runs, the providers, commands, tools and MCP servers of the actor's
// workspace, and a project whose manifest does not load; worst first.
type healthListIn struct {
	Project string `type:"project" opt:"" desc:"Only the commands, tools and servers of this project"`
	Machine bool   `opt:"" desc:"Only what the machine runs: providers, the disk, the sandbox, the runtime of scripts, the tailnet"`
	Lang    string `opt:"" desc:"Language of the prices of models and of the sentences of deck, such as ru; English without one"`
}

// shown is the health of the workspace with the model of each command and schedule and the work of each model.
func (s *Service) shown(ctx context.Context, act registry.Actor, in healthListIn) ([]shownRow, error) {
	rows, err := s.list(ctx, act, in)
	if err != nil {
		return nil, err
	}
	ps, err := s.St.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	ids := map[string]int64{}
	for _, p := range ps {
		ids[p.Name] = p.ID
	}
	ts, err := s.St.Q.ListWorkspaceTriggers(ctx, db.ListWorkspaceTriggersParams{WorkspaceID: act.Workspace})
	if err != nil {
		return nil, err
	}
	works, places := s.Works(ctx, act.Workspace, in.Lang), s.Places(ctx, act.Workspace)
	ws := words.In(in.Lang)
	out := make([]shownRow, 0, len(rows))
	for _, r := range rows {
		r.Error = manifest.TrText(ws, r.Error)
		v := shownRow{healthRow: r}
		name := strings.TrimPrefix(r.Subject, r.Kind+":")
		switch r.Kind {
		case "command", "trigger":
			if id, ok := ids[r.Project]; ok {
				v.Command = strings.TrimPrefix(name, r.Project+".")
				v.Work = works(id, v.Command)
				if i := slices.IndexFunc(ts, func(t db.Trigger) bool { return t.ProjectID == id && t.Command == v.Command && t.Kind == "cron" }); i >= 0 {
					v.Trigger = ts[i].ID
				}
			}
		case "provider", "model":
			v.Places = places[name]
		}
		out = append(out, v)
	}
	return out, nil
}

func (s *Service) list(ctx context.Context, act registry.Actor, in healthListIn) ([]healthRow, error) {
	hs, err := s.St.Q.ListHealth(ctx, store.ID(act.Workspace))
	if err != nil {
		return nil, err
	}
	pvs, err := s.Providers(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	mine, checked := map[string]bool{}, map[string]bool{}
	for _, pv := range pvs {
		_, checked[health.Provider(pv.Name())] = pv.(provider.SelfChecker)
		mine[health.Provider(pv.Name())] = true
	}
	models, err := s.St.Q.ListModels(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	for _, m := range models {
		mine[health.Provider(m.Model.Name)] = true
	}
	out := make([]healthRow, 0, len(hs))
	shown := func(project string) bool {
		return (in.Project == "" || project == in.Project) && (!in.Machine || project == "")
	}
	for _, hr := range hs {
		h := hr.Health
		if strings.HasPrefix(h.Subject, "provider:") && !mine[h.Subject] || !shown(hr.Project) {
			continue
		}
		out = append(out, healthRow{Subject: h.Subject, Project: hr.Project, Status: h.Status, Error: h.Error, Version: h.Version,
			Verdict: h.Verdict, Speed: h.Speed, UpdatedAt: h.UpdatedAt, SelfCheck: checked[h.Subject], Seen: h.SeenAt.Valid && h.SeenAt.String >= h.UpdatedAt})
	}
	for i := range out {
		out[i].Kind, _, _ = strings.Cut(out[i].Subject, ":")
		if !strings.Contains(out[i].Subject, ":") {
			out[i].Kind = out[i].Subject
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
	// NoSearch is what the owner does while no search is assigned, or "".
	NoSearch func(ctx context.Context, workspace int64) string
	// Missing are the names assignments hold that are no model of deck, with the places that name them.
	Missing func(ctx context.Context, workspace int64) map[string][]string
	// Works looks up the model that does the work of a command; Places the work each model does.
	Works  func(ctx context.Context, workspace int64, lang string) func(projectID int64, command string) provider.Work
	Places func(ctx context.Context, workspace int64) map[string][]string
	// Go runs work beside the calls unless deck stops.
	Go func(func(context.Context)) bool

	// deriving keeps one Derive at a time.
	deriving sync.Mutex
}

// Commands are the commands of health.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Read("health_list", "Health",
			"Lists the health of providers and other dependencies with the last error, worst first.",
			registry.OwnerSide, s.shown).On("health", "secrets", "projects", "models", "assignments", "triggers", "documents"),
		registry.Def("health_mark_seen", "Mark a failing check read",
			"Marks a failing check read: it stops counting among the failing until its status or error changes.",
			registry.OwnerOnly, s.seen),
	}
}

type seenIn struct {
	Subject string `desc:"Subject of a failing health row, such as tool:web.fetch"`
	Project string `type:"project" opt:"" desc:"Project of the row; none for the machine and the workspace"`
}

// seen marks a failing check read until its status or error changes.
func (s *Service) seen(ctx context.Context, act registry.Actor, in seenIn) (any, error) {
	var project int64
	if in.Project != "" {
		p, err := access.ByName(ctx, s.St.Q, act, in.Project)
		if err != nil {
			return nil, err
		}
		project = p.ID
	}
	n, err := store.TxGet(s.St, ctx, func(ctx context.Context, q *db.Queries) (int64, error) {
		return q.SeeHealth(ctx, db.SeeHealthParams{Subject: in.Subject, WorkspaceID: store.ID(act.Workspace), ProjectID: store.ID(project)})
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.NotFound("deck: no failing check %s", in.Subject)
	}
	return map[string]any{"ok": true}, nil
}
