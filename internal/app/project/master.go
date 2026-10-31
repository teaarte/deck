package project

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"
	"slices"

	"deck2/internal/docs"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/master"
)

// SeedMaster ships the built-in project master into every workspace with docs.Ship; the master gets a note of
// each edit deck could not carry whole.
func (pj *Service) SeedMaster(ctx context.Context) error {
	raw, err := fs.ReadFile(master.FS, docs.ManifestName)
	if err != nil {
		return err
	}
	m, err := manifest.Parse(raw)
	if err != nil {
		return err
	}
	files, err := docs.Files(master.FS, "", func(name string) string { return moduleKind(m, name) })
	if err != nil {
		return err
	}
	ws, err := pj.St.Q.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, w := range ws {
		if err := pj.seedMaster(ctx, w, files); err != nil {
			return err
		}
	}
	return nil
}

func (pj *Service) seedMaster(ctx context.Context, workspace int64, files []docs.File) error {
	return pj.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		p, err := q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: master.Name})
		switch {
		case errors.Is(err, sql.ErrNoRows):
			if p, err = q.CreateProject(ctx, db.CreateProjectParams{WorkspaceID: workspace, Name: master.Name, Status: store.ProjectEnabled}); err != nil {
				return err
			}
			if err := q.SetProjectBuiltin(ctx, p.ID); err != nil {
				return err
			}
		case err != nil:
			return err
		case p.Builtin == 0:
			slog.Error("a project of the owner is named master, so the built-in master is missing", "workspace", workspace)
			_, err := health.Set(ctx, q, health.Of{Workspace: workspace}, health.Master, health.Error,
				"a project of the owner is named master, so the built-in master is missing: export that project, delete it and import it under another name")
			return err
		}
		if err := health.Clear(ctx, q, health.Of{Workspace: workspace}, health.Master); err != nil {
			return err
		}
		done, err := docs.Ship(ctx, q, docs.ProjectScope(p), "", files, pj.Now())
		if err != nil {
			return err
		}
		for _, n := range done.Notes {
			if err := q.AddMasterNote(ctx, db.AddMasterNoteParams{CreatedAt: store.Time(pj.Now()), WorkspaceID: workspace, ProjectID: p.ID, Text: n}); err != nil {
				return err
			}
		}
		if !slices.Contains(done.Changed, docs.ManifestName) {
			return nil
		}
		cur, err := docs.Manifest(ctx, q, p)
		if err != nil {
			return err
		}
		return pj.Scheduler.Sync(ctx, q, p.ID, cur)
	})
}
