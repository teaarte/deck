package records

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

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

// deleteRecords deletes a record with what belongs to it for good, its folders after the commit; the master's
// deletion leaves a line in the feed whose undo writes back the data of the record.
func (s *Service) deleteRecords(ctx context.Context, act registry.Actor, p db.Project, rec db.Record, own bool) error {
	var folders []string
	err := s.writeRecords(ctx, p, func(ctx context.Context, q *db.Queries, m *manifest.Manifest) error {
		pl, err := s.plan(ctx, q, p, m, rec)
		if err != nil {
			return err
		}
		folders = pl.folders
		for _, f := range pl.files {
			if err := q.DeleteFile(ctx, f.ID); err != nil {
				return err
			}
		}
		for _, r := range pl.records {
			if err := q.DeleteOneRecordDocuments(ctx, store.ID(r.ID)); err != nil {
				return err
			}
			if err := q.DeleteRecord(ctx, r.ID); err != nil {
				return err
			}
		}
		if !own {
			return nil
		}
		params, _ := json.Marshal(map[string]any{"project": p.Name, "collection": rec.Collection, "records": rec.Data})
		_, err = q.AddFeed(ctx, db.AddFeedParams{CreatedAt: store.Time(s.Now()), WorkspaceID: p.WorkspaceID, ProjectID: p.ID, Kind: "change",
			Author: act.Author(), Text: fmt.Sprintf("deleted %s with %d records, %d files and %d folders in all; an undo writes back its data, not what belonged to it",
				pl.parts(m)[0].Name, len(pl.records), len(pl.files), len(pl.folders)),
			Actions: "[]", Command: registry.Builtin + ".records_upsert", Params: string(params), ProcessID: store.ID(act.ProcessID)})
		return err
	})
	if err != nil {
		return err
	}
	s.removeFolders(p, folders)
	s.DrainFiles(ctx)
	return nil
}

// removeFolders deletes folders of the data folder of a project through it as a root, so no link leads outside.
func (s *Service) removeFolders(p db.Project, folders []string) {
	if len(folders) == 0 {
		return
	}
	root, err := os.OpenRoot(s.Home.Data(p.ID))
	if err != nil {
		return
	}
	defer root.Close()
	for _, f := range folders {
		f = strings.TrimPrefix(filepath.Clean("/"+f), "/")
		if f == "" {
			continue
		}
		if err := root.RemoveAll(f); err != nil {
			slog.Error("delete record folder", "project", p.Name, "folder", f, "err", err)
		}
	}
}
