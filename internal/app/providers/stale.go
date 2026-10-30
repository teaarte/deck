package providers

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"

	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/process"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Follow prunes the assignments of commands a manifest no longer has whenever a manifest or a project changes.
func (s *Service) Follow(bus *events.Bus) {
	bus.Follow(s.Go, []string{"documents", "projects"}, s.Prune)
}

// Prune removes the assignments of commands gone or no longer of an agent, every workspace for zero and of the
// projects named, every one for nil; a project whose manifest does not read keeps them.
func (s *Service) Prune(ctx context.Context, workspace int64, projects []string) {
	spaces := []int64{workspace}
	if workspace == 0 {
		var err error
		if spaces, err = s.St.Q.ListWorkspaces(ctx); err != nil {
			slog.Error("prune assignments", "err", err)
			return
		}
	}
	for _, ws := range spaces {
		if err := s.prune(ctx, ws, projects); err != nil && ctx.Err() == nil {
			slog.Error("prune assignments", "workspace", ws, "err", err)
		}
	}
}

func (s *Service) prune(ctx context.Context, ws int64, projects []string) error {
	rows, err := s.St.Q.ListAssignments(ctx, ws)
	if err != nil {
		return err
	}
	var gone []db.ListAssignmentsRow
	for _, r := range rows {
		if r.Command == "" || !r.ProjectID.Valid || projects != nil && !slices.Contains(projects, r.Project) {
			continue
		}
		p, err := s.St.Q.GetProject(ctx, r.ProjectID.Int64)
		if err != nil {
			continue
		}
		m, err := docs.Manifest(ctx, s.St.Q, p)
		if err != nil {
			continue
		}
		c := m.Command(r.Command)
		if c == nil || (r.Role == RoleAgent || r.Role == RoleSearch) && c.Executor.Type != process.KindAgent {
			gone = append(gone, r)
		}
	}
	if len(gone) == 0 {
		return nil
	}
	return s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		for _, r := range gone {
			slog.Info("assignment of a command gone removed", "project", r.Project, "command", r.Command, "role", r.Role)
			err := q.DeleteAssignment(ctx, db.DeleteAssignmentParams{WorkspaceID: ws, ProjectID: r.ProjectID.Int64, Command: r.Command, Role: r.Role})
			if err != nil {
				return err
			}
		}
		return nil
	})
}

// dropModel takes a model out of every assignment of a workspace; an assignment left with none goes.
func dropModel(ctx context.Context, q *db.Queries, ws int64, name string) error {
	rows, err := q.ListAssignments(ctx, ws)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var names []string
		if json.Unmarshal([]byte(r.Models), &names) != nil || !slices.Contains(names, name) {
			continue
		}
		names = slices.DeleteFunc(names, func(n string) bool { return n == name })
		if len(names) == 0 {
			err = q.DeleteAssignment(ctx, db.DeleteAssignmentParams{WorkspaceID: ws, ProjectID: r.ProjectID.Int64, Command: r.Command, Role: r.Role})
		} else {
			raw, _ := json.Marshal(names)
			err = q.SetAssignment(ctx, db.SetAssignmentParams{WorkspaceID: ws, ProjectID: store.ID(r.ProjectID.Int64), Command: r.Command, Role: r.Role, Models: string(raw)})
		}
		if err != nil {
			return err
		}
	}
	return nil
}

const noModel = "deck has no such model, so the work skips it"

// exists reports whether a name is a built-in provider or a model of the workspace.
func (s *Service) exists(ctx context.Context, workspace int64, name string) bool {
	if s.builtinNamed(name) != nil {
		return true
	}
	_, err := s.St.Q.GetModel(ctx, db.GetModelParams{WorkspaceID: workspace, Name: name})
	return err == nil
}

// Missing are the names assignments of a workspace hold that are no model of deck, each with the places that name it.
func (s *Service) Missing(ctx context.Context, workspace int64) map[string][]string {
	rows, err := s.St.Q.ListAssignments(ctx, workspace)
	if err != nil {
		return nil
	}
	out := map[string][]string{}
	for _, r := range rows {
		var names []string
		_ = json.Unmarshal([]byte(r.Models), &names)
		place := r.Role
		switch {
		case r.Command != "":
			place = r.Role + " of " + r.Project + "." + r.Command
		case r.Project != "":
			place = r.Role + " of " + r.Project
		}
		for _, name := range names {
			if !s.exists(ctx, workspace, name) {
				out[name] = append(out[name], place)
			}
		}
	}
	return out
}
