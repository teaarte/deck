package records

import (
	"cmp"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/fault"
	"deck2/internal/gateway"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

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
		return nil, registry.Invalid("unknown collection %q", in.Collection)
	}
	recs, err := s.St.Q.ListRecords(ctx, db.ListRecordsParams{ProjectID: p.ID, Collection: in.Collection})
	if err != nil {
		return nil, err
	}
	if where := strings.TrimSpace(in.Where); where != "" {
		if strings.HasPrefix(where, "{") {
			if where, err = equality(where); err != nil {
				return nil, registry.Invalid("where: %v", err)
			}
		}
		keep, err := manifest.Filter(where)
		if err != nil {
			return nil, registry.Invalid("where: %v", err)
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
				return nil, registry.Invalid("where: record %d: %v", r.ID, err)
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

// recordOf finds a record of the actor's workspace; the caller checks whether the actor may read or change it.
func (s *Service) recordOf(ctx context.Context, act registry.Actor, id int64) (db.Record, db.Project, error) {
	rec, err := s.St.Q.GetRecord(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return rec, db.Project{}, registry.NotFound("no record %d", id)
	}
	if err != nil {
		return rec, db.Project{}, err
	}
	p, err := s.St.Q.GetProject(ctx, rec.ProjectID)
	if err == nil && p.WorkspaceID != act.Workspace {
		err = registry.NotFound("no record %d", id)
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
		return View{}, registry.NotFound("no record %d", in.Record)
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

// recordDelete deletes a record with what belongs to it for good, the owner seeing record_parts_list first; outside a
// draft only the owner does, by a button or by hand, and the master what its own processes wrote.
func (s *Service) recordDelete(ctx context.Context, act registry.Actor, in recordDeleteIn) (any, error) {
	rec, err := s.St.Q.GetRecord(ctx, in.Record)
	rec, p, err := access.Owned(ctx, s.St.Q, act, rec, err, rec.ProjectID, registry.NotFound("no record %d", in.Record))
	var out any
	if err == nil {
		out, err = s.deleteRecord(ctx, act, p, in.Record)
	}
	if _, replay := gateway.Approved(ctx); replay && fault.Status(err) == http.StatusNotFound {
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
			"Deletes a record with what belongs to it, as record_parts_list lists, for good, and stops its open processes; outside a draft the owner, an agent or script of the record's own project, and the master the records its own processes wrote: what else the master did not make waits for the owner's yes. The owner may undo a deletion that was not theirs.",
			registry.Everyone, s.recordDelete),
		registry.Read("record_parts_list", "What a record takes along",
			"Lists what deleting a record takes with it: the record, the records of collections whose parent it is, their files, the files of the processes whose input field record names them, their folders of the data folder no other record holds, and the processes it stops.",
			registry.OwnerSide, s.recordParts).On("collection", "files", "processes"),
		registry.Def("collection_delete", "Delete collection records",
			"Deletes every record of a collection with what belongs to each, as record_parts_list lists, so the collection can be removed from the manifest.",
			registry.OwnerSide, s.collectionDelete).As(registry.External),
	}
}
