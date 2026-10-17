package records

import (
	"bufio"
	"cmp"
	"context"
	"database/sql"
	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
)

type UpsertResult struct {
	Written int `json:"written"`
	// IDs are the ids of the written records, in order.
	IDs    []int64  `json:"ids"`
	Errors []string `json:"errors,omitempty"`
}

// UpsertJSONL writes one record per line; records with an existing key are updated. A new record keeps the process
// that wrote it, when one did.
func UpsertJSONL(ctx context.Context, q *db.Queries, now time.Time, projectID, process int64, m *manifest.Manifest, collection, jsonl string) (UpsertResult, error) {
	var recs []map[string]any
	var out UpsertResult
	sc := bufio.NewScanner(strings.NewReader(jsonl))
	sc.Buffer(make([]byte, 1<<20), 16<<20)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		r, err := manifest.DecodeObject([]byte(text))
		if err != nil {
			out.Errors = append(out.Errors, fmt.Sprintf("line %d: %v", line, err))
			continue
		}
		recs = append(recs, r)
	}
	if err := sc.Err(); err != nil {
		return out, registry.Invalid("deck: records: %v", err)
	}
	res, err := upsert(ctx, q, now, projectID, process, m, collection, recs)
	res.Errors = append(out.Errors, res.Errors...)
	return res, err
}

// upsert validates each record against the collection schema and writes it by its key; run it in
// a transaction so the immutable-field check and the write see the same record.
func upsert(ctx context.Context, q *db.Queries, now time.Time, projectID, process int64, m *manifest.Manifest, collection string, recs []map[string]any) (UpsertResult, error) {
	c := m.Collection(collection)
	if c == nil {
		return UpsertResult{}, registry.Invalid("deck: unknown collection %q", collection)
	}
	var out UpsertResult
	err := func() error {
		for i, r := range recs {
			key, err := c.RecordKey(r)
			if err != nil {
				out.Errors = append(out.Errors, fmt.Sprintf("records[%d]: %v", i, err))
				continue
			}
			prev, err := q.GetRecordByKey(ctx, db.GetRecordByKeyParams{ProjectID: projectID, Collection: collection, Key: key})
			switch {
			case err == nil:
				old, derr := manifest.DecodeObject([]byte(prev.Data))
				if derr == nil {
					if err := c.CheckImmutable(old, r); err != nil {
						out.Errors = append(out.Errors, fmt.Sprintf("records[%d]: %v", i, err))
						continue
					}
				}
			case !errors.Is(err, sql.ErrNoRows):
				return err
			}
			data := manifest.JSON(r, "")
			rec, err := q.UpsertRecord(ctx, db.UpsertRecordParams{ProjectID: projectID, Collection: collection, Key: key, Data: string(data), Now: store.Time(now),
				ProcessID: store.ID(process)})
			if err != nil {
				return err
			}
			out.Written++
			out.IDs = append(out.IDs, rec.ID)
		}
		return nil
	}()
	if err != nil {
		return UpsertResult{}, err
	}
	return out, nil
}

// Update replaces the data of a record; the key and immutable fields must stay the same.
func Update(ctx context.Context, q *db.Queries, now time.Time, rec db.Record, m *manifest.Manifest, data map[string]any) error {
	c := m.Collection(rec.Collection)
	if c == nil {
		return registry.Invalid("deck: collection %q no longer exists", rec.Collection)
	}
	key, err := c.RecordKey(data)
	if err != nil {
		return registry.Invalid("deck: %v", err)
	}
	if key != rec.Key {
		return registry.Invalid("deck: the change would move the record key from %q to %q; key fields cannot change", rec.Key, key)
	}
	if old, err := manifest.DecodeObject([]byte(rec.Data)); err == nil {
		if err := c.CheckImmutable(old, data); err != nil {
			return registry.Invalid("deck: %v", err)
		}
	}
	b := manifest.JSON(data, "")
	n, err := q.UpdateRecordData(ctx, db.UpdateRecordDataParams{Data: string(b), Now: store.Time(now), ID: rec.ID, ProjectID: rec.ProjectID, Collection: rec.Collection, Key: rec.Key})
	if err != nil {
		return err
	}
	if n == 0 {
		return registry.NotFound("deck: record %d no longer exists", rec.ID)
	}
	return nil
}

type View struct {
	ID int64 `json:"id"`
	// Project is the project of a record read by its id.
	Project    string          `json:"project,omitempty"`
	Collection string          `json:"collection"`
	Key        string          `json:"key"`
	Title      string          `json:"title,omitempty"`
	Data       json.RawMessage `json:"data"`
	UpdatedAt  string          `json:"updated_at"`
}

