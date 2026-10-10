package records

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// trashKept is how long a deletion waits for an undo before it is for good.
const trashKept = 24 * time.Hour

// part is a thing deleting a record takes with it: a record, a file or a folder of the data folder.
type part struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	ID   int64  `json:"id,omitempty"`
}

// plan is what deleting a record takes: the record and the records that belong to it, their files and those of the
// processes whose input field record names them, and their folders.
type plan struct {
	records []db.Record
	files   []db.File
	folders []string
}

func (s *Service) plan(ctx context.Context, q *db.Queries, p db.Project, m *manifest.Manifest, rec db.Record) (plan, error) {
	var out plan
	seen, kept := map[int64]bool{}, map[int64]bool{}
	files := func(kind string, owner int64) error {
		fs, err := q.ListOwnedFiles(ctx, db.ListOwnedFilesParams{OwnerKind: kind, OwnerID: owner})
		for _, f := range fs {
			if f.ProjectID == p.ID && !kept[f.ID] {
				kept[f.ID], out.files = true, append(out.files, f)
			}
		}
		return err
	}
	var walk func(r db.Record) error
	walk = func(r db.Record) error {
		if seen[r.ID] {
			return nil
		}
		seen[r.ID] = true
		out.records = append(out.records, r)
		var data map[string]any
		_ = json.Unmarshal([]byte(r.Data), &data)
		if c := m.Collection(r.Collection); c != nil {
			if f := c.FolderOf(data); f != "" {
				out.folders = append(out.folders, f)
			}
		}
		if err := files("record", r.ID); err != nil {
			return err
		}
		procs, err := q.ListProcessesFiltered(ctx, db.ListProcessesFilteredParams{WorkspaceID: p.WorkspaceID, ProjectID: p.ID, Record: r.ID, Lim: 1000})
		if err != nil {
			return err
		}
		for _, pr := range procs {
			var in struct {
				Record any `json:"record"`
			}
			if json.Unmarshal([]byte(pr.Input), &in) != nil || fmt.Sprint(in.Record) != fmt.Sprint(r.ID) {
				continue
			}
			if err := files("process", pr.ID); err != nil {
				return err
			}
		}
		for _, child := range m.Children(r.Collection) {
			rows, err := q.ListRecords(ctx, db.ListRecordsParams{ProjectID: p.ID, Collection: child.Name})
			if err != nil {
				return err
			}
			for _, k := range rows {
				var kd map[string]any
				_ = json.Unmarshal([]byte(k.Data), &kd)
				if key, ok := child.ParentKey(kd); ok && key == r.Key {
					if err := walk(k); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	return out, walk(rec)
}

// parts are the plan as the owner reads it before saying yes.
func (pl plan) parts(m *manifest.Manifest) []part {
	out := []part{}
	for _, r := range pl.records {
		var data map[string]any
		_ = json.Unmarshal([]byte(r.Data), &data)
		name := r.Key
		if c := m.Collection(r.Collection); c != nil && c.RecordTitle(data) != "" {
			name = c.RecordTitle(data)
		}
		out = append(out, part{Kind: "record", Name: r.Collection + ": " + name, ID: r.ID})
	}
	for _, f := range pl.files {
		out = append(out, part{Kind: "file", Name: f.Name, ID: f.ID})
	}
	for _, f := range pl.folders {
		out = append(out, part{Kind: "folder", Name: f + "/"})
	}
	return out
}

type recordPartsIn struct {
	Record int64 `type:"record" desc:"Record id"`
}

// recordParts lists what deleting a record takes with it.
func (s *Service) recordParts(ctx context.Context, act registry.Actor, in recordPartsIn) ([]part, error) {
	rec, p, err := s.recordOf(ctx, act, in.Record)
	if err != nil {
		return nil, err
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return nil, err
	}
	pl, err := s.plan(ctx, s.St.Q, p, m, rec)
	return pl.parts(m), err
}

// trashedFile is a file moved to the trash and the owner it goes back to.
type trashedFile struct {
	ID        int64  `json:"id"`
	OwnerKind string `json:"owner_kind"`
	OwnerID   int64  `json:"owner_id"`
}

// trashRecords moves a record with its parts to the trash in one transaction and tells it in the feed with an undo;
// its folders move after the commit. It gives the trash entry.
func (s *Service) trashRecords(ctx context.Context, act registry.Actor, p db.Project, rec db.Record) (db.Trash, error) {
	var t db.Trash
	var folders []string
	now := s.Now()
	err := s.writeRecords(ctx, p, func(ctx context.Context, q *db.Queries, m *manifest.Manifest) error {
		pl, err := s.plan(ctx, q, p, m, rec)
		if err != nil {
			return err
		}
		folders = pl.folders
		var files []trashedFile
		for _, f := range pl.files {
			files = append(files, trashedFile{ID: f.ID, OwnerKind: f.OwnerKind, OwnerID: f.OwnerID})
		}
		recs, _ := json.Marshal(pl.records)
		fs, _ := json.Marshal(files)
		title := pl.parts(m)[0].Name
		what := fmt.Sprintf("deleted %s with %d records, %d files and %d folders in all; an undo within a day brings them back",
			title, len(pl.records), len(pl.files), len(pl.folders))
		if t, err = q.AddTrash(ctx, db.AddTrashParams{WorkspaceID: p.WorkspaceID, ProjectID: p.ID, What: what, RecordID: store.ID(rec.ID),
			Records: string(recs), Documents: "[]", Files: string(fs), CreatedAt: store.Time(now)}); err != nil {
			return err
		}
		for _, f := range pl.files {
			if _, err := q.BindFile(ctx, db.BindFileParams{OwnerKind: "trash", OwnerID: now.Unix(), ID: f.ID, ProjectID: p.ID,
				FromKind: f.OwnerKind, FromID: f.OwnerID}); err != nil {
				return err
			}
		}
		for _, r := range pl.records {
			if err := q.TrashRecordDocuments(ctx, db.TrashRecordDocumentsParams{At: sql.NullString{String: t.CreatedAt, Valid: true}, RecordID: store.ID(r.ID)}); err != nil {
				return err
			}
			if err := q.DeleteRecord(ctx, r.ID); err != nil {
				return err
			}
		}
		return s.undoNote(ctx, q, act, p, t)
	})
	if err != nil {
		return t, err
	}
	s.movePaths(ctx, p, t, folders)
	return t, nil
}

// undoNote tells the feed of a deletion, with the button that takes it back.
func (s *Service) undoNote(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project, t db.Trash) error {
	params, _ := json.Marshal(map[string]any{"trash": t.ID})
	_, err := q.AddFeed(ctx, db.AddFeedParams{CreatedAt: t.CreatedAt, WorkspaceID: p.WorkspaceID, ProjectID: p.ID, Kind: "change",
		Author: act.Author(), Text: t.What, Actions: "[]", Command: registry.Builtin + ".trash_restore", Params: string(params),
		ProcessID: store.ID(act.ProcessID)})
	return err
}

// The folder of a project holds its data folder and its trash; every move goes through it as a root, so no link
// takes a path of either outside.
const (
	dataDir  = "data"
	trashDir = "trash"
)

func trashOf(t int64, path string) string { return filepath.Join(trashDir, fmt.Sprint(t), path) }

func (s *Service) projectRoot(project int64) (*os.Root, error) {
	if err := os.MkdirAll(s.Home.Project(project), 0o700); err != nil {
		return nil, err
	}
	return os.OpenRoot(s.Home.Project(project))
}

// clean is a path inside a data folder, or empty for one that names the folder itself.
func clean(path string) string {
	return strings.TrimPrefix(filepath.Clean("/"+path), "/")
}

// movePaths moves paths of the data folder of a project under its trash entry and records those that moved; one
// that is gone or inside one moved already is skipped.
func (s *Service) movePaths(ctx context.Context, p db.Project, t db.Trash, paths []string) []string {
	root, err := s.projectRoot(p.ID)
	if err != nil {
		slog.Error("trash", "project", p.Name, "err", err)
		return nil
	}
	defer root.Close()
	var moved []string
	for _, path := range paths {
		path = clean(path)
		src, dst := filepath.Join(dataDir, path), trashOf(t.ID, path)
		if path == "" {
			continue
		}
		if _, err := root.Lstat(src); err != nil {
			continue
		}
		if err := errors.Join(root.MkdirAll(filepath.Dir(dst), 0o700), root.Rename(src, dst)); err != nil {
			slog.Error("trash", "path", src, "err", err)
			continue
		}
		moved = append(moved, path)
	}
	raw, _ := json.Marshal(moved)
	if err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.SetTrashPaths(ctx, db.SetTrashPathsParams{Paths: string(raw), ID: t.ID})
	}); err != nil {
		slog.Error("trash paths", "trash", t.ID, "err", err)
	}
	return moved
}

type dataDeleteIn struct {
	Project string `type:"project" desc:"Project name"`
	Path    string `desc:"A file or folder inside the data folder"`
}

// dataDelete moves a file or folder of a data folder to the trash, with an undo for a day.
func (s *Service) dataDelete(ctx context.Context, act registry.Actor, in dataDeleteIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	path := clean(in.Path)
	if path == "" {
		return nil, registry.Invalid("deck: name a file or folder inside the data folder, not the folder itself")
	}
	var t db.Trash
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		t, err = q.AddTrash(ctx, db.AddTrashParams{WorkspaceID: p.WorkspaceID, ProjectID: p.ID, Path: path, Records: "[]", Documents: "[]", Files: "[]",
			What: fmt.Sprintf("deleted %s of the data folder of %s; an undo within a day brings it back", path, p.Name), CreatedAt: store.Time(s.Now())})
		return err
	})
	if err != nil {
		return nil, err
	}
	if len(s.movePaths(ctx, p, t, []string{path})) == 0 {
		_ = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return q.DeleteTrash(ctx, t.ID) })
		return nil, registry.NotFound("deck: no %q in the data folder of %s to delete", in.Path, p.Name)
	}
	if err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error { return s.undoNote(ctx, q, act, p, t) }); err != nil {
		return nil, err
	}
	return map[string]any{"trash": t.ID}, nil
}

