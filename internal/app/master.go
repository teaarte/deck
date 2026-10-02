package app

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"

	"deck2/internal/docs"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/master"
)

// SeedMaster stores the built-in project master in every workspace; a file someone else edited stays theirs.
func (a *App) SeedMaster(ctx context.Context) error {
	files := map[string]string{}
	err := fs.WalkDir(master.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := fs.ReadFile(master.FS, path)
		files[path] = string(b)
		return err
	})
	if err != nil {
		return err
	}
	m, err := manifest.Parse([]byte(files[docs.ManifestName]))
	if err != nil {
		return err
	}
	ws, err := a.Store.Q.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, w := range ws {
		if err := a.seedMaster(ctx, w, m, files); err != nil {
			return err
		}
	}
	a.Bus.Publish("projects")
	return nil
}

func (a *App) seedMaster(ctx context.Context, workspace int64, m *manifest.Manifest, files map[string]string) error {
	return a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
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
			return health.Set(ctx, q, health.Of{Workspace: workspace}, "project:"+master.Name, "error",
				"a project of the owner is named master, so the built-in master is missing: export that project, delete it and import it under another name")
		}
		changed := false
		for path, text := range files {
			ref := docs.Ref{Scope: docs.ProjectScope(p), Name: path}
			d, err := q.FindAnyDocument(ctx, db.FindAnyDocumentParams(ref.Find()))
			if err == nil && d.DeletedAt.Valid {
				continue
			}
			if err == nil {
				r, err := docs.Revision(ctx, q, d, 0)
				if err != nil {
					return err
				}
				if r.Author != "deck" || r.Text == text {
					continue
				}
			} else if !errors.Is(err, sql.ErrNoRows) {
				return err
			}
			if _, _, err := docs.Save(ctx, q, ref, moduleKind(m, path), text, "deck", "built-in project"); err != nil {
				return err
			}
			changed = changed || path == docs.ManifestName
		}
		if !changed {
			return nil
		}
		cur, err := docs.Manifest(ctx, q, p)
		if err != nil {
			return err
		}
		return a.Scheduler.Sync(ctx, q, p.ID, cur)
	})
}

// masterProject is the built-in project master of a workspace.
func (a *App) masterProject(ctx context.Context, workspace int64) (db.Project, error) {
	p, err := a.Store.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: master.Name})
	if err != nil || p.Builtin == 0 {
		return db.Project{}, registry.Invalid("deck: this workspace has no built-in project master")
	}
	return p, nil
}