// Name is the title of the record, or its key when its collection names none.
func (v View) Name() string { return cmp.Or(v.Title, v.Key) }

func ViewOf(r db.Record, c *manifest.Collection) View {
	v := View{ID: r.ID, Collection: r.Collection, Key: r.Key, Data: json.RawMessage(r.Data), UpdatedAt: r.UpdatedAt}
	if c != nil {
		if data, err := manifest.DecodeObject([]byte(r.Data)); err == nil {
			v.Title = c.RecordTitle(data)
		}
	}
	return v
}

// Service keeps the records of projects.
type Service struct {
	St  *store.Store
	Now func() time.Time
	// DrainFiles removes the files and folders whose rows are gone.
	DrainFiles func(ctx context.Context)
	// Stop ends an open process of a deleted record as stopped by deck; one the owner stops drops its frozen calls.
	Stop func(ctx context.Context, processID int64, reason string, owner bool) error
}

type recordsListIn struct {
	Project    string   `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
	Collection string   `desc:"Collection name"`
	Limit      int      `opt:"" desc:"At most this many, the newest; every record when omitted"`
	Where      string   `type:"json" opt:"" desc:"Only the records this expression over their fields is true for, as the where of a source of a screen, such as status == 'new' && score > 2; a JSON object of field to value means each field equals its value"`
	Fields     []string `opt:"" desc:"Only these fields of the data of each record"`
}

func (s *Service) recordsList(ctx context.Context, act registry.Actor, in recordsListIn) ([]View, error) {
	p, err := access.ReadProject(ctx, s.St.Q, act, in.Project, in.Collection, "")
	if err != nil {
		return nil, err
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return nil, err
	}
	c := m.Collection(in.Collection)
	if c == nil {
		return nil, registry.Invalid("deck: unknown collection %q", in.Collection)
	}
	recs, err := s.St.Q.ListRecords(ctx, db.ListRecordsParams{ProjectID: p.ID, Collection: in.Collection})
	if err != nil {
		return nil, err
	}
	if where := strings.TrimSpace(in.Where); where != "" {
		if strings.HasPrefix(where, "{") {
			if where, err = equality(where); err != nil {
				return nil, registry.Invalid("deck: where: %v", err)
			}
		}
		keep, err := manifest.Filter(where)
		if err != nil {
			return nil, registry.Invalid("deck: where: %v", err)
		}
		kept := recs[:0]
		for _, r := range recs {
			env, err := manifest.DecodeObject([]byte(r.Data))
			if err != nil {
				continue
			}
			env["_id"], env["_updated"] = r.ID, r.UpdatedAt
			ok, err := keep(env)
			if err != nil {
				return nil, registry.Invalid("deck: where: record %d: %v", r.ID, err)
			}
			if ok {
				kept = append(kept, r)
			}
		}
		recs = kept
	}
	if in.Limit > 0 && len(recs) > in.Limit {
		recs = recs[len(recs)-in.Limit:]
	}
	out := make([]View, 0, len(recs))
	for _, r := range recs {
		v := ViewOf(r, c)
		if len(in.Fields) > 0 {
			v.Data = only(v.Data, in.Fields)
		}
		out = append(out, v)
	}
	return out, nil
}

// equality is the expression of a JSON object of field to value: each field equals its value.
func equality(where string) (string, error) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(where), &fields); err != nil {
		return "", fmt.Errorf("a JSON object of field to value: %v", err)
	}
	names := slices.Sorted(maps.Keys(fields))
	parts := make([]string, 0, len(names))
	for _, n := range names {
		v := strings.TrimSpace(string(fields[n]))
		field := "$env[" + strconv.Quote(n) + "]"
		switch {
		case v == "null":
			parts = append(parts, field+" == nil")
		case strings.HasPrefix(v, "{") || strings.HasPrefix(v, "["):
			parts = append(parts, "toJSON("+field+") == toJSON(fromJSON("+strconv.Quote(v)+"))")
		default:
			parts = append(parts, field+" == fromJSON("+strconv.Quote(v)+")")
		}
	}
	if len(parts) == 0 {
		return "true", nil
	}
	return strings.Join(parts, " && "), nil
}

// only keeps the named fields of a JSON object.
func only(data json.RawMessage, fields []string) json.RawMessage {
	var d map[string]json.RawMessage
	if json.Unmarshal(data, &d) != nil {
		return data
	}
	kept := map[string]json.RawMessage{}
	for _, f := range fields {
		if v, ok := d[f]; ok {
			kept[f] = v
		}
	}
	return manifest.JSON(kept, "")
}

// writeRecords runs a record write in one transaction with the manifest it validates against, so a manifest
// save cannot change the schema between the check and the write.
func (s *Service) writeRecords(ctx context.Context, p db.Project, f func(ctx context.Context, q *db.Queries, m *manifest.Manifest) error) error {
	return s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		m, err := docs.Manifest(ctx, q, p)
		if err != nil {
			return err
		}
		return f(ctx, q, m)
	})
}

type recordsUpsertIn struct {
	Project    string `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
	Collection string `desc:"Collection name"`
	Records    string `type:"text" desc:"Records as JSONL: one JSON object per line"`
}

