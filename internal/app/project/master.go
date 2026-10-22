package project

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"log/slog"
	"time"

	"deck2/internal/docs"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/master"
)

// SeedMaster stores the built-in project master in every workspace; a file someone else edited stays theirs.
func (pj *Service) SeedMaster(ctx context.Context) error {
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
	ws, err := pj.St.Q.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, w := range ws {
		if err := pj.seedMaster(ctx, w, m, files); err != nil {
			return err
		}
	}
	return nil
}

func (pj *Service) seedMaster(ctx context.Context, workspace int64, m *manifest.Manifest, files map[string]string) error {
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
		changed := false
		for path, text := range files {
			ref := docs.Ref{Scope: docs.ProjectScope(p), Name: path}
			d, err := q.FindDocument(ctx, ref.Find())
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
			if _, _, _, err := docs.Save(ctx, q, ref, moduleKind(m, path), text, "deck", "built-in project"); err != nil {
				return err
			}
			changed = changed || path == docs.ManifestName
		}
		if err := dropUnshipped(ctx, q, p, files, pj.Now()); err != nil {
			return err
		}
		if !changed {
			return nil
		}
		cur, err := docs.Manifest(ctx, q, p)
		if err != nil {
			return err
		}
		return pj.Scheduler.Sync(ctx, q, p.ID, cur)
	})
}

// dropUnshipped deletes the documents deck wrote into project master that it ships no more; one the owner or an
// agent changed since stays.
func dropUnshipped(ctx context.Context, q *db.Queries, p db.Project, files map[string]string, now time.Time) error {
	ds, err := q.ListDocuments(ctx, db.ListDocumentsParams{WorkspaceID: p.WorkspaceID, ProjectID: store.ID(p.ID)})
	if err != nil {
		return err
	}
	for _, d := range ds {
		if _, shipped := files[d.Name]; shipped || d.RecordID.Valid {
			continue
		}
		r, err := docs.Revision(ctx, q, d, 0)
		if err != nil {
			return err
		}
		if r.Author != "deck" {
			continue
		}
		if _, err := q.DeleteDocument(ctx, db.DeleteDocumentParams{DeletedAt: sql.NullString{String: store.Time(now), Valid: true}, ID: d.ID}); err != nil {
			return err
		}
	}
	return nil
}
