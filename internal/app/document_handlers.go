package app

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"deck2/internal/app/docs"
	"deck2/internal/registry"
	"deck2/internal/store"
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
	Deleted   bool   `json:"deleted,omitempty"`
	// Warning says what a save did that its author should know of, such as an index past its limit.
	Warning string `json:"warning,omitempty"`
}

func docView(d db.Document) documentView {
	return documentView{ID: d.ID, Project: d.ProjectID, OwnerKind: d.OwnerKind, OwnerID: d.OwnerID, Kind: d.Kind, Name: d.Name, Revision: d.Current,
		Deleted: d.DeletedAt.Valid}
}

type documentsListIn struct {
	Project string `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
	Kind    string `opt:"" desc:"Only this kind"`
	Deleted bool   `opt:"" desc:"The deleted documents instead, which the owner can bring back"`
}

func (a *App) documentsList(ctx context.Context, act registry.Actor, in documentsListIn) (any, error) {
	var s docs.Scope
	var err error
	if in.Project == "" && in.Kind == docs.KindMemory {
		s, err = a.memoryScope(ctx, act, "", docs.MemoryIndex)
	} else {
		s, err = a.readDocScope(ctx, act, in.Project, "")
	}
	if err != nil {
		return nil, err
	}
	var ds []db.Document
	if in.Deleted {
		if act.Kind != registry.Owner {
			return nil, registry.Refuse("deck: only the owner lists deleted documents")
		}
		ds, err = a.Store.Q.ListDeletedDocuments(ctx, db.ListDeletedDocumentsParams{WorkspaceID: s.Workspace, ProjectID: s.Project})
	} else {
		ds, err = a.Store.Q.ListDocuments(ctx, db.ListDocumentsParams{WorkspaceID: s.Workspace, ProjectID: s.Project, Kind: in.Kind})
	}
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
func (a *App) readDocScope(ctx context.Context, act registry.Actor, project, name string) (docs.Scope, error) {
	if project == "" {
		return a.docScope(ctx, act, project)
	}
	p, err := a.readProject(ctx, act, project, "", name)
	return docs.ProjectScope(p), err
}

// readableDocument loads a document the actor may read: its own project's, its workspace's, or one that reads
// in its manifest names.
func (a *App) readableDocument(ctx context.Context, q *db.Queries, act registry.Actor, id int64) (db.Document, error) {
	d, err := docs.Get(ctx, q, id)
	if err != nil {
		return d, err
	}
	if d.WorkspaceID != act.Workspace {
		return d, registry.NotFound("deck: no document %d", id)
	}
	if d.ProjectID == 0 || act.Within(d.ProjectID) {
		return d, nil
	}
	p, err := q.GetProject(ctx, d.ProjectID)
	if err != nil {
		return d, err
	}
	return d, a.mayRead(ctx, q, act, p, "", d.Name)
}

type documentGetIn struct {
	Document int64  `type:"document" opt:"" desc:"Document id"`
	Project  string `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
	Name     string `opt:"" desc:"Document name (path)"`
	Revision int64  `opt:"" desc:"Revision number"`
}