func (s *Service) recordsUpsert(ctx context.Context, act registry.Actor, in recordsUpsertIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	var out UpsertResult
	err = s.writeRecords(ctx, p, func(ctx context.Context, q *db.Queries, m *manifest.Manifest) error {
		out, err = UpsertJSONL(ctx, q, s.Now(), p.ID, act.ProcessID, m, in.Collection, in.Records)
		return err
	})
	return out, err
}

type recordUpdateIn struct {
	Record int64  `type:"record" desc:"Record id"`
	Data   string `type:"json" desc:"New record JSON"`
}

func (s *Service) recordUpdate(ctx context.Context, act registry.Actor, in recordUpdateIn) (any, error) {
	data, err := manifest.DecodeObject([]byte(in.Data))
	if err != nil {
		return nil, registry.Invalid("deck: data is not a JSON object: %v", err)
	}
	rec, err := s.St.Q.GetRecord(ctx, in.Record)
	rec, p, err := access.Owned(ctx, s.St.Q, act, rec, err, rec.ProjectID, registry.NotFound("deck: no record %d", in.Record))
	if err != nil {
		return nil, err
	}
	err = s.writeRecords(ctx, p, func(ctx context.Context, q *db.Queries, m *manifest.Manifest) error {
		cur, err := q.GetRecord(ctx, rec.ID)
		if errors.Is(err, sql.ErrNoRows) || err == nil && cur.ProjectID != p.ID {
			return registry.NotFound("deck: record %d no longer exists", rec.ID)
		}
		if err != nil {
			return err
		}
		return Update(ctx, q, s.Now(), cur, m, data)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

type collectionDeleteIn struct {
	Project    string `type:"project" desc:"Project name"`
	Collection string `desc:"Collection name"`
}

func (s *Service) collectionDelete(ctx context.Context, act registry.Actor, in collectionDeleteIn) (any, error) {
	p, err := access.Project(ctx, s.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	var pl plan
	err = s.writeRecords(ctx, p, func(ctx context.Context, q *db.Queries, m *manifest.Manifest) error {
		recs, err := q.ListRecords(ctx, db.ListRecordsParams{ProjectID: p.ID, Collection: in.Collection})
		if err != nil {
			return err
		}
		if pl, err = s.plan(ctx, q, p, m, recs...); err != nil {
			return err
		}
		return remove(ctx, q, p, pl)
	})
	if err != nil {
		return nil, err
	}
	s.settle(ctx, pl.open, 0, act.Kind == registry.Owner)
	return map[string]any{"deleted": len(pl.records)}, nil
}

// Expire deletes the records past their retention with what belongs to them; a
// project whose records deck cannot plan is red in its health until it can. One whose manifest does not read is skipped.
func (s *Service) Expire(ctx context.Context, now time.Time) {
	projects, err := s.St.Q.ListProjects(ctx)
	if err != nil {
		slog.Error("expire records", "err", err)
		return
	}
	days := func(n int) string { return store.Time(now.Add(-time.Duration(n) * 24 * time.Hour)) }
	var open []db.Process
	for _, p := range projects {
		if _, err := docs.Manifest(ctx, s.St.Q, p); err != nil {
			continue
		}
		err = s.writeRecords(ctx, p, func(ctx context.Context, q *db.Queries, m *manifest.Manifest) error {
			var expired []db.Record
			for _, c := range m.Collections {
				if c.RetentionDays > 0 {
					recs, err := q.ListExpiredRecords(ctx, db.ListExpiredRecordsParams{ProjectID: p.ID, Collection: c.Name, Before: days(c.RetentionDays)})
					if err != nil {
						return err
					}
					expired = append(expired, recs...)
				}
			}
			if len(expired) == 0 {
				return q.DeleteHealth(ctx, db.DeleteHealthParams{ProjectID: store.ID(p.ID), Subject: retentionSubject(p.Name)})
			}
			pl, err := s.plan(ctx, q, p, m, expired...)
			if err != nil {
				return err
			}
			open = append(open, pl.open...)
			if err := remove(ctx, q, p, pl); err != nil {
				return err
			}
			return q.DeleteHealth(ctx, db.DeleteHealthParams{ProjectID: store.ID(p.ID), Subject: retentionSubject(p.Name)})
		})
		if err != nil {
			health.Write(ctx, s.St, health.Project(p), retentionSubject(p.Name), health.Error, fmt.Sprintf("deck could not delete the records of %s past their retention: %v", p.Name, err))
		}
	}
	s.settle(ctx, open, 0, false)
}

// retentionSubject is the health of the retention of a project.
func retentionSubject(project string) string { return "retention:" + project }

// Held reports whether a record of a project gives a folder, or one inside or around it, as the outbox removes it;
// a folder deck cannot check is held.
func (s *Service) Held(ctx context.Context, projectID int64, folder string) bool {
	p, err := s.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return !errors.Is(err, sql.ErrNoRows)
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return true
	}
	left, err := unheld(ctx, s.St.Q, p, m, nil, []string{folder})
	return err != nil || len(left) == 0
}

// recordOf finds a record of the actor's workspace; the caller checks whether the actor may read or change it.
func (s *Service) recordOf(ctx context.Context, act registry.Actor, id int64) (db.Record, db.Project, error) {
	rec, err := s.St.Q.GetRecord(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return rec, db.Project{}, registry.NotFound("deck: no record %d", id)
	}
	if err != nil {
		return rec, db.Project{}, err
	}
	p, err := s.St.Q.GetProject(ctx, rec.ProjectID)
	if err == nil && p.WorkspaceID != act.Workspace {
		err = registry.NotFound("deck: no record %d", id)
	}
	return rec, p, err
}

type recordGetIn struct {
	Record int64 `type:"record" desc:"Record id"`
}

func (s *Service) recordGet(ctx context.Context, act registry.Actor, in recordGetIn) (View, error) {
	rec, p, err := s.recordOf(ctx, act, in.Record)
	if err != nil {
		return View{}, err
	}
	if err := access.MayRead(ctx, s.St.Q, act, p, rec.Collection, ""); err != nil {
		return View{}, registry.NotFound("deck: no record %d", in.Record)
	}
	m, err := docs.Manifest(ctx, s.St.Q, p)
	if err != nil {
		return View{}, err
	}
	v := ViewOf(rec, m.Collection(rec.Collection))
	v.Project = p.Name
	return v, nil
}

type recordDeleteIn struct {
	Record int64 `type:"record" desc:"Record id"`
}

// recordDelete deletes a record with what belongs to it for good, the owner seeing record_parts first; outside a
// draft only the owner does, by a button or by hand, and the master what its own processes wrote.
func (s *Service) recordDelete(ctx context.Context, act registry.Actor, in recordDeleteIn) (any, error) {
	_, p, err := s.recordOf(ctx, act, in.Record)
	var out any
	if err == nil {
		out, err = s.deleteRecord(ctx, act, p, in.Record)
	}
	if _, replay := gateway.Approved(ctx); replay && registry.Status(err) == http.StatusNotFound {
		return map[string]any{"ok": true, "gone": "the record was deleted before the owner's yes"}, nil
	}
	return out, err
}

// Commands are the commands of records.
func (s *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Read("records_list", "List records",
			"Lists records of a collection, newest last.",
			registry.Everyone, s.recordsList).On("collection"),
		registry.Def("records_upsert", "Write records",
			"Writes records into a collection; records with an existing key are updated.",
			registry.Everyone, s.recordsUpsert),
		registry.Def("record_update", "Edit record",
			"Replaces the data of one record; key fields cannot change.",
			registry.Everyone, s.recordUpdate),
		registry.Read("record_get", "Get record",
			"Returns one record by its id.",
			registry.Everyone, s.recordGet).On("collection"),
		registry.Def("record_delete", "Delete record",
			"Deletes a record with what belongs to it, as record_parts lists, for good, and stops its open processes; outside a draft only the owner may, and the master the records its own processes wrote: what else the master did not make waits for the owner's yes.",
			registry.OwnerSide, s.recordDelete),
		registry.Read("record_parts", "What a record takes along",
			"Lists what deleting a record takes with it: the record, the records of collections whose parent it is, their files, the files of the processes whose input field record names them, their folders of the data folder no other record holds, and the processes it stops.",
			registry.OwnerSide, s.recordParts).On("collection", "files", "processes"),
		registry.Def("collection_delete", "Delete collection records",
			"Deletes every record of a collection with what belongs to each, as record_parts lists, so the collection can be removed from the manifest.",
			registry.OwnerOnly, s.collectionDelete).As(registry.External),
	}
}
