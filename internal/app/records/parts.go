package records

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"deck2/internal/access"
	"deck2/internal/ask"
	"deck2/internal/docs"
	"deck2/internal/fault"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/paths"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

// part is a thing deleting a record takes with it: a record, a file, a folder of the data folder or an open process
// it stops.
type part struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	ID   int64  `json:"id,omitempty"`
}

// plan is what deleting records takes: they and their child records, their files and those of their processes, the
// folders no other record holds, and the processes still open.
type plan struct {
	records []db.Record
	files   []db.File
	folders []string
	open    []db.Process
}

// folderOf is the folder of a record; an expression that fails stops the plan.
func folderOf(c *manifest.Collection, r db.Record) (string, error) {
	data, err := manifest.DecodeObject([]byte(r.Data))
	if err == nil {
		var f string
		if f, err = c.FolderOf(data); err == nil {
			return f, nil
		}
	}
	return "", unplanned(r, err)
}

// unplanned names the record whose expression stops a plan and why, for the owner to fix its data or the manifest.
func unplanned(r db.Record, err error) error {
	return registry.Invalid("record %d with key %q of %s stops the deletion: %v; fix its data or the expression of the collection",
		r.ID, r.Key, r.Collection, err)
}

// plan is the one cascade of every deletion of records.
func (s *Service) plan(ctx context.Context, q *db.Queries, p db.Project, m *manifest.Manifest, roots ...db.Record) (plan, error) {
	var out plan
	var folders []string
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
	children := map[string]map[string][]db.Record{}
	kids := func(child *manifest.Collection, key string) ([]db.Record, error) {
		byKey, ok := children[child.Name]
		if !ok {
			rows, err := q.ListRecords(ctx, db.ListRecordsParams{ProjectID: p.ID, Collection: child.Name})
			if err != nil {
				return nil, err
			}
			byKey = map[string][]db.Record{}
			for _, k := range rows {
				var parent string
				var has bool
				data, err := manifest.DecodeObject([]byte(k.Data))
				if err == nil {
					parent, has, err = child.ParentKey(data)
				}
				if err != nil {
					return nil, unplanned(k, err)
				}
				if has {
					byKey[parent] = append(byKey[parent], k)
				}
			}
			children[child.Name] = byKey
		}
		return byKey[key], nil
	}
	var walk func(r db.Record) error
	walk = func(r db.Record) error {
		if seen[r.ID] {
			return nil
		}
		seen[r.ID] = true
		out.records = append(out.records, r)
		if c := m.Collection(r.Collection); c != nil {
			f, err := folderOf(c, r)
			if err != nil {
				return err
			}
			if f != "" && !slices.Contains(folders, f) {
				folders = append(folders, f)
			}
		}
		if err := files("record", r.ID); err != nil {
			return err
		}
		procs, err := q.ListRecordProcesses(ctx, db.ListRecordProcessesParams{ProjectID: p.ID, Record: r.ID})
		if err != nil {
			return err
		}
		for _, pr := range procs {
			if err := files("process", pr.ID); err != nil {
				return err
			}
			if !pr.FinishedAt.Valid {
				out.open = append(out.open, pr)
			}
		}
		for _, child := range m.Children(r.Collection) {
			ks, err := kids(child, r.Key)
			if err != nil {
				return err
			}
			for _, k := range ks {
				if err := walk(k); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, r := range roots {
		if err := walk(r); err != nil {
			return plan{}, err
		}
	}
	var err error
	out.folders, err = unheld(ctx, q, p, m, seen, folders)
	return out, err
}

// unheld are the folders no record outside the plan gives, nor a folder inside or around one of them.
func unheld(ctx context.Context, q *db.Queries, p db.Project, m *manifest.Manifest, planned map[int64]bool, folders []string) ([]string, error) {
	if len(folders) == 0 {
		return nil, nil
	}
	var held []string
	for i := range m.Collections {
		c := &m.Collections[i]
		if c.Folder == "" {
			continue
		}
		rows, err := q.ListRecords(ctx, db.ListRecordsParams{ProjectID: p.ID, Collection: c.Name})
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			if planned[r.ID] {
				continue
			}
			f, err := folderOf(c, r)
			if err != nil {
				return nil, err
			}
			if f != "" {
				held = append(held, f)
			}
		}
	}
	return slices.DeleteFunc(folders, func(f string) bool {
		return slices.ContainsFunc(held, func(h string) bool { return paths.Overlap(f, h) })
	}), nil
}

// parts are the plan as the owner reads it before saying yes.
func (pl plan) parts(m *manifest.Manifest) []part {
	out := []part{}
	for _, r := range pl.records {
		out = append(out, part{Kind: "record", Name: r.Collection + ": " + ViewOf(r, m.Collection(r.Collection)).Name(), ID: r.ID})
	}
	for _, f := range pl.files {
		out = append(out, part{Kind: "file", Name: f.Name, ID: f.ID})
	}
	for _, f := range pl.folders {
		out = append(out, part{Kind: "folder", Name: f + "/"})
	}
	for _, pr := range pl.open {
		out = append(out, part{Kind: "process", Name: pr.Command + ", " + pr.State, ID: pr.ID})
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
	if err != nil {
		return nil, err
	}
	return pl.parts(m), nil
}

// remove deletes the rows of a plan and puts its folders in the outbox, in the transaction that planned it.
func remove(ctx context.Context, q *db.Queries, p db.Project, pl plan) error {
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
	for _, f := range pl.folders {
		if err := q.AddFolderOutbox(ctx, db.AddFolderOutboxParams{Ref: p.ID, Path: f}); err != nil {
			return err
		}
	}
	return nil
}

// settle stops the open processes of a committed deletion but the caller's own, then removes its files and folders;
// only a deletion of the owner drops their frozen calls.
func (s *Service) settle(ctx context.Context, open []db.Process, caller int64, owner bool) {
	for _, pr := range open {
		if pr.ID == caller {
			continue
		}
		if err := s.Stop(ctx, pr.ID, "stopped: its record was deleted", owner); err != nil && fault.Status(err) >= 500 {
			slog.Error("stop a process of a deleted record", "process", pr.ID, "err", err)
		}
	}
}

// deleteRecord deletes a record with what belongs to it for good; outside a draft the master deletes only what it made
// or waits for the owner, an agent any record of its project, and either leaves a line whose undo writes the data back.
func (s *Service) deleteRecord(ctx context.Context, act registry.Actor, p db.Project, id int64) (any, error) {
	var pl plan
	var waiting *gateway.Waiting
	err := s.writeRecords(ctx, p, func(ctx context.Context, q *db.Queries, m *manifest.Manifest) error {
		rec, err := q.GetRecord(ctx, id)
		if errors.Is(err, sql.ErrNoRows) || err == nil && rec.ProjectID != p.ID {
			return registry.NotFound("record %d no longer exists", id)
		}
		if err != nil {
			return err
		}
		cur, err := q.GetProject(ctx, p.ID)
		if err != nil {
			return err
		}
		if pl, err = s.plan(ctx, q, p, m, rec); err != nil {
			return err
		}
		other := act.Kind != registry.Owner && cur.Status != store.ProjectDraft
		if other && act.Kind != registry.Agent {
			others, err := mastersOnly(ctx, q, p, pl)
			if err != nil {
				return err
			}
			if others != "" {
				w, err := s.hold(ctx, q, act, p, m, pl, others)
				waiting = &w
				return err
			}
		}
		if err := remove(ctx, q, p, pl); err != nil {
			return err
		}
		if !other {
			return nil
		}
		params, _ := json.Marshal(map[string]any{"project": p.Name, "collection": rec.Collection, "records": rec.Data})
		return store.AddChange(ctx, q, db.AddFeedParams{CreatedAt: store.Time(s.Now()), WorkspaceID: p.WorkspaceID, ProjectID: p.ID,
			Author: act.Author(), Text: fmt.Sprintf("deleted %s with %d records, %d files and %d folders in all; an undo writes back its data, not what belonged to it",
				pl.parts(m)[0].Name, len(pl.records), len(pl.files), len(pl.folders)),
			Actions: "[]", Command: registry.Deck("records_upsert"), Params: string(params), ProcessID: store.ID(act.ProcessID),
			What: "deleted records of " + rec.Collection})
	})
	if err != nil {
		return nil, err
	}
	if waiting != nil {
		return *waiting, nil
	}
	s.settle(ctx, pl.open, act.ProcessID, act.Kind == registry.Owner)
	return map[string]any{"ok": true}, nil
}

// mastersOnly refuses a plan with a record no process of the master wrote, and names the first thing else in it the
// master did not make: a file another's process made or got, a folder, another's open process.
func mastersOnly(ctx context.Context, q *db.Queries, p db.Project, pl plan) (others string, err error) {
	mp, err := access.Master(ctx, q, p.WorkspaceID)
	if err != nil {
		return "", err
	}
	mine := func(process sql.NullInt64) (bool, error) {
		if !process.Valid {
			return false, nil
		}
		root, err := q.RootProjectOf(ctx, process.Int64)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return err == nil && root == mp.ID, err
	}
	for _, r := range pl.records {
		if ok, err := mine(r.ProcessID); err != nil || !ok {
			return "", cmp.Or(err, registry.Refuse("only the owner deletes records of an enabled project; the master only those its own processes wrote, with all that belongs to them"))
		}
	}
	if len(pl.folders) > 0 {
		return fmt.Sprintf("the folder %s/ of the data folder: deck does not know who wrote in it", pl.folders[0]), nil
	}
	for _, f := range pl.files {
		maker := f.ProcessID
		if f.OwnerKind == "process" {
			maker = store.ID(f.OwnerID)
		}
		if ok, err := mine(maker); err != nil || !ok {
			return fmt.Sprintf("the file %s: no process of the master made it", f.Name), err
		}
	}
	for _, pr := range pl.open {
		if ok, err := mine(store.ID(pr.ID)); err != nil || !ok {
			return fmt.Sprintf("the open process %d %s: not of the master", pr.ID, pr.Command), err
		}
	}
	return "", nil
}

// hold freezes the deletion until the owner, who reads what it takes, says yes; deck then makes it as the owner.
// The same deletion already waiting is the same ask.
func (s *Service) hold(ctx context.Context, q *db.Queries, act registry.Actor, p db.Project, m *manifest.Manifest, pl plan, others string) (gateway.Waiting, error) {
	rec := pl.records[0]
	input, _ := json.Marshal(map[string]any{"record": rec.ID})
	f := gateway.Freeze(act, registry.Deck("record_delete"), input)
	f.As = gateway.AsOwner
	a, err := q.FindOpenCallAsk(ctx, db.FindOpenCallAskParams{ProjectID: p.ID, Payload: f.String(), ProcessID: act.ProcessID})
	if errors.Is(err, sql.ErrNoRows) {
		var b strings.Builder
		parts := pl.parts(m)
		fmt.Fprintf(&b, "%s wants to delete %s of %s for good, with what it did not make:\n", act.Author(), untrusted.Line(parts[0].Name, 200), p.Name)
		for _, pt := range parts {
			fmt.Fprintf(&b, "- %s %s\n", pt.Kind, untrusted.Line(pt.Name, 200))
		}
		a, err = gateway.Hold(ctx, q, act, p.ID, act.ProcessID, ask.KindCall, registry.Internal, untrusted.Cut(b.String(), 4000), f, time.Time{}, s.Now())
	}
	return gateway.Waiting{Ask: a.ID, Text: fmt.Sprintf("deck: deleting record %d takes what the master did not make (%s), so it waits for the owner's confirmation (ask %d); "+
		"nothing was deleted yet. Go on with other work: once the owner allows it, deck deletes it and tells you.", rec.ID, untrusted.Line(others, 300), a.ID)}, err
}