func (a *App) documentGet(ctx context.Context, act registry.Actor, in documentGetIn) (any, error) {
	var d db.Document
	var err error
	switch {
	case in.Document != 0:
		d, err = a.readableDocument(ctx, a.Store.Q, act, in.Document)
	case in.Name != "":
		var s docs.Scope
		if in.Project == "" {
			s, err = a.memoryScope(ctx, act, "", in.Name)
		} else {
			s, err = a.readDocScope(ctx, act, in.Project, in.Name)
		}
		if err == nil {
			d, err = a.Store.Q.FindDocument(ctx, docs.Ref{Scope: s, Name: in.Name}.Find())
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
	r, err := docs.Revision(ctx, a.Store.Q, d, in.Revision)
	if err != nil {
		return nil, err
	}
	v := docView(d)
	v.Revision, v.Text = r.N, r.Text
	return v, nil
}

type documentSaveIn struct {
	Name    string `desc:"Document name (path), e.g. notes/criteria.md; memory/MEMORY.md and memory/<fact>.md for memory"`
	Kind    string `desc:"Document kind"`
	Text    string `type:"text" desc:"Full new text"`
	Project string `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
	Record  int64  `type:"record" opt:"" desc:"Record the document belongs to"`
	Card    int64  `type:"card" opt:"" desc:"Card the document belongs to"`
	Reason  string `opt:"" desc:"Why it changed"`
}

func (a *App) documentSave(ctx context.Context, act registry.Actor, in documentSaveIn) (any, error) {
	if in.Record != 0 && in.Card != 0 {
		return nil, registry.Invalid("deck: a document belongs to a record or a card, not both")
	}
	if in.Kind == docs.KindManifest {
		return nil, registry.Invalid("deck: save manifests with deck.manifest_save")
	}
	if act.Kind == registry.Agent && in.Kind != docs.KindArtifact && in.Kind != docs.KindMemory {
		return nil, registry.Refuse("deck: agents write only artifacts and the memory of their project")
	}
	s, err := a.memoryScope(ctx, act, in.Project, in.Name)
	if err != nil {
		return nil, err
	}
	var d db.Document
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		ref := docs.Ref{Scope: s, Name: in.Name}
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
		if d, n, err = docs.Save(ctx, q, ref, in.Kind, in.Text, act.Author(), in.Reason); err != nil {
			return err
		}
		return a.scriptChanged(ctx, q, act, d, n, in.Reason)
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("documents")
	v := docView(d)
	if d.Name == docs.MemoryIndex && d.OwnerKind == "" {
		if n := docs.IndexLines(in.Text); n > docs.IndexLimit {
			v.Warning = fmt.Sprintf("the index has %d lines; only the first %d reach prompts: merge or drop lines, the most important first", n, docs.IndexLimit)
		}
	}
	return v, nil
}

// memoryScope is docScope, except that memory of no project is the memory of the owner, in project master.
func (a *App) memoryScope(ctx context.Context, act registry.Actor, project, name string) (docs.Scope, error) {
	s, err := a.docScope(ctx, act, project)
	if err != nil || s.Project != 0 || !strings.HasPrefix(name, docs.MemoryDir) {
		return s, err
	}
	p, err := a.masterProject(ctx, act.Workspace)
	return docs.ProjectScope(p), err
}

type documentDeleteIn struct {
	Document int64  `type:"document" opt:"" desc:"Document id"`
	Project  string `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
	Name     string `opt:"" desc:"Document name (path)"`
}

// documentDelete hides a document with its revisions; agents delete only the memory of their project.
func (a *App) documentDelete(ctx context.Context, act registry.Actor, in documentDeleteIn) (any, error) {
	var d db.Document
	var err error
	switch {
	case in.Document != 0:
		d, err = a.readableDocument(ctx, a.Store.Q, act, in.Document)
	case in.Name != "":
		var s docs.Scope
		if s, err = a.memoryScope(ctx, act, in.Project, in.Name); err == nil {
			d, err = a.Store.Q.FindDocument(ctx, docs.Ref{Scope: s, Name: in.Name}.Find())
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
	switch {
	case d.Kind == docs.KindManifest || d.Kind == docs.KindPrompt && d.OwnerKind == "":
		return nil, registry.Invalid("deck: a project keeps its %s; change it instead", d.Name)
	case act.Kind == registry.Agent && (d.Kind != docs.KindMemory || d.ProjectID != act.ProjectID):
		return nil, registry.Refuse("deck: agents delete only the memory of their project")
	}
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.DeleteDocument(ctx, db.DeleteDocumentParams{DeletedAt: sql.NullString{String: store.Time(a.Cfg.Now()), Valid: true}, ID: d.ID})
		return err
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("documents")
	return map[string]any{"document": d.ID, "deleted": true}, nil
}

type documentRestoreIn struct {
	Document int64 `type:"document" desc:"Document id"`
}

// documentRestore brings a deleted document back as it was.
func (a *App) documentRestore(ctx context.Context, act registry.Actor, in documentRestoreIn) (any, error) {
	d, err := a.Store.Q.GetDocument(ctx, in.Document)
	if errors.Is(err, sql.ErrNoRows) || err == nil && d.WorkspaceID != act.Workspace {
		return nil, registry.NotFound("deck: no document %d", in.Document)
	}
	if err != nil {
		return nil, err
	}
	var n int64
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := q.FindDocument(ctx, docs.Ref{Scope: docs.Scope{Workspace: d.WorkspaceID, Project: d.ProjectID}, OwnerKind: d.OwnerKind,
			OwnerID: d.OwnerID, Name: d.Name}.Find()); err == nil {
			return nil
		}
		var err error
		n, err = q.RestoreDocument(ctx, d.ID)
		return err
	})
	if err != nil {
		return nil, err
	}
	if n == 0 {
		return nil, registry.Conflict("deck: document %d is not deleted", d.ID)
	}
	a.Bus.Publish("documents")
	return map[string]any{"document": d.ID, "restored": true}, nil
}

type documentRevisionsIn struct {
	Document int64 `type:"document" desc:"Document id"`
}

func (a *App) documentRevisions(ctx context.Context, act registry.Actor, in documentRevisionsIn) (any, error) {
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

type documentRevertIn struct {
	Document   int64  `type:"document" desc:"Document id"`
	Revision   int64  `desc:"Revision to return to"`
	Reason     string `opt:"" desc:"Why"`
	IfRevision int64  `json:"if_revision" opt:"" desc:"Only while the current revision is still this one"`
}

func (a *App) documentRevert(ctx context.Context, act registry.Actor, in documentRevertIn) (any, error) {
	d, err := a.readableDocument(ctx, a.Store.Q, act, in.Document)
	if err != nil {
		return nil, err
	}
	r, err := docs.Revision(ctx, a.Store.Q, d, in.Revision)
	if err != nil {
		return nil, err
	}
	reason := docs.RevertReason(r.N, in.Reason)
	if d.Kind == docs.KindManifest {
		p, err := a.Store.Q.GetProject(ctx, d.ProjectID)
		if err != nil {
			return nil, err
		}
		return a.SaveManifest(ctx, act, p, []byte(r.Text), reason, r.N, in.IfRevision,
			Call{Command: registry.Builtin + ".document_revert", Input: map[string]any{"document": d.ID, "revision": r.N, "reason": in.Reason}})
	}
	var n int64
	err = a.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		ref := docs.Ref{Scope: docs.Scope{Workspace: d.WorkspaceID, Project: d.ProjectID}, OwnerKind: d.OwnerKind, OwnerID: d.OwnerID, Name: d.Name}
		if cur, err := q.GetDocument(ctx, d.ID); err != nil || cur.DeletedAt.Valid || in.IfRevision != 0 && cur.Current != in.IfRevision {
			switch {
			case err == nil && cur.DeletedAt.Valid:
				err = registry.NotFound("deck: no document %d", d.ID)
			case err == nil:
				err = registry.Conflict("deck: document %d is at revision %d now, not %d", d.ID, cur.Current, in.IfRevision)
			}
			return err
		}
		if _, n, err = docs.Save(ctx, q, ref, d.Kind, r.Text, act.Author(), reason); err != nil {
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
	if d.Kind != docs.KindScript || d.ProjectID == 0 || d.OwnerKind != "" {
		return nil
	}
	prev := db.Document{ID: d.ID, Current: n - 1}
	return a.unattended(ctx, q, act, d.ProjectID, prev, n, fmt.Sprintf("%s changed script %s", act.Author(), d.Name), reason)
}

func (a *App) documentCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("documents_list", "List documents",
			"Lists documents of a project, or workspace documents when no project is given; deleted ones for the owner when asked.",
			registry.Everyone, a.documentsList).Enum("kind", docs.Kinds...),
		registry.Def("document_get", "Get document",
			"Returns a document by id, or by project and name; the current revision unless one is given.",
			registry.Everyone, a.documentGet),
		registry.Def("document_save", "Save document",
			"Writes a new revision of a document, creating it on first save.",
			registry.Everyone, a.documentSave).Enum("kind", docs.Kinds...),
		registry.Def("document_delete", "Delete document",
			"Deletes a document; its revisions stay and the owner can bring it back. Agents delete only the memory of their project.",
			registry.Everyone, a.documentDelete),
		registry.Def("document_restore", "Bring document back",
			"Brings a deleted document back as it was.",
			registry.OwnerOnly, a.documentRestore),
		registry.Def("document_revisions", "Document history",
			"Lists revisions of a document: who, when and why.",
			registry.Everyone, a.documentRevisions),
		registry.Def("document_revert", "Revert document",
			"Makes an earlier revision current again as a new revision.",
			registry.OwnerSide, a.documentRevert),
	}
}
