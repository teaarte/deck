package health

import (
	"context"
	"fmt"
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

// Follow derives the rows of health of the projects whose rows are worked out of what changed, beside the commits.
func (s *Service) Follow(bus *events.Bus) {
	bus.Follow(s.Go, derivedFrom, s.Derive)
}

// derived is a row deck works out of what a workspace holds.
type derived struct {
	of           health.Of
	status, text string
}

// Derive writes the rows deck works out of what a workspace holds, every workspace for zero and of the projects named,
// every one for nil: a manifest that does not read, a command no model takes, an unset secret, no search.
func (s *Service) Derive(ctx context.Context, workspace int64, projects []string) {
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
		if err := s.derive(ctx, ws, projects); err != nil && ctx.Err() == nil {
			slog.Error("derive health", "workspace", ws, "err", err)
		}
	}
}

func (s *Service) derive(ctx context.Context, ws int64, projects []string) error {
	ps, err := s.St.Q.ListWorkspaceProjects(ctx, ws)
	if err != nil {
		return err
	}
	if projects != nil {
		ps = slices.DeleteFunc(ps, func(p db.Project) bool { return !slices.Contains(projects, p.Name) })
	}
	ids := map[int64]bool{}
	for _, p := range ps {
		ids[p.ID] = true
	}
	want := map[string]derived{}
	if text := s.NoSearch(ctx, ws); text != "" {
		want[health.Search] = derived{health.Of{Workspace: ws}, health.Warning, text}
	}
	missing := s.Missing(ctx, ws)
	for name, places := range missing {
		want[health.Model(name)] = derived{health.Of{Workspace: ws}, health.Warning,
			fmt.Sprintf("deck has no such model, and the assignments of %s name it; pick another in Settings → Models", strings.Join(places, ", "))}
	}
	for _, p := range ps {
		m, err := docs.Manifest(ctx, s.St.Q, p)
		if err != nil {
			want[health.Manifest(p.Name)] = derived{health.Project(p), health.Error, "manifest: " + err.Error()}
			continue
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
		// The commands of scripts are read again here, as the rows of those are of their programs and a save may have
		// made one a script since the reads above.
		scripts := map[string]bool{}
		for _, p := range ps {
			if m, err := docs.Manifest(ctx, q, p); err == nil {
				for _, c := range m.Commands {
					scripts[health.Command(p.Name, c.Name)] = c.Executor.Type == "script"
				}
			}
		}
		for subject, d := range want {
			if scripts[subject] {
				continue
			}
			if name, ok := strings.CutPrefix(subject, "model:"); ok {
				if _, err := q.GetModel(ctx, db.GetModelParams{WorkspaceID: ws, Name: name}); err == nil {
					continue
				}
			}
			if _, err := health.Set(ctx, q, d.of, subject, d.status, d.text); err != nil {
				return err
			}
		}
		for _, r := range rows {
			h := r.Health
			if h.ProjectID.Valid && !ids[h.ProjectID.Int64] && projects != nil {
				continue
			}
			if _, ok := want[h.Subject]; ok || h.WorkspaceID.Int64 != ws || !ownDerived(h, scripts) && !gone(h) {
				continue
			}
			if err := health.Clear(ctx, q, health.Of{Workspace: ws}, h.Subject); err != nil {
				return err
			}
		}
		return nil
	})
}

// noModel is the start of the row Derive writes of a model deck has none of while assignments name it.
const noModel = "deck has no such model"

// gone reports whether a row is the one Derive writes of a model deck has none of.
func gone(h db.Health) bool {
	return strings.HasPrefix(h.Subject, "model:") && strings.HasPrefix(h.Error, noModel)
}

// ownDerived reports whether Derive writes a row: of a manifest, a secret, search, or of a command that is not of a
// script, whose row is of its programs; a manifest that does not read takes the rows of its commands along.
func ownDerived(h db.Health, scripts map[string]bool) bool {
	kind, _, _ := strings.Cut(h.Subject, ":")
	switch kind {
	case "project", "secret", health.Search:
		return true
	case "command":
		return !scripts[h.Subject]
	}
	return false
}