type trashRestoreIn struct {
	Trash   int64  `opt:"" desc:"The trash entry to bring back"`
	Record  int64  `type:"record" opt:"" desc:"Or the record whose last deletion to bring back"`
	Project string `type:"project" opt:"" desc:"With path: the project of the data folder"`
	Path    string `opt:"" desc:"Or the path of the data folder whose last deletion to bring back"`
}

// trashRestore brings back what a deletion took: it checks that nothing of it exists again, moves its paths back,
// then brings its records, their documents and files back in one transaction, moving the paths again if that fails.
func (s *Service) trashRestore(ctx context.Context, act registry.Actor, in trashRestoreIn) (any, error) {
	t, err := s.findTrash(ctx, act, in)
	if err != nil {
		return nil, err
	}
	var paths []string
	var recs []db.Record
	var files []trashedFile
	_ = json.Unmarshal([]byte(t.Paths), &paths)
	_ = json.Unmarshal([]byte(t.Records), &recs)
	_ = json.Unmarshal([]byte(t.Files), &files)
	for _, r := range recs {
		if _, err := s.St.Q.GetRecordByKey(ctx, db.GetRecordByKeyParams{ProjectID: r.ProjectID, Collection: r.Collection, Key: r.Key}); err == nil {
			return nil, registry.Conflict("deck: a record %s of %s exists again; delete it first", r.Key, r.Collection)
		}
	}
	root, err := s.projectRoot(t.ProjectID)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	for _, path := range paths {
		if _, err := root.Lstat(filepath.Join(dataDir, path)); err == nil {
			return nil, registry.Conflict("deck: %s of the data folder exists again; move it away first", path)
		}
	}
	var back []string
	undo := func() {
		for _, path := range back {
			_ = root.Rename(filepath.Join(dataDir, path), trashOf(t.ID, path))
		}
	}
	for _, path := range paths {
		dst := filepath.Join(dataDir, path)
		if err := errors.Join(root.MkdirAll(filepath.Dir(dst), 0o700), root.Rename(trashOf(t.ID, path), dst)); err != nil {
			undo()
			return nil, err
		}
		back = append(back, path)
	}
	at, _ := time.Parse(time.RFC3339Nano, t.CreatedAt)
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		for _, r := range recs {
			if err := q.RestoreRecord(ctx, db.RestoreRecordParams{ID: r.ID, ProjectID: r.ProjectID, Collection: r.Collection, Key: r.Key, Data: r.Data,
				CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt, ProcessID: r.ProcessID}); err != nil {
				return err
			}
			if err := q.RestoreRecordDocuments(ctx, db.RestoreRecordDocumentsParams{RecordID: store.ID(r.ID), At: sql.NullString{String: t.CreatedAt, Valid: true}}); err != nil {
				return err
			}
		}
		for _, f := range files {
			if _, err := q.BindFile(ctx, db.BindFileParams{OwnerKind: f.OwnerKind, OwnerID: f.OwnerID, ID: f.ID, ProjectID: t.ProjectID,
				FromKind: "trash", FromID: at.Unix()}); err != nil {
				return err
			}
		}
		return q.DeleteTrash(ctx, t.ID)
	})
	if err != nil {
		undo()
		return nil, err
	}
	_ = root.RemoveAll(trashOf(t.ID, ""))
	return map[string]any{"records": len(recs), "files": len(files), "paths": paths}, nil
}

