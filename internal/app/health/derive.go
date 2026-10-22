package health

import (
	"context"
	"log/slog"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/health"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// derivedFrom are the topics of what the derived rows of health are worked out of.
var derivedFrom = []string{"projects", "documents", "assignments", "models", "secrets"}

// Watch derives the rows of health again whenever what they are worked out of changes.
func (s *Service) Watch(bus *events.Bus) {
	bus.Watch(func(e events.Event) {
		if slices.Contains(derivedFrom, e.Topic) {
			s.Derive(context.Background(), e.Workspace)
		}
	})
}

// derived is a row deck works out of what a workspace holds.
type derived struct {
	of           health.Of
	status, text string
}

// Derive writes the rows deck works out of what a workspace holds, every workspace for zero: a manifest that does not
// read, a command no model takes, a secret a project needs that is not set and no search; a row whose cause went goes.
func (s *Service) Derive(ctx context.Context, workspace int64) {
	s.deriving.Lock()
	defer s.deriving.Unlock()
	spaces := []int64{workspace}
	if workspace == 0 {
		var err error
		if spaces, err = s.St.Q.ListWorkspaces(ctx); err != nil {
			slog.Error("derive health", "err", err)
			return
		}
	}
	for _, ws := range spaces {
		if err := s.derive(ctx, ws); err != nil {
			slog.Error("derive health", "workspace", ws, "err", err)
		}
	}
}

func (s *Service) derive(ctx context.Context, ws int64) error {
	ps, err := s.St.Q.ListWorkspaceProjects(ctx, ws)
	if err != nil {
		return err
	}
	want := map[string]derived{}
	if text := s.NoSearch(ctx, ws); text != "" {
		want[health.Search] = derived{health.Of{Workspace: ws}, health.Warning, text}
	}
	// commands are the commands of each project whose rows are derived: those of agents and models; unread are the
	// projects whose manifest does not read, whose commands deck does not know.
	commands, unread := map[string]bool{}, map[string]bool{}
	for _, p := range ps {
		m, err := docs.Manifest(ctx, s.St.Q, p)
		if err != nil {
			want[health.Manifest(p.Name)] = derived{health.Project(p), health.Error, "manifest: " + err.Error()}
			unread[p.Name] = true
			continue
		}
		for _, c := range m.Commands {
			commands[health.Command(p.Name, c.Name)] = c.Executor.Type != "script"
		}
		for name, text := range s.Unassigned(ctx, ws, p.ID, m) {
			status := health.Warning
			if c := m.Command(name); c != nil && c.Executor.Type == "agent" && p.Status == store.ProjectEnabled {
				status = health.Error
			}
			want[health.Command(p.Name, name)] = derived{health.Project(p), status, text}
		}
		if p.Status != store.ProjectEnabled {
			continue
		}
		for _, name := range m.Keys() {
			if !s.Secret(ws, name) {
				want[health.Secret(p.Name, name)] = derived{health.Project(p), health.Error,
					"not set, so the tools that send it fail; set it in Settings → Advanced → Secrets"}
			}
		}
	}
	rows, err := s.St.Q.ListHealth(ctx, store.ID(ws))
	if err != nil {
		return err
	}
	return s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		for subject, d := range want {
			if _, err := health.Set(ctx, q, d.of, subject, d.status, d.text); err != nil {
				return err
			}
		}
		for _, r := range rows {
			h := r.Health
			if _, ok := want[h.Subject]; ok || h.WorkspaceID.Int64 != ws || !ownDerived(h, commands, unread[r.Project]) {
				continue
			}
			if err := health.Clear(ctx, q, health.Of{Workspace: ws}, h.Subject); err != nil {
				return err
			}
		}
		return nil
	})
}

// ownDerived reports whether Derive writes a row: of a manifest, a secret, search, or of a command of an agent or a
// model or of none in a manifest that reads, as the rows of script commands are of their programs.
func ownDerived(h db.Health, commands map[string]bool, unread bool) bool {
	kind, _, _ := strings.Cut(h.Subject, ":")
	switch kind {
	case "project", "secret", health.Search:
		return true
	case "command":
		derived, ok := commands[h.Subject]
		return derived || !ok && !unread
	}
	return false
}
