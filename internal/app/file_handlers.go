package app

import (
	"context"
	"database/sql"
	"errors"

	"deck2/internal/registry"
	"deck2/internal/store/db"
)

type fileView struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Size      int64  `json:"size"`
	OwnerKind string `json:"owner_kind"`
	OwnerID   int64  `json:"owner_id"`
	CreatedAt string `json:"created_at"`
}

// filesList lists the files of a record, card or process the actor can see.
func (a *App) filesList(ctx context.Context, act registry.Actor, in struct{ Record, Card, Process int64 }) (any, error) {
	var kind string
	var id, projectID int64
	var err error
	switch {
	case in.Record != 0:
		kind, id = "record", in.Record
		var r db.Record
		if r, err = a.Store.Q.GetRecord(ctx, id); err == nil {
			projectID = r.ProjectID
		}
	case in.Card != 0:
		kind, id = "card", in.Card
		var c db.Card
		if c, err = a.Store.Q.GetCard(ctx, id); err == nil {
			projectID = c.ProjectID
		}
	case in.Process != 0:
		kind, id = "process", in.Process
		var p db.Process
		if p, err = a.Store.Q.GetProcess(ctx, id); err == nil {
			projectID = p.ProjectID
		}
	default:
		return nil, registry.Invalid("deck: give a record, a card or a process")
	}
	if errors.Is(err, sql.ErrNoRows) {
		return nil, registry.NotFound("deck: no %s %d", kind, id)
	}
	if err != nil {
		return nil, err
	}
	if p, err := a.Store.Q.GetProject(ctx, projectID); err != nil || p.WorkspaceID != act.Workspace {
		return nil, registry.NotFound("deck: no %s %d", kind, id)
	}
	if err := inside(act, projectID); err != nil {
		return nil, err
	}
	fs, err := a.Store.Q.ListFilesOf(ctx, db.ListFilesOfParams{ProjectID: projectID, OwnerKind: kind, OwnerID: id})
	if err != nil {
		return nil, err
	}
	out := make([]fileView, 0, len(fs))
	for _, f := range fs {
		out = append(out, fileView{ID: f.ID, Name: f.Name, Type: f.Type, Size: f.Size, OwnerKind: f.OwnerKind, OwnerID: f.OwnerID, CreatedAt: f.CreatedAt})
	}
	return out, nil
}
