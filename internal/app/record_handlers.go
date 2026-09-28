package app

import (
	"context"
	"database/sql"
	"errors"

	"deck2/internal/app/docs"
	"deck2/internal/app/records"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

func (a *App) recordsList(ctx context.Context, act registry.Actor, in struct {
	Project, Collection string
	Limit               int
}) (any, error) {
	p, err := a.readScope(ctx, act, in.Project, in.Collection, "")
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

func (a *App) recordsUpsert(ctx context.Context, act registry.Actor, in struct{ Project, Collection, Records string }) (any, error) {
	p, err := a.scope(ctx, act, in.Project)
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

func (a *App) recordUpdate(ctx context.Context, act registry.Actor, in struct {
	Record int64
	Data   string
}) (any, error) {
	data, err := manifest.DecodeObject([]byte(in.Data))
	if err != nil {
		return nil, registry.Invalid("deck: data is not a JSON object: %v", err)
	}
	rec, p, err := a.recordOf(ctx, act, in.Record)
	if err != nil {
		return nil, err
	}
	if err := inside(act, p.ID); err != nil {
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

func (a *App) collectionDelete(ctx context.Context, act registry.Actor, in struct{ Project, Collection string }) (any, error) {
	p, err := a.scope(ctx, act, in.Project)
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

// recordOf finds a record of the actor's workspace.
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

func (a *App) recordGet(ctx context.Context, act registry.Actor, in struct{ Record int64 }) (any, error) {
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

// recordDelete deletes a record with its documents and files for good; the master may do it only in a draft,
// where trial runs leave their records.
func (a *App) recordDelete(ctx context.Context, act registry.Actor, in struct{ Record int64 }) (any, error) {
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
