package screens

import (
	"context"

	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Sources read what the blocks of screens show besides the commands they read: chats, forms and documents.
type Sources struct {
	Store    *store.Store
	Registry *registry.Registry
}

func (s Sources) Records(ctx context.Context, project int64, collection string) ([]db.Record, error) {
	return s.Store.Q.ListRecords(ctx, db.ListRecordsParams{ProjectID: project, Collection: collection})
}

func (s Sources) Record(ctx context.Context, id int64) (db.Record, error) {
	return s.Store.Q.GetRecord(ctx, id)
}

func (s Sources) ProjectByName(ctx context.Context, workspace int64, name string) (db.Project, error) {
	return s.Store.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: workspace, Name: name})
}
