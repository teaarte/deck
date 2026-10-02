package app

import (
	"context"
	"database/sql"
	"errors"

	"deck2/internal/app/records"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

type recordsListIn struct {
	Project    string `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
	Collection string `desc:"Collection name"`
	Limit      int    `opt:"" desc:"At most this many"`
}

func (a *App) recordsList(ctx context.Context, act registry.Actor, in recordsListIn) (any, error) {
	p, err := a.readProject(ctx, act, in.Project, in.Collection, "")
	if err != nil {
		return nil, err
	}
	m, err := docs.Manifest(ctx, a.Store.Q, p)
	if err != nil {
		return nil, err
	}
	c := m.Collection(in.Collection)
	if c == nil {
		return nil, registry.Invalid("deck: unknown collection %q", in.Collection)
	}
	recs, err := a.Store.Q.ListRecords(ctx, db.ListRecordsParams{ProjectID: p.ID, Collection: in.Collection})
	if err != nil {
		return nil, err
	}
	if in.Limit > 0 && len(recs) > in.Limit {
		recs = recs[len(recs)-in.Limit:]
	}
	out := make([]records.View, 0, len(recs))
	for _, r := range recs {
		out = append(out, records.ViewOf(r, c))
	}
	return out, nil
}

// writeRecords runs a record write in one transaction with the manifest it validates against, so a manifest
// save cannot change the schema between the check and the write.
func (a *App) writeRecords(ctx context.Context, p db.Project, f func(ctx context.Context, q *db.Queries, m *manifest.Manifest) error) error {
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		m, err := docs.Manifest(ctx, q, p)
		if err != nil {
			return err
		}
		return f(ctx, q, m)
	})
	if err == nil {
		a.Bus.Publish("collection")
	}
	return err
}

type recordsUpsertIn struct {
	Project    string `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
	Collection string `desc:"Collection name"`
	Records    string `type:"text" desc:"Records as JSONL: one JSON object per line"`
}

func (a *App) recordsUpsert(ctx context.Context, act registry.Actor, in recordsUpsertIn) (any, error) {
	p, err := a.project(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	var out records.UpsertResult
	err = a.writeRecords(ctx, p, func(ctx context.Context, q *db.Queries, m *manifest.Manifest) error {
		out, err = records.UpsertJSONL(ctx, q, a.Cfg.Now(), p.ID, m, in.Collection, in.Records)
		return err
	})
	return out, err
}

type recordUpdateIn struct {
	Record int64  `type:"record" desc:"Record id"`
	Data   string `type:"text" desc:"New record JSON"`
}

func (a *App) recordUpdate(ctx context.Context, act registry.Actor, in recordUpdateIn) (any, error) {
	data, err := manifest.DecodeObject([]byte(in.Data))
	if err != nil {
		return nil, registry.Invalid("deck: data is not a JSON object: %v", err)
	}
	rec, err := a.Store.Q.GetRecord(ctx, in.Record)
	rec, p, err := owned(ctx, a.Store.Q, act, rec, err, rec.ProjectID, registry.NotFound("deck: no record %d", in.Record))
	if err != nil {
		return nil, err
	}
	err = a.writeRecords(ctx, p, func(ctx context.Context, q *db.Queries, m *manifest.Manifest) error {
		cur, err := q.GetRecord(ctx, rec.ID)
		if errors.Is(err, sql.ErrNoRows) || err == nil && cur.ProjectID != p.ID {
			return registry.NotFound("deck: record %d no longer exists", rec.ID)
		}
		if err != nil {
			return err
		}
		return records.Update(ctx, q, a.Cfg.Now(), cur, m, data)
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

func (a *App) collectionDelete(ctx context.Context, act registry.Actor, in collectionDeleteIn) (any, error) {
	p, err := a.project(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	var n int64
	err = a.writeRecords(ctx, p, func(ctx context.Context, q *db.Queries, _ *manifest.Manifest) error {
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
func (a *App) recordOf(ctx context.Context, act registry.Actor, id int64) (db.Record, db.Project, error) {
	rec, err := a.Store.Q.GetRecord(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return rec, db.Project{}, registry.NotFound("deck: no record %d", id)
	}
	if err != nil {
		return rec, db.Project{}, err
	}
	p, err := a.Store.Q.GetProject(ctx, rec.ProjectID)
	if err == nil && p.WorkspaceID != act.Workspace {
		err = registry.NotFound("deck: no record %d", id)
	}
	return rec, p, err
}

type recordGetIn struct {
	Record int64 `type:"record" desc:"Record id"`
}

func (a *App) recordGet(ctx context.Context, act registry.Actor, in recordGetIn) (any, error) {
	rec, p, err := a.recordOf(ctx, act, in.Record)
	if err != nil {
		return nil, err
	}
	if err := a.mayRead(ctx, a.Store.Q, act, p, rec.Collection, ""); err != nil {
		return nil, registry.NotFound("deck: no record %d", in.Record)
	}
	m, err := docs.Manifest(ctx, a.Store.Q, p)
	if err != nil {
		return nil, err
	}
	return records.ViewOf(rec, m.Collection(rec.Collection)), nil
}

type recordDeleteIn struct {
	Record int64 `type:"record" desc:"Record id"`
}

// recordDelete deletes a record with its documents and files for good; the master may do it only in a draft,
// where trial runs leave their records.
func (a *App) recordDelete(ctx context.Context, act registry.Actor, in recordDeleteIn) (any, error) {
	rec, p, err := a.recordOf(ctx, act, in.Record)
	if err != nil {
		return nil, err
	}
	if act.Kind != registry.Owner && p.Status != store.ProjectDraft {
		return nil, registry.Refuse("deck: only the owner deletes records of an enabled project")
	}
	var gone []int64
	err = a.writeRecords(ctx, p, func(ctx context.Context, q *db.Queries, _ *manifest.Manifest) error {
		if err := q.DeleteOneRecordDocuments(ctx, rec.ID); err != nil {
			return err
		}
		var err error
		if gone, err = q.DeleteRecordFiles(ctx, rec.ID); err != nil {
			return err
		}
		return q.DeleteRecord(ctx, rec.ID)
	})
	if err != nil {
		return nil, err
	}
	a.removeFiles(gone)
	return map[string]any{"ok": true}, nil
}

func (a *App) recordCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("records_list", "List records",
			"Lists records of a collection, newest last.",
			registry.Everyone, a.recordsList),
		registry.Def("records_upsert", "Write records",
			"Writes records into a collection; records with an existing key are updated.",
			registry.Everyone, a.recordsUpsert),
		registry.Def("record_update", "Edit record",
			"Replaces the data of one record; key fields cannot change.",
			registry.Everyone, a.recordUpdate),
		registry.Def("record_get", "Get record",
			"Returns one record by its id.",
			registry.Everyone, a.recordGet),
		registry.Def("record_delete", "Delete record",
			"Deletes a record with its documents and files for good; outside a draft only the owner may.",
			registry.OwnerSide, a.recordDelete),
		registry.Def("collection_delete", "Delete collection records",
			"Deletes every record of a collection, so the collection can be removed from the manifest.",
			registry.OwnerOnly, a.collectionDelete).As(registry.External),
	}
}
