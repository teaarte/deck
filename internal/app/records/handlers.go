package records

import (
	"context"
	"database/sql"
	"deck2/internal/app/access"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

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
	b, _ := json.Marshal(kept)
	return b
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
		out, err = UpsertJSONL(ctx, q, s.Now(), p.ID, m, in.Collection, in.Records)
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
	var n int64
	err = s.writeRecords(ctx, p, func(ctx context.Context, q *db.Queries, _ *manifest.Manifest) error {
		if err := q.DeleteRecordDocuments(ctx, db.DeleteRecordDocumentsParams{ProjectID: p.ID, Collection: in.Collection}); err != nil {
			return err
		}
		n, err = q.DeleteCollection(ctx, db.DeleteCollectionParams{ProjectID: p.ID, Collection: in.Collection})
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"deleted": n}, nil
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

// recordDelete deletes a record with its documents and files for good; outside a draft only the owner does, by a
// button or by hand.
func (s *Service) recordDelete(ctx context.Context, act registry.Actor, in recordDeleteIn) (any, error) {
	rec, p, err := s.recordOf(ctx, act, in.Record)
	if err != nil {
		return nil, err
	}
	if act.Kind != registry.Owner && p.Status != store.ProjectDraft {
		return nil, registry.Refuse("deck: only the owner deletes records of an enabled project")
	}
	err = s.writeRecords(ctx, p, func(ctx context.Context, q *db.Queries, _ *manifest.Manifest) error {
		if err := q.DeleteOneRecordDocuments(ctx, store.ID(rec.ID)); err != nil {
			return err
		}
		if err := q.DeleteFilesOf(ctx, db.DeleteFilesOfParams{OwnerKind: "record", OwnerID: rec.ID}); err != nil {
			return err
		}
		return q.DeleteRecord(ctx, rec.ID)
	})
	if err != nil {
		return nil, err
	}
	s.DrainFiles(ctx)
	return map[string]any{"ok": true}, nil
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
			"Deletes a record with its documents and files for good; outside a draft only the owner may.",
			registry.OwnerSide, s.recordDelete),
		registry.Def("collection_delete", "Delete collection records",
			"Deletes every record of a collection, so the collection can be removed from the manifest.",
			registry.OwnerOnly, s.collectionDelete).As(registry.External),
	}
}
