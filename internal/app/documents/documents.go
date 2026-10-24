// Package documents keeps the documents of projects and the workspace with their revisions, the skills deck
// ships and the health of script commands.
package documents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Service keeps the documents; Ctx and Go are the life of deck, for the checks of scripts in the background.
type Service struct {
	St      *store.Store
	Now     func() time.Time
	Sandbox sandbox.Sandbox
	// Changed lists a change made without the owner.
	Changed func(ctx context.Context, q *db.Queries, act registry.Actor, projectID int64, prev db.Document, n int64, text, reason string) error
	// SaveManifest saves a manifest through its checks; a change that widens rights waits for the owner.
	SaveManifest func(ctx context.Context, act registry.Actor, p db.Project, raw []byte, reason string, revertTo, ifRevision int64) (any, error)
	Ctx          context.Context
	// Go runs work beside the calls; false once deck stops.
	Go func(func()) bool
}

type documentView struct {
	ID        int64  `json:"id"`
	Project   string `json:"project,omitempty"`
	OwnerKind string `json:"owner_kind"`
	OwnerID   int64  `json:"owner_id"`
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Revision  int64  `json:"revision"`
	Text      string `json:"text,omitempty"`
	Deleted   bool   `json:"deleted"`
	// Editable is a document a screen edits as text.
	Editable bool `json:"editable"`
	// Warning says what a save did that its author should know of, such as an index past its limit.
	Warning   string         `json:"warning,omitempty"`
	Revisions []revisionView `json:"revisions,omitempty"`
}

func (dc *Service) docView(ctx context.Context, d db.Document) documentView {
	return view(d, access.Name(ctx, dc.St.Q, d.ProjectID.Int64))
}

// view is a document as the lists show it, in its project.
func view(d db.Document, project string) documentView {
	kind, owner := docs.Owner(d)
	return documentView{ID: d.ID, Project: project, OwnerKind: kind, OwnerID: owner, Kind: d.Kind, Name: d.Name, Revision: d.Current,
		Deleted: d.DeletedAt.Valid, Editable: docs.Editable(d.Kind, d.Name)}
}

type documentsListIn struct {
	Project     string `type:"project" opt:"" desc:"Project name; without it an agent of a project works in its own, the owner and the master in the workspace documents (memory/ names: the owner's memory, in project master)"`
	Kind        string `opt:"" desc:"Only this kind"`
	Deleted     bool   `opt:"" desc:"The deleted documents instead, which the owner can bring back"`
	WithDeleted bool   `json:"with_deleted" opt:"" desc:"The deleted documents after the others when the owner reads; others get the rest alone"`
	Record      int64  `type:"record" opt:"" desc:"Only the documents of this record"`
}

func (dc *Service) documentsList(ctx context.Context, act registry.Actor, in documentsListIn) ([]documentView, error) {
	var s docs.Scope
	var err error
	if in.Project == "" && in.Kind == docs.KindMemory {
		s, err = dc.memoryScope(ctx, act, "", docs.MemoryIndex)
	} else {
		s, err = dc.readDocScope(ctx, act, in.Project, "")
	}
	if err != nil {
		return nil, err
	}
	if in.Deleted && act.Kind != registry.Owner {
		return nil, registry.Refuse("deck: only the owner lists deleted documents")
	}
	in.WithDeleted = in.WithDeleted && act.Kind == registry.Owner
	var ds []db.Document
	if !in.Deleted {
		if ds, err = dc.St.Q.ListDocuments(ctx, db.ListDocumentsParams{WorkspaceID: s.Workspace, ProjectID: store.ID(s.Project), Kind: in.Kind, RecordID: in.Record}); err != nil {
			return nil, err
		}
	}
	if in.Deleted || in.WithDeleted {
		gone, err := dc.St.Q.ListDeletedDocuments(ctx, db.ListDeletedDocumentsParams{WorkspaceID: s.Workspace, ProjectID: store.ID(s.Project)})
		if err != nil {
			return nil, err
		}
		ds = append(ds, gone...)
	}
	out, project := make([]documentView, 0, len(ds)), access.Name(ctx, dc.St.Q, s.Project)
	for _, d := range ds {
		if in.Record != 0 && d.RecordID.Int64 != in.Record {
			continue
		}
		p := db.Project{ID: d.ProjectID.Int64, Name: in.Project}
		if d.ProjectID.Valid && access.MayRead(ctx, dc.St.Q, act, p, "", d.Name) != nil {
			continue
		}
		out = append(out, view(d, project))
	}
	return out, nil
}

