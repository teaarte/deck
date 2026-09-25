package app

import (
	"context"
	"database/sql"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"errors"
)

type documentView struct {
	ID        int64  `json:"id"`
	Project   int64  `json:"project,omitempty"`
	OwnerKind string `json:"owner_kind,omitempty"`
	OwnerID   int64  `json:"owner_id,omitempty"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Revision  int64  `json:"revision"`
	Text      string `json:"text,omitempty"`
}

func docView(d db.Document) documentView {
	return documentView{ID: d.ID, Project: d.ProjectID, OwnerKind: d.OwnerKind, OwnerID: d.OwnerID, Kind: d.Kind, Name: d.Name, Revision: d.Current}
}

func (a *App) documentsList(ctx context.Context, act registry.Actor, in struct{ Project, Kind string }) (any, error) {
	s, err := a.docScope(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	ds, err := a.Store.Q.ListDocuments(ctx, db.ListDocumentsParams{WorkspaceID: s.Workspace, ProjectID: s.Project, Kind: in.Kind})
	if err != nil {
		return nil, err
	}
	out := make([]documentView, 0, len(ds))
	for _, d := range ds {
		out = append(out, docView(d))
	}
	return out, nil
}

// readableDocument loads a document the actor may read: its own project's or its workspace's.
func readableDocument(ctx context.Context, q *db.Queries, act registry.Actor, id int64) (db.Document, error) {
	d, err := getDocument(ctx, q, id)
	if err != nil {
		return d, err
	}
	if d.WorkspaceID != act.Workspace {
		return d, registry.NotFound("deck: no document %d", id)
	}
	if d.ProjectID == 0 {
		return d, nil
	}
	return d, inside(act, d.ProjectID)
}

func (a *App) documentGet(ctx context.Context, act registry.Actor, in struct {
	Document int64
	Project  string
	Name     string
	Revision int64
}) (any, error) {
	var d db.Document
	var err error
	switch {
	case in.Document != 0:
		d, err = readableDocument(ctx, a.Store.Q, act, in.Document)
	case in.Name != "":
		var s Scope
		if s, err = a.docScope(ctx, act, in.Project); err == nil {
			d, err = a.Store.Q.FindDocument(ctx, DocRef{Scope: s, Name: in.Name}.find())
			if errors.Is(err, sql.ErrNoRows) {
				err = registry.NotFound("deck: no document %q", in.Name)
			}
		}
	default:
		err = registry.Invalid("deck: give a document id or a name")
	}
	if err != nil {
		return nil, err
	}
	r, err := getRevision(ctx, a.Store.Q, d, in.Revision)
	if err != nil {
		return nil, err
	}
	v := docView(d)
	v.Revision, v.Text = r.N, r.Text
	return v, nil
}

func (a *App) documentSave(ctx context.Context, act registry.Actor, in struct {
	Name, Kind, Text, Project, Reason string
	Record, Card                      int64
}) (any, error) {
	if in.Record != 0 && in.Card != 0 {
		return nil, registry.Invalid("deck: a document belongs to a record or a card, not both")
	}
	if in.Kind == KindManifest {
		return nil, registry.Invalid("deck: save manifests with deck.manifest_save")
	}
	if act.Kind == registry.Agent && in.Kind != KindArtifact {
		return nil, registry.Refuse("deck: agents write only artifacts")
	}
	s, err := a.docScope(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	var d db.Document
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		ref := DocRef{Scope: s, Name: in.Name}
		if in.Record != 0 {
			rec, err := q.GetRecord(ctx, in.Record)
			if errors.Is(err, sql.ErrNoRows) || err == nil && (s.Project == 0 || rec.ProjectID != s.Project) {
				return registry.NotFound("deck: no record %d in this project", in.Record)
			}
			if err != nil {
				return err
			}
			ref.OwnerKind, ref.OwnerID = "record", rec.ID
		}
		if in.Card != 0 {
			c, err := q.GetCard(ctx, in.Card)
			if errors.Is(err, sql.ErrNoRows) || err == nil && (s.Project == 0 || c.ProjectID != s.Project) {
				return registry.NotFound("deck: no card %d in this project", in.Card)
			}
			if err != nil {
				return err
			}
			ref.OwnerKind, ref.OwnerID = "card", c.ID
		}
		d, _, err = SaveDocument(ctx, q, ref, in.Kind, in.Text, act.Author(), in.Reason)
		return err
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("documents")
	return docView(d), nil
}

func (a *App) documentRevisions(ctx context.Context, act registry.Actor, in struct{ Document int64 }) (any, error) {
	d, err := readableDocument(ctx, a.Store.Q, act, in.Document)
	if err != nil {
		return nil, err
	}
	rs, err := a.Store.Q.ListRevisions(ctx, d.ID)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(rs))
	for _, r := range rs {
		out = append(out, map[string]any{"revision": r.N, "author": r.Author, "reason": r.Reason, "created_at": r.CreatedAt, "current": r.N == d.Current})
	}
	return out, nil
}

func (a *App) documentRevert(ctx context.Context, act registry.Actor, in struct {
	Document, Revision int64
	Reason             string
}) (any, error) {
	d, err := readableDocument(ctx, a.Store.Q, act, in.Document)
	if err != nil {
		return nil, err
	}
	r, err := getRevision(ctx, a.Store.Q, d, in.Revision)
	if err != nil {
		return nil, err
	}
	reason := revertReason(r.N, in.Reason)
	if d.Kind == KindManifest {
		p, err := a.Store.Q.GetProject(ctx, d.ProjectID)
		if err != nil {
			return nil, err
		}
		n, err := a.SaveManifest(ctx, act, p, []byte(r.Text), reason)
		if err != nil {
			return nil, err
		}
		return map[string]any{"revision": n}, nil
	}
	var n int64
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		ref := DocRef{Scope: Scope{Workspace: d.WorkspaceID, Project: d.ProjectID}, OwnerKind: d.OwnerKind, OwnerID: d.OwnerID, Name: d.Name}
		_, n, err = SaveDocument(ctx, q, ref, d.Kind, r.Text, act.Author(), reason)
		return err
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("documents")
	return map[string]any{"revision": n}, nil
}