func (s *Service) findTrash(ctx context.Context, act registry.Actor, in trashRestoreIn) (db.Trash, error) {
	gone := registry.NotFound("deck: nothing to bring back; a deletion is kept for a day")
	var t db.Trash
	var err error
	switch {
	case in.Trash != 0:
		t, err = s.St.Q.GetTrash(ctx, in.Trash)
	case in.Record != 0:
		var projects []db.Project
		if projects, err = s.St.Q.ListWorkspaceProjects(ctx, act.Workspace); err == nil {
			for _, p := range projects {
				if t, err = s.St.Q.LastTrashOf(ctx, db.LastTrashOfParams{ProjectID: p.ID, RecordID: store.ID(in.Record)}); err == nil {
					break
				}
			}
		}
	case in.Path != "":
		var p db.Project
		if p, err = access.Project(ctx, s.St.Q, act, in.Project); err == nil {
			t, err = s.St.Q.LastTrashOf(ctx, db.LastTrashOfParams{ProjectID: p.ID, Path: clean(in.Path)})
		}
	default:
		return t, registry.Invalid("deck: name the trash, the record or the path to bring back")
	}
	if err != nil || t.WorkspaceID != act.Workspace {
		return t, gone
	}
	return t, nil
}

// PurgeTrash deletes for good what was deleted more than a day ago: the documents of its records and its paths; its
// files go by the purge of trashed files.
func (s *Service) PurgeTrash(ctx context.Context) {
	old, err := s.St.Q.ListOldTrash(ctx, store.Time(s.Now().Add(-trashKept)))
	if err != nil {
		return
	}
	for _, t := range old {
		var recs []db.Record
		_ = json.Unmarshal([]byte(t.Records), &recs)
		err := s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			for _, r := range recs {
				if err := q.DeleteOneRecordDocuments(ctx, store.ID(r.ID)); err != nil {
					return err
				}
			}
			return q.DeleteTrash(ctx, t.ID)
		})
		if err != nil {
			slog.Error("purge trash", "trash", t.ID, "err", err)
			continue
		}
		if root, err := s.projectRoot(t.ProjectID); err == nil {
			_ = root.RemoveAll(trashOf(t.ID, ""))
			_ = root.Close()
		}
	}
}