// readDocScope is where a document read works; another project only as far as reads allow.
func (dc *Service) readDocScope(ctx context.Context, act registry.Actor, project, name string) (docs.Scope, error) {
	if project == "" {
		return access.DocScope(ctx, dc.St.Q, act, project)
	}
	p, err := access.ReadProject(ctx, dc.St.Q, act, project, "", name)
	return docs.ProjectScope(p), err
}

// readableDocument loads a document the actor may read: its own project's, its workspace's, or one that reads
// in its manifest names.
func (dc *Service) readableDocument(ctx context.Context, q *db.Queries, act registry.Actor, id int64) (db.Document, error) {
	d, err := docs.Get(ctx, q, id)
	if err != nil {
		return d, err
	}
	if d.WorkspaceID != act.Workspace {
		return d, registry.NotFound("deck: no document %d", id)
	}
	if !d.ProjectID.Valid || act.Within(d.ProjectID.Int64) {
		return d, nil
	}
	p, err := q.GetProject(ctx, d.ProjectID.Int64)
	if err != nil {
		return d, err
	}
	return d, access.MayRead(ctx, q, act, p, "", d.Name)
}

type documentGetIn struct {
	Document int64  `type:"document" opt:"" desc:"Document id"`
	Project  string `type:"project" opt:"" desc:"Project name; without it an agent of a project works in its own, the owner and the master in the workspace documents (memory/ names: the owner's memory, in project master)"`
	Name     string `opt:"" desc:"Document name (path)"`
	Revision int64  `opt:"" desc:"Revision number"`
	Record   int64  `type:"record" opt:"" desc:"Record the named document belongs to"`
	History  bool   `opt:"" desc:"Also its revisions, newest first, with who wrote each and why"`
}

// withoutProject is the hint of a name missing in a project that the caller finds among the documents of the workspace.
func (dc *Service) withoutProject(ctx context.Context, act registry.Actor, in documentGetIn) string {
	if in.Project == "" || in.Record != 0 {
		return ""
	}
	s, err := dc.memoryScope(ctx, act, "", in.Name)
	if err != nil || s.Project != 0 {
		return ""
	}
	if _, err := docs.Find(ctx, dc.St.Q, docs.Ref{Scope: s, Name: in.Name}); err != nil {
		return ""
	}
	return fmt.Sprintf(" in project %s; it is a document of the workspace: leave out project", in.Project)
}

func (dc *Service) documentGet(ctx context.Context, act registry.Actor, in documentGetIn) (documentView, error) {
	var d db.Document
	var err error
	switch {
	case in.Document != 0:
		d, err = dc.readableDocument(ctx, dc.St.Q, act, in.Document)
	case in.Name != "":
		var s docs.Scope
		if in.Project == "" {
			s, err = dc.memoryScope(ctx, act, "", in.Name)
		} else {
			s, err = dc.readDocScope(ctx, act, in.Project, in.Name)
		}
		ref := docs.Ref{Scope: s, Name: in.Name}
		if in.Record != 0 {
			ref.OwnerKind, ref.OwnerID = docs.OwnerRecord, in.Record
		}
		if err == nil {
			d, err = docs.Find(ctx, dc.St.Q, ref)
			if errors.Is(err, sql.ErrNoRows) {
				err = registry.NotFound("deck: no document %q%s", in.Name, dc.withoutProject(ctx, act, in))
			}
		}
	default:
		err = registry.Invalid("deck: give a document id or a name")
	}
	if err != nil {
		return documentView{}, err
	}
	r, err := docs.Revision(ctx, dc.St.Q, d, in.Revision)
	if err != nil {
		return documentView{}, err
	}
	v := dc.docView(ctx, d)
	v.Revision, v.Text = r.N, r.Text
	if in.History {
		v.Revisions, err = dc.revisions(ctx, d)
	}
	return v, err
}

type documentSaveIn struct {
	Name       string   `desc:"Document name (path), e.g. notes/criteria.md; memory/MEMORY.md and memory/<fact>.md for memory"`
	Kind       string   `opt:"" desc:"Document kind; needed only to create a document"`
	Text       string   `type:"text" opt:"" desc:"Full new text; leave it out to change parts of the current text with old and new"`
	Old        []string `type:"text[]" opt:"" desc:"Parts of the current text to replace, each occurring in it exactly once, as it is or, in md documents, with runs of spaces and line breaks taken as one"`
	New        []string `type:"text[]" opt:"" desc:"The text that replaces each old part, in the same order"`
	IfRevision int64    `json:"if_revision" opt:"" desc:"Only while the current revision is still this one"`
	Project    string   `type:"project" opt:"" desc:"Project name; without it an agent of a project works in its own, the owner and the master in the workspace documents (memory/ names: the owner's memory, in project master)"`
	Record     int64    `type:"record" opt:"" desc:"Record the document belongs to"`
	Reason     string   `opt:"" desc:"Why it changed"`
}

