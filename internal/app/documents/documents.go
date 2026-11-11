// Package documents keeps the documents of projects and the workspace with their revisions, the skills deck
// ships and the health of script commands.
package documents

import (
	"context"
	"database/sql"
	"deck2/internal/events"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"deck2/internal/access"
	"deck2/internal/docs"
	"deck2/internal/gateway"
	"deck2/internal/home"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// Service keeps the documents; Go runs the checks of scripts in the background.
type Service struct {
	St      *store.Store
	Now     func() time.Time
	Sandbox *sandbox.Sandbox
	// Missing reports the scripts of a project whose programs are not on the machine they run on, by script name.
	Missing func(ctx context.Context, projectID int64, m *manifest.Manifest) map[string]error
	// Noted lists a change made without the owner.
	Noted func(ctx context.Context, q *db.Queries, act registry.Actor, projectID int64, prev db.Document, n int64, text, what, reason string) error
	// SaveManifest saves a manifest through its checks; a change that widens rights waits for the owner.
	SaveManifest func(ctx context.Context, act registry.Actor, p db.Project, raw []byte, reason string, revertTo, ifRevision int64) (any, error)
	// Go runs work beside the calls; false once deck stops.
	Go func(func(context.Context)) bool
	// Home is where the picture of the memory map is written; Fits are the models a command starts on, in order.
	Home home.Layout
	Fits func(ctx context.Context, projectID int64, cmd *manifest.Command) []string
	// Language is the language of the interface of a workspace, the picture's.
	Language func(ctx context.Context, workspace int64) string
	// Saw notes the revision of a document a running turn read or saved; Seen gives it back, 0 when none.
	Saw  func(turn, doc, revision int64)
	Seen func(turn, doc int64) int64
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
	// Warning says what the author of the text should know of: an index past its limit, a text that fails its check.
	Warning   string         `json:"warning,omitempty"`
	Revisions []revisionView `json:"revisions,omitempty"`
	// Reads is how many times a memory document reached a turn of an agent or a search, LastRead when it last did.
	Reads    int64  `json:"reads,omitempty"`
	LastRead string `json:"last_read_at,omitempty"`
}

// readsOf are the reads of memory documents by their ids.
func (s *Service) readsOf(ctx context.Context, ids []int64) map[int64]db.MemoryRead {
	out := map[int64]db.MemoryRead{}
	if len(ids) == 0 {
		return out
	}
	rows, err := s.St.Q.ListMemoryReads(ctx, ids)
	if err != nil {
		return out
	}
	for _, r := range rows {
		out[r.DocumentID] = r
	}
	return out
}

func (s *Service) docView(ctx context.Context, d db.Document) documentView {
	return view(d, access.Name(ctx, s.St.Q, d.ProjectID.Int64))
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

func (s *Service) documentsList(ctx context.Context, act registry.Actor, in documentsListIn) ([]documentView, error) {
	var sc docs.Scope
	var err error
	if in.Project == "" && in.Kind == docs.KindMemory {
		sc, err = s.memoryScope(ctx, act, "", docs.MemoryIndex)
	} else {
		sc, err = s.readDocScope(ctx, act, in.Project, "")
	}
	if err != nil {
		return nil, err
	}
	if in.Deleted && act.Kind != registry.Owner {
		return nil, registry.Refuse("only the owner lists deleted documents")
	}
	in.WithDeleted = in.WithDeleted && act.Kind == registry.Owner
	var ds []db.Document
	if !in.Deleted {
		if ds, err = s.St.Q.ListDocuments(ctx, db.ListDocumentsParams{WorkspaceID: sc.Workspace, ProjectID: store.ID(sc.Project), Kind: in.Kind, RecordID: in.Record}); err != nil {
			return nil, err
		}
	}
	if in.Deleted || in.WithDeleted {
		gone, err := s.St.Q.ListDeletedDocuments(ctx, db.ListDeletedDocumentsParams{WorkspaceID: sc.Workspace, ProjectID: store.ID(sc.Project)})
		if err != nil {
			return nil, err
		}
		ds = append(ds, gone...)
	}
	out, project := make([]documentView, 0, len(ds)), access.Name(ctx, s.St.Q, sc.Project)
	for _, d := range ds {
		if in.Record != 0 && d.RecordID.Int64 != in.Record {
			continue
		}
		p := db.Project{ID: d.ProjectID.Int64, Name: in.Project}
		if d.ProjectID.Valid && access.MayRead(ctx, s.St.Q, act, p, "", d.Name) != nil {
			continue
		}
		out = append(out, view(d, project))
	}
	var memory []int64
	for _, v := range out {
		if v.Kind == docs.KindMemory {
			memory = append(memory, v.ID)
		}
	}
	reads := s.readsOf(ctx, memory)
	for i := range out {
		if r, ok := reads[out[i].ID]; ok {
			out[i].Reads, out[i].LastRead = r.Reads, r.LastReadAt
		}
	}
	return out, nil
}

// readDocScope is where a document read works; another project only as far as reads allow.
func (s *Service) readDocScope(ctx context.Context, act registry.Actor, project, name string) (docs.Scope, error) {
	if project == "" {
		return access.DocScope(ctx, s.St.Q, act, project)
	}
	p, err := access.ReadProject(ctx, s.St.Q, act, project, "", name)
	return docs.ProjectScope(p), err
}

// readableDocument loads a document the actor may read: its own project's, its workspace's, or one that reads
// in its manifest names.
func (s *Service) readableDocument(ctx context.Context, q *db.Queries, act registry.Actor, id int64) (db.Document, error) {
	d, err := docs.Get(ctx, q, id)
	if err != nil {
		return d, err
	}
	if d.WorkspaceID != act.Workspace {
		return d, registry.NotFound("no document %d", id)
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
func (s *Service) withoutProject(ctx context.Context, act registry.Actor, in documentGetIn) string {
	if in.Project == "" || in.Record != 0 {
		return ""
	}
	sc, err := s.memoryScope(ctx, act, "", in.Name)
	if err != nil || sc.Project != 0 {
		return ""
	}
	if _, err := docs.Find(ctx, s.St.Q, docs.Ref{Scope: sc, Name: in.Name}); err != nil {
		return ""
	}
	return fmt.Sprintf(" in project %s; it is a document of the workspace: leave out project", in.Project)
}

func (s *Service) documentGet(ctx context.Context, act registry.Actor, in documentGetIn) (documentView, error) {
	var d db.Document
	var err error
	switch {
	case in.Document != 0:
		d, err = s.readableDocument(ctx, s.St.Q, act, in.Document)
	case in.Name != "":
		var sc docs.Scope
		if in.Project == "" {
			sc, err = s.memoryScope(ctx, act, "", in.Name)
		} else {
			sc, err = s.readDocScope(ctx, act, in.Project, in.Name)
		}
		ref := docs.Ref{Scope: sc, Name: in.Name}
		if in.Record != 0 {
			ref.OwnerKind, ref.OwnerID = docs.OwnerRecord, in.Record
		}
		if err == nil {
			d, err = docs.Find(ctx, s.St.Q, ref)
			if errors.Is(err, sql.ErrNoRows) {
				err = registry.NotFound("no document %q%s", in.Name, s.withoutProject(ctx, act, in))
			}
		}
	default:
		err = registry.Invalid("give a document id or a name")
	}
	if err != nil {
		return documentView{}, err
	}
	r, err := docs.Revision(ctx, s.St.Q, d, in.Revision)
	if err != nil {
		return documentView{}, err
	}
	v := s.docView(ctx, d)
	v.Revision, v.Text = r.N, r.Text
	if r.N == d.Current && gateway.Live(ctx) && act.ProcessID != 0 {
		s.Saw(act.ProcessID, d.ID, r.N)
	}
	if r.N == d.Current {
		if err := docs.CheckMemory(d.Kind, d.Name, r.Text); err != nil {
			v.Warning = "this text would not save as it is: " + err.Error()
		} else {
			v.Warning = lessonWarning(ctx, s.St.Q, d, r.Text)
		}
	}
	if in.History {
		v.Revisions, err = s.revisions(ctx, d)
	}
	if err == nil && (act.Kind == registry.Agent || act.Kind == registry.Master) && r.N == d.Current && d.Kind == docs.KindMemory && d.Name != docs.MemoryIndex {
		docs.Reached(ctx, s.St, s.Now(), d.ID)
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

func (s *Service) documentSave(ctx context.Context, act registry.Actor, in documentSaveIn) (any, error) {
	if err := ownSave(in.Kind); err != nil {
		return nil, err
	}
	sc, err := s.memoryScope(ctx, act, in.Project, in.Name)
	if err != nil {
		return nil, err
	}
	var d db.Document
	var saved int64
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		ref := docs.Ref{Scope: sc, Name: in.Name}
		if in.Record != 0 {
			rec, err := q.GetRecord(ctx, in.Record)
			if errors.Is(err, sql.ErrNoRows) || err == nil && (sc.Project == 0 || rec.ProjectID != sc.Project) {
				return registry.NotFound("no record %d in this project", in.Record)
			}
			if err != nil {
				return err
			}
			ref.OwnerKind, ref.OwnerID = "record", rec.ID
		}
		if in.Kind == "" {
			old, err := docs.Find(ctx, q, ref)
			if errors.Is(err, sql.ErrNoRows) {
				return registry.Invalid("no document %q yet; give its kind to create it", in.Name)
			}
			if err != nil {
				return err
			}
			in.Kind = old.Kind
		}
		if err := ownSave(in.Kind); err != nil {
			return err
		}
		if err := mayWrite(ctx, q, act, sc, in.Kind, in.Name); err != nil {
			return err
		}
		if err := s.behind(ctx, q, act, ref, in); err != nil {
			return err
		}
		if err := editText(ctx, q, ref, &in); err != nil {
			return err
		}
		if in.Kind == docs.KindMemory {
			cur, err := docs.CurrentText(ctx, q, sc, docs.KindMemory, in.Name)
			if err != nil {
				return err
			}
			if err := ownersFact(ctx, q, act, in.Name, cur, in.Text); err != nil {
				return err
			}
		}
		var changed bool
		if d, saved, changed, err = docs.Save(ctx, q, ref, in.Kind, in.Text, act.Author(), in.Reason); err != nil || !changed {
			return err
		}
		return s.docChanged(ctx, q, act, d, saved, in.Reason)
	})
	if err != nil {
		return nil, err
	}
	if gateway.Live(ctx) && act.ProcessID != 0 && saved != 0 {
		s.Saw(act.ProcessID, d.ID, saved)
	}
	v := s.docView(ctx, d)
	v.Warning = lessonWarning(ctx, s.St.Q, d, in.Text)
	if kind, _ := docs.Owner(d); d.Name == docs.MemoryIndex && kind == "" {
		var warn []string
		if n := docs.IndexLines(in.Text); n > docs.IndexLimit {
			warn = append(warn, fmt.Sprintf("the index has %d lines; only the first %d reach prompts: merge or drop lines, the most important first", n, docs.IndexLimit))
		}
		if n := docs.LessonLines(in.Text); n > docs.LessonLimit {
			warn = append(warn, fmt.Sprintf("the lessons section has %d lines; only the first %d reach prompts: merge or drop lessons, the most useful first", n, docs.LessonLimit))
		}
		v.Warning = strings.Join(warn, "; ")
	}
	return v, nil
}

// lessonWarning says that a lesson names a command its project does not have, so it reaches no run.
func lessonWarning(ctx context.Context, q *db.Queries, d db.Document, text string) string {
	if d.Kind != docs.KindMemory || d.Name == docs.MemoryIndex || !d.ProjectID.Valid {
		return ""
	}
	f, err := docs.ParseFact(text)
	if err != nil || f.Type != docs.Lesson || f.Command == "" {
		return ""
	}
	p, err := q.GetProject(ctx, d.ProjectID.Int64)
	if err != nil {
		return ""
	}
	if m, err := docs.Manifest(ctx, q, p); err != nil || m.Command(f.Command) != nil {
		return ""
	}
	return fmt.Sprintf("no command %s in project %s: this lesson reaches no run", f.Command, p.Name)
}

// ownSave refuses the kinds that have their own command to save them, which checks them.
func ownSave(kind string) error {
	switch kind {
	case docs.KindManifest:
		return registry.Invalid("save manifests with deck.manifest_save")
	case docs.KindTheme:
		return registry.Invalid("save themes with deck.theme_save")
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
	return registry.Refuse("this project does not write %s; its agents write artifacts, its memory and the documents writes in its manifest names (%q, or its folder with / at the end), and the owner grants it", name, name)
}

// ownersFact refuses a run no one started by hand a save or delete of a memory fact that is not a lesson, as it is
// or as it would be; the index is the agent's to keep.
func ownersFact(ctx context.Context, q *db.Queries, act registry.Actor, name string, texts ...string) error {
	if name == docs.MemoryIndex || !strings.HasPrefix(name, docs.MemoryDir) {
		return nil
	}
	fact := false
	for _, t := range texts {
		if f, err := docs.ParseFact(t); t != "" && (err != nil || f.Type != docs.Lesson) {
			fact = true
		}
	}
	if !fact {
		return nil
	}
	if alone, err := unattended(ctx, q, act); err != nil || !alone {
		return err
	}
	return registry.Refuse("%s is a fact of the owner and no one started this run by hand: propose it with document_propose; the owner decides", name)
}

// unattended is a run of a schedule or an event, or one such a run started; a reply to the owner's message is not.
func unattended(ctx context.Context, q *db.Queries, act registry.Actor) (bool, error) {
	if act.Kind == registry.Trigger {
		return true, nil
	}
	for id := act.ProcessID; id != 0; {
		p, err := q.GetProcess(ctx, id)
		if err != nil {
			return false, err
		}
		if p.TriggerID.Valid {
			t, err := q.GetTrigger(ctx, p.TriggerID.Int64)
			if errors.Is(err, sql.ErrNoRows) {
				return true, nil
			}
			return t.Kind != manifest.OnMessage, err
		}
		id = p.ParentID.Int64
	}
	return false, nil
}

// behind refuses a whole text a turn saves without if_revision over what others saved after the revision it read.
func (s *Service) behind(ctx context.Context, q *db.Queries, act registry.Actor, ref docs.Ref, in documentSaveIn) error {
	if !gateway.Live(ctx) || act.ProcessID == 0 || in.Text == "" || len(in.Old) > 0 || in.IfRevision != 0 {
		return nil
	}
	d, err := docs.Find(ctx, q, ref)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return docs.Since(ctx, q, d, s.Seen(act.ProcessID, d.ID))
}

// editText checks if_revision and, for old and new parts, makes the text of the save from the current one.
func editText(ctx context.Context, q *db.Queries, ref docs.Ref, in *documentSaveIn) error {
	if len(in.Old) == 0 && len(in.New) == 0 && in.Text == "" {
		return registry.Invalid("give the full text, or old and new parts of the current one")
	}
	if len(in.Old) == 0 && len(in.New) == 0 && in.IfRevision == 0 {
		return nil
	}
	if len(in.Old) > 0 && in.Text != "" {
		return registry.Invalid("give the full text or old and new parts, not both")
	}
	d, err := docs.Find(ctx, q, ref)
	if errors.Is(err, sql.ErrNoRows) {
		if in.IfRevision != 0 || len(in.Old) > 0 {
			return registry.NotFound("no document %q to change; save it with its full text", ref.Name)
		}
		return nil
	}
	if err != nil {
		return err
	}
	if in.IfRevision != 0 && d.Current != in.IfRevision {
		return registry.Conflict("%s is at revision %d now, not %d", ref.Name, d.Current, in.IfRevision)
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
func (s *Service) memoryScope(ctx context.Context, act registry.Actor, project, name string) (docs.Scope, error) {
	sc, err := access.DocScope(ctx, s.St.Q, act, project)
	if err != nil || sc.Project != 0 || !strings.HasPrefix(name, docs.MemoryDir) {
		return sc, err
	}
	p, err := access.Master(ctx, s.St.Q, act.Workspace)
	return docs.ProjectScope(p), err
}

type documentDeleteIn struct {
	Document int64  `type:"document" opt:"" desc:"Document id"`
	Project  string `type:"project" opt:"" desc:"Project name; without it an agent of a project works in its own, the owner and the master in the workspace documents (memory/ names: the owner's memory, in project master)"`
	Name     string `opt:"" desc:"Document name (path)"`
}

// documentDelete hides a document with its revisions; agents delete only the memory of their project.
func (s *Service) documentDelete(ctx context.Context, act registry.Actor, in documentDeleteIn) (any, error) {
	var d db.Document
	var err error
	switch {
	case in.Document != 0:
		d, err = s.readableDocument(ctx, s.St.Q, act, in.Document)
	case in.Name != "":
		var sc docs.Scope
		if sc, err = s.memoryScope(ctx, act, in.Project, in.Name); err == nil {
			d, err = docs.Find(ctx, s.St.Q, docs.Ref{Scope: sc, Name: in.Name})
			if errors.Is(err, sql.ErrNoRows) {
				err = registry.NotFound("no document %q", in.Name)
			}
		}
	default:
		err = registry.Invalid("give a document id or a name")
	}
	if err != nil {
		return nil, err
	}
	switch {
	case d.Kind == docs.KindManifest || d.Kind == docs.KindPrompt && !d.RecordID.Valid:
		return nil, registry.Invalid("a project keeps its %s; change it instead", d.Name)
	case act.Kind == registry.Agent && (d.Kind != docs.KindMemory || d.ProjectID.Int64 != act.ProjectID):
		return nil, registry.Refuse("agents delete only the memory of their project")
	}
	if d.Kind == docs.KindMemory {
		r, err := docs.Revision(ctx, s.St.Q, d, 0)
		if err != nil {
			return nil, err
		}
		// A fact past the date the owner gave it may go.
		if f, err := docs.ParseFact(r.Text); err != nil || !f.Expired(s.Now()) {
			if err := ownersFact(ctx, s.St.Q, act, d.Name, r.Text); err != nil {
				return nil, err
			}
		}
	}
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.DeleteDocument(ctx, db.DeleteDocumentParams{DeletedAt: store.At(s.Now()), ID: d.ID})
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
func (s *Service) documentRestore(ctx context.Context, act registry.Actor, in documentRestoreIn) (any, error) {
	d, err := s.St.Q.GetDocument(ctx, in.Document)
	if errors.Is(err, sql.ErrNoRows) || err == nil && d.WorkspaceID != act.Workspace {
		return nil, registry.NotFound("no document %d", in.Document)
	}
	if err != nil {
		return nil, err
	}
	var n int64
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
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
		return nil, registry.Conflict("document %d is not deleted", d.ID)
	}
	return map[string]any{"document": d.ID, "restored": true}, nil
}

// Commands are the commands of documents.
func (s *Service) Commands() []*registry.Command {
	return slices.Concat(s.documentsCommands(), s.revisionsCommands(), s.programsCommands(), s.mapCommands(), s.proposeCommands())
}

func (s *Service) documentsCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("documents_list", "List documents",
			"Lists documents of a project, or workspace documents when no project is given; deleted ones for the owner when asked. A memory document says how many times it reached a turn of an agent or a search (reads) and when it last did: the lessons a turn got, a fact an agent or the master read with document_get or found with memory_search.",
			registry.Everyone, s.documentsList).Enum("kind", docs.Kinds...).On(events.Documents),
		registry.Read("document_get", "Get document",
			"Returns a document by id, or by project and name; the current revision unless one is given.",
			registry.Everyone, s.documentGet).On(events.Documents).
			Answers(`Answer: {"id", "project", "kind", "name", "revision", "text"}.`),
		registry.Def("document_save", "Save document",
			"Writes a new revision of a document, creating it on first save.",
			registry.Everyone, s.documentSave).Enum("kind", docs.Kinds...).
			Answers(`Answer: {"id", "project", "kind", "name", "revision", "warning"}; the text is not in it. Example input: {"name": "notes/plan.md", "old": ["- [ ] call"], "new": ["- [x] call"], "reason": "called"}.`),
		registry.Def("document_delete", "Delete document",
			"Deletes a document; its revisions stay and the owner can bring it back. Agents delete only the memory of their project.",
			registry.Everyone, s.documentDelete),
		registry.Def("document_restore", "Bring document back",
			"Brings a deleted document back as it was.",
			registry.OwnerOnly, s.documentRestore),
		registry.Read("memory_search", "Search memory",
			"Finds words in memory, the summaries of the master's topics, skills and finished processes, those the caller reads; what an index does not list is found here. A memory document says how many times it reached a turn of an agent or a search before (reads) and when it last did; a hit of an agent or the master counts as one.",
			registry.Everyone, s.memorySearch).On(events.Documents, events.Processes),
	}
}
