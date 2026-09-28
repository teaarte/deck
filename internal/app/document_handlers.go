package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"deck2/internal/registry"
	"deck2/internal/store/db"
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
	s, err := a.readDocScope(ctx, act, in.Project, "")
	if err != nil {
		return nil, err
	}
	ds, err := a.Store.Q.ListDocuments(ctx, db.ListDocumentsParams{WorkspaceID: s.Workspace, ProjectID: s.Project, Kind: in.Kind})
	if err != nil {
		return nil, err
	}
	out := make([]documentView, 0, len(ds))
	for _, d := range ds {
		p := db.Project{ID: d.ProjectID, Name: in.Project}
		if d.ProjectID != 0 && a.mayRead(ctx, a.Store.Q, act, p, "", d.Name) != nil {
			continue
		}
		out = append(out, docView(d))
	}
	return out, nil
}

// readDocScope is where a document read works; another project only as far as reads allow.
func (a *App) readDocScope(ctx context.Context, act registry.Actor, project, name string) (Scope, error) {
	if project == "" {
		return a.docScope(ctx, act, project)
	}
	p, err := a.readScope(ctx, act, project, "", name)
	return projectScope(p), err
}

// readableDocument loads a document the actor may read: its own project's, its workspace's, or one that reads
// in its manifest names.
func (a *App) readableDocument(ctx context.Context, q *db.Queries, act registry.Actor, id int64) (db.Document, error) {
	d, err := getDocument(ctx, q, id)
	if err != nil {
		return d, err
	}
	if d.WorkspaceID != act.Workspace {
		return d, registry.NotFound("deck: no document %d", id)
	}
	if d.ProjectID == 0 || inside(act, d.ProjectID) == nil {
		return d, nil
	}
	p, err := q.GetProject(ctx, d.ProjectID)
	if err != nil {
		return d, err
	}
	return d, a.mayRead(ctx, q, act, p, "", d.Name)
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
		d, err = a.readableDocument(ctx, a.Store.Q, act, in.Document)
	case in.Name != "":
		var s Scope
		if s, err = a.readDocScope(ctx, act, in.Project, in.Name); err == nil {
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
		var n int64
		if d, n, err = SaveDocument(ctx, q, ref, in.Kind, in.Text, act.Author(), in.Reason); err != nil {
			return err
		}
		return a.scriptChanged(ctx, q, act, d, n, in.Reason)
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("documents")
	return docView(d), nil
}

func (a *App) documentRevisions(ctx context.Context, act registry.Actor, in struct{ Document int64 }) (any, error) {
	d, err := a.readableDocument(ctx, a.Store.Q, act, in.Document)
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
	IfRevision         int64 `json:"if_revision"`
}) (any, error) {
	d, err := a.readableDocument(ctx, a.Store.Q, act, in.Document)
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
		return a.SaveManifest(ctx, act, p, []byte(r.Text), reason, r.N, in.IfRevision,
			Call{Command: registry.Builtin + ".document_revert", Input: map[string]any{"document": d.ID, "revision": r.N, "reason": in.Reason}})
	}
	var n int64
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		ref := DocRef{Scope: Scope{Workspace: d.WorkspaceID, Project: d.ProjectID}, OwnerKind: d.OwnerKind, OwnerID: d.OwnerID, Name: d.Name}
		if cur, err := q.GetDocument(ctx, d.ID); err != nil || in.IfRevision != 0 && cur.Current != in.IfRevision {
			if err == nil {
				err = registry.Conflict("deck: document %d is at revision %d now, not %d", d.ID, cur.Current, in.IfRevision)
			}
			return err
		}
		if _, n, err = SaveDocument(ctx, q, ref, d.Kind, r.Text, act.Author(), reason); err != nil {
			return err
		}
		return a.scriptChanged(ctx, q, act, d, n, reason)
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("documents")
	return map[string]any{"revision": n}, nil
}

// scriptChanged lists a script change made without the owner, undone by returning to the revision before n.
func (a *App) scriptChanged(ctx context.Context, q *db.Queries, act registry.Actor, d db.Document, n int64, reason string) error {
	if d.Kind != KindScript || d.ProjectID == 0 || d.OwnerKind != "" {
		return nil
	}
	prev := db.Document{ID: d.ID, Current: n - 1}
	return a.unattended(ctx, q, act, d.ProjectID, prev, n, fmt.Sprintf("%s changed script %s", act.Author(), d.Name), reason)
}