func (dc *Service) documentSave(ctx context.Context, act registry.Actor, in documentSaveIn) (any, error) {
	if err := ownSave(in.Kind); err != nil {
		return nil, err
	}
	s, err := dc.memoryScope(ctx, act, in.Project, in.Name)
	if err != nil {
		return nil, err
	}
	var d db.Document
	err = dc.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
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
		if in.Kind == "" {
			old, err := docs.Find(ctx, q, ref)
			if errors.Is(err, sql.ErrNoRows) {
				return registry.Invalid("deck: no document %q yet; give its kind to create it", in.Name)
			}
			if err != nil {
				return err
			}
			in.Kind = old.Kind
		}
		if err := ownSave(in.Kind); err != nil {
			return err
		}
		if err := mayWrite(ctx, q, act, s, in.Kind, in.Name); err != nil {
			return err
		}
		if err := editText(ctx, q, ref, &in); err != nil {
			return err
		}
		var n int64
		var changed bool
		if d, n, changed, err = docs.Save(ctx, q, ref, in.Kind, in.Text, act.Author(), in.Reason); err != nil || !changed {
			return err
		}
		return dc.docChanged(ctx, q, act, d, n, in.Reason)
	})
	if err != nil {
		return nil, err
	}
	v := dc.docView(ctx, d)
	if kind, _ := docs.Owner(d); d.Name == docs.MemoryIndex && kind == "" {
		if n := docs.IndexLines(in.Text); n > docs.IndexLimit {
			v.Warning = fmt.Sprintf("the index has %d lines; only the first %d reach prompts: merge or drop lines, the most important first", n, docs.IndexLimit)
		}
	}
	return v, nil
}

// ownSave refuses the kinds that have their own command to save them, which checks them.
func ownSave(kind string) error {
	switch kind {
	case docs.KindManifest:
		return registry.Invalid("deck: save manifests with deck.manifest_save")
	case docs.KindTheme:
		return registry.Invalid("deck: save themes with deck.theme_save")
	}
	return nil
}

// mayWrite refuses an agent or script a document of its project other than artifacts and memory unless writes in
// the manifest in force names it.
func mayWrite(ctx context.Context, q *db.Queries, act registry.Actor, s docs.Scope, kind, name string) error {
	if act.Kind != registry.Agent || kind == docs.KindArtifact || kind == docs.KindMemory {
		return nil
	}
	if s.Project != 0 {
		m, err := docs.For(ctx, q, act, s.Project)
		if err != nil {
			return err
		}
		if m.MayWrite(name) {
			return nil
		}
	}
	return registry.Refuse("deck: this project does not write %s; its agents write artifacts, its memory and the documents writes in its manifest names (%q, or its folder with / at the end), and the owner grants it", name, name)
}

