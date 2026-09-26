package app

import (
	"context"
	"database/sql"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"errors"
)

func (a *App) recordsList(ctx context.Context, act registry.Actor, in struct {
	Project, Collection string
	Limit               int
}) (any, error) {
	p, err := a.readScope(ctx, act, in.Project, in.Collection, "")
	if err != nil {
		return nil, err
	}
	m, err := loadManifest(ctx, a.Store.Q, p)
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
	out := make([]RecordView, 0, len(recs))
	for _, r := range recs {
		out = append(out, recordView(r, c))
	}
	return out, nil
}

// writeRecords runs a record write in one transaction with the manifest it validates against, so a manifest
// save cannot change the schema between the check and the write.
func (a *App) writeRecords(ctx context.Context, p db.Project, f func(ctx context.Context, q *db.Queries, m *manifest.Manifest) error) error {
	err := a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		m, err := loadManifest(ctx, q, p)
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
	var out UpsertResult
	err = a.writeRecords(ctx, p, func(ctx context.Context, q *db.Queries, m *manifest.Manifest) error {
		out, err = UpsertJSONL(ctx, q, p.ID, m, in.Collection, in.Records)
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
	rec, err := a.Store.Q.GetRecord(ctx, in.Record)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, registry.NotFound("deck: no record %d", in.Record)
	}
	if err != nil {
		return nil, err
	}
	p, err := a.Store.Q.GetProject(ctx, rec.ProjectID)
	if err != nil {
		return nil, err
	}
	if p.WorkspaceID != act.Workspace {
		return nil, registry.NotFound("deck: no record %d", in.Record)
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
		return UpdateRecord(ctx, q, cur, m, data)
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