// editText checks if_revision and, for old and new parts, makes the text of the save from the current one.
func editText(ctx context.Context, q *db.Queries, ref docs.Ref, in *documentSaveIn) error {
	if len(in.Old) == 0 && len(in.New) == 0 && in.Text == "" {
		return registry.Invalid("deck: give the full text, or old and new parts of the current one")
	}
	if len(in.Old) == 0 && len(in.New) == 0 && in.IfRevision == 0 {
		return nil
	}
	if len(in.Old) > 0 && in.Text != "" {
		return registry.Invalid("deck: give the full text or old and new parts, not both")
	}
	d, err := docs.Find(ctx, q, ref)
	if errors.Is(err, sql.ErrNoRows) {
		if in.IfRevision != 0 || len(in.Old) > 0 {
			return registry.NotFound("deck: no document %q to change; save it with its full text", ref.Name)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if in.IfRevision != 0 && d.Current != in.IfRevision {
		return registry.Conflict("deck: %s is at revision %d now, not %d", ref.Name, d.Current, in.IfRevision)
	}
	if len(in.Old) == 0 && len(in.New) == 0 {
		return nil
	}
	r, err := docs.Revision(ctx, q, d, 0)
	if err != nil {
		return err
	}
	in.Text, err = docs.Edit(r.Text, in.Old, in.New, docs.Loose(d.Kind, d.Name))
	return err
}

// memoryScope is access.DocScope, except that memory of no project is the memory of the owner, in project master.
func (dc *Service) memoryScope(ctx context.Context, act registry.Actor, project, name string) (docs.Scope, error) {
	s, err := access.DocScope(ctx, dc.St.Q, act, project)
	if err != nil || s.Project != 0 || !strings.HasPrefix(name, docs.MemoryDir) {
		return s, err
	}
	p, err := access.Master(ctx, dc.St.Q, act.Workspace)
	return docs.ProjectScope(p), err
}

type documentDeleteIn struct {
	Document int64  `type:"document" opt:"" desc:"Document id"`
	Project  string `type:"project" opt:"" desc:"Project name; without it an agent of a project works in its own, the owner and the master in the workspace documents (memory/ names: the owner's memory, in project master)"`
	Name     string `opt:"" desc:"Document name (path)"`
}

// documentDelete hides a document with its revisions; agents delete only the memory of their project.
func (dc *Service) documentDelete(ctx context.Context, act registry.Actor, in documentDeleteIn) (any, error) {
	var d db.Document
	var err error
	switch {
	case in.Document != 0:
		d, err = dc.readableDocument(ctx, dc.St.Q, act, in.Document)
	case in.Name != "":
		var s docs.Scope
		if s, err = dc.memoryScope(ctx, act, in.Project, in.Name); err == nil {
			d, err = docs.Find(ctx, dc.St.Q, docs.Ref{Scope: s, Name: in.Name})
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
	case d.Kind == docs.KindManifest || d.Kind == docs.KindPrompt && !d.RecordID.Valid:
		return nil, registry.Invalid("deck: a project keeps its %s; change it instead", d.Name)
	case act.Kind == registry.Agent && (d.Kind != docs.KindMemory || d.ProjectID.Int64 != act.ProjectID):
		return nil, registry.Refuse("deck: agents delete only the memory of their project")
	}
	err = dc.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.DeleteDocument(ctx, db.DeleteDocumentParams{DeletedAt: sql.NullString{String: store.Time(dc.Now()), Valid: true}, ID: d.ID})
		return err
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"document": d.ID, "deleted": true}, nil
}

type documentRestoreIn struct {
	Document int64 `type:"document" desc:"Document id"`
}

// documentRestore brings a deleted document back as it was.
func (dc *Service) documentRestore(ctx context.Context, act registry.Actor, in documentRestoreIn) (any, error) {
	d, err := dc.St.Q.GetDocument(ctx, in.Document)
	if errors.Is(err, sql.ErrNoRows) || err == nil && d.WorkspaceID != act.Workspace {
		return nil, registry.NotFound("deck: no document %d", in.Document)
	}
	if err != nil {
		return nil, err
	}
	var n int64
	err = dc.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := docs.Find(ctx, q, docs.RefOf(d)); err == nil {
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
	return map[string]any{"document": d.ID, "restored": true}, nil
}

// Commands are the commands of documents.
func (dc *Service) Commands() []*registry.Command {
	return []*registry.Command{
		registry.Read("documents_list", "List documents",
			"Lists documents of a project, or workspace documents when no project is given; deleted ones for the owner when asked.",
			registry.Everyone, dc.documentsList).Enum("kind", docs.Kinds...).On("documents"),
		registry.Read("document_get", "Get document",
			"Returns a document by id, or by project and name; the current revision unless one is given.",
			registry.Everyone, dc.documentGet).On("documents"),
		registry.Def("document_save", "Save document",
			"Writes a new revision of a document, creating it on first save.",
			registry.Everyone, dc.documentSave).Enum("kind", docs.Kinds...),
		registry.Def("document_delete", "Delete document",
			"Deletes a document; its revisions stay and the owner can bring it back. Agents delete only the memory of their project.",
			registry.Everyone, dc.documentDelete),
		registry.Def("document_restore", "Bring document back",
			"Brings a deleted document back as it was.",
			registry.OwnerOnly, dc.documentRestore),
		registry.Def("document_revert", "Revert document",
			"Makes an earlier revision current again as a new revision.",
			registry.OwnerSide, dc.documentRevert),
		registry.Def("programs_check", "Check programs",
			"Looks again for the programs the scripts of a project need, after the owner installed one, and updates the health of its commands.",
			registry.OwnerSide, dc.programsCheck),
	}
}
