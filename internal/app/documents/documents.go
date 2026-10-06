// Package documents keeps the documents of projects and the workspace with their revisions, the skills deck
// ships and the health of script commands.
package documents

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"
	"sync"
	"time"

	"deck2/internal/app/access"
	"deck2/internal/docs"
	"deck2/internal/health"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
	"deck2/internal/script"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/skills"
)

// Service keeps the documents; Ctx and BG are the life of deck, for the checks of scripts in the background.
type Service struct {
	St      *store.Store
	Now     func() time.Time
	Sandbox sandbox.Sandbox
	// Changed lists a change made without the owner.
	Changed func(ctx context.Context, q *db.Queries, act registry.Actor, projectID int64, prev db.Document, n int64, text, reason string) error
	// SaveManifest saves a manifest through its checks, with the call the owner allows when it widens rights.
	SaveManifest func(ctx context.Context, act registry.Actor, p db.Project, raw []byte, reason string, revertTo, ifRevision int64, command string, input map[string]any) (any, error)
	Ctx          context.Context
	BG           *sync.WaitGroup
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
	// Warning says what a save did that its author should know of, such as an index past its limit.
	Warning string `json:"warning,omitempty"`
}

func (dc *Service) docView(ctx context.Context, d db.Document) documentView {
	kind, owner := docs.Owner(d)
	return documentView{ID: d.ID, Project: access.Name(ctx, dc.St.Q, d.ProjectID.Int64), OwnerKind: kind, OwnerID: owner, Kind: d.Kind, Name: d.Name, Revision: d.Current,
		Deleted: d.DeletedAt.Valid}
}

type documentsListIn struct {
	Project     string `type:"project" opt:"" desc:"Project name; without it an agent of a project works in its own, the owner and the master in the workspace documents (memory/ names: the owner's memory, in project master)"`
	Kind        string `opt:"" desc:"Only this kind"`
	Deleted     bool   `opt:"" desc:"The deleted documents instead, which the owner can bring back"`
	WithDeleted bool   `json:"with_deleted" opt:"" desc:"The deleted documents after the others, for the owner"`
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
	if (in.Deleted || in.WithDeleted) && act.Kind != registry.Owner {
		return nil, registry.Refuse("deck: only the owner lists deleted documents")
	}
	var ds []db.Document
	if !in.Deleted {
		if ds, err = dc.St.Q.ListDocuments(ctx, db.ListDocumentsParams{WorkspaceID: s.Workspace, ProjectID: store.ID(s.Project), Kind: in.Kind}); err != nil {
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
	out := make([]documentView, 0, len(ds))
	for _, d := range ds {
		if in.Record != 0 && d.RecordID.Int64 != in.Record {
			continue
		}
		p := db.Project{ID: d.ProjectID.Int64, Name: in.Project}
		if d.ProjectID.Valid && access.MayRead(ctx, dc.St.Q, act, p, "", d.Name) != nil {
			continue
		}
		out = append(out, dc.docView(ctx, d))
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
			d, err = dc.St.Q.FindDocument(ctx, ref.Find())
			if errors.Is(err, sql.ErrNoRows) {
				err = registry.NotFound("deck: no document %q", in.Name)
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
	return v, nil
}

type documentSaveIn struct {
	Name       string   `desc:"Document name (path), e.g. notes/criteria.md; memory/MEMORY.md and memory/<fact>.md for memory"`
	Kind       string   `desc:"Document kind"`
	Text       string   `type:"text" opt:"" desc:"Full new text; leave it out to change parts of the current text with old and new"`
	Old        []string `type:"text[]" opt:"" desc:"Parts of the current text to replace, each occurring in it exactly once"`
	New        []string `type:"text[]" opt:"" desc:"The text that replaces each old part, in the same order"`
	IfRevision int64    `json:"if_revision" opt:"" desc:"Only while the current revision is still this one"`
	Project    string   `type:"project" opt:"" desc:"Project name; without it an agent of a project works in its own, the owner and the master in the workspace documents (memory/ names: the owner's memory, in project master)"`
	Record     int64    `type:"record" opt:"" desc:"Record the document belongs to"`
	Reason     string   `opt:"" desc:"Why it changed"`
}

func (dc *Service) documentSave(ctx context.Context, act registry.Actor, in documentSaveIn) (any, error) {
	if in.Kind == docs.KindManifest {
		return nil, registry.Invalid("deck: save manifests with deck.manifest_save")
	}
	if act.Kind == registry.Agent && in.Kind != docs.KindArtifact && in.Kind != docs.KindMemory {
		return nil, registry.Refuse("deck: agents write only artifacts and the memory of their project")
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
		if err := editText(ctx, q, ref, &in); err != nil {
			return err
		}
		var n int64
		var changed bool
		if d, n, changed, err = docs.Save(ctx, q, ref, in.Kind, in.Text, act.Author(), in.Reason); err != nil || !changed {
			return err
		}
		return dc.scriptChanged(ctx, q, act, d, n, in.Reason)
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
	d, err := q.FindDocument(ctx, ref.Find())
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
	in.Text, err = docs.Edit(r.Text, in.Old, in.New)
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
			d, err = dc.St.Q.FindDocument(ctx, docs.Ref{Scope: s, Name: in.Name}.Find())
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
		if _, err := q.FindDocument(ctx, docs.RefOf(d).Find()); err == nil {
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

type documentRevisionsIn struct {
	Document int64 `type:"document" desc:"Document id"`
}

func (dc *Service) documentRevisions(ctx context.Context, act registry.Actor, in documentRevisionsIn) (any, error) {
	d, err := dc.readableDocument(ctx, dc.St.Q, act, in.Document)
	if err != nil {
		return nil, err
	}
	rs, err := dc.St.Q.ListRevisions(ctx, d.ID)
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

func (dc *Service) documentRevert(ctx context.Context, act registry.Actor, in documentRevertIn) (any, error) {
	d, err := dc.readableDocument(ctx, dc.St.Q, act, in.Document)
	if err != nil {
		return nil, err
	}
	r, err := docs.Revision(ctx, dc.St.Q, d, in.Revision)
	if err != nil {
		return nil, err
	}
	reason := docs.RevertReason(r.N, in.Reason)
	if d.Kind == docs.KindManifest {
		p, err := dc.St.Q.GetProject(ctx, d.ProjectID.Int64)
		if err != nil {
			return nil, err
		}
		return dc.SaveManifest(ctx, act, p, []byte(r.Text), reason, r.N, in.IfRevision,
			registry.Builtin+".document_revert", map[string]any{"document": d.ID, "revision": r.N, "reason": in.Reason})
	}
	var n int64
	err = dc.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		ref := docs.RefOf(d)
		if cur, err := q.GetDocument(ctx, d.ID); err != nil || cur.DeletedAt.Valid || in.IfRevision != 0 && cur.Current != in.IfRevision {
			switch {
			case err == nil && cur.DeletedAt.Valid:
				err = registry.NotFound("deck: no document %d", d.ID)
			case err == nil:
				err = registry.Conflict("deck: document %d is at revision %d now, not %d", d.ID, cur.Current, in.IfRevision)
			}
			return err
		}
		var changed bool
		if _, n, changed, err = docs.Save(ctx, q, ref, d.Kind, r.Text, act.Author(), reason); err != nil || !changed {
			return err
		}
		return dc.scriptChanged(ctx, q, act, d, n, reason)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"revision": n}, nil
}

// scriptChanged lists a script change made without the owner, undone by returning to the revision before n.
func (dc *Service) scriptChanged(ctx context.Context, q *db.Queries, act registry.Actor, d db.Document, n int64, reason string) error {
	if d.Kind != docs.KindScript || !d.ProjectID.Valid || d.RecordID.Valid {
		return nil
	}
	prev := db.Document{ID: d.ID, Current: n - 1}
	return dc.Changed(ctx, q, act, d.ProjectID.Int64, prev, n, fmt.Sprintf("%s changed script %s", act.Author(), d.Name), reason)
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
		registry.Def("document_revisions", "Document history",
			"Lists revisions of a document: who, when and why.",
			registry.Everyone, dc.documentRevisions),
		registry.Def("document_revert", "Revert document",
			"Makes an earlier revision current again as a new revision.",
			registry.OwnerSide, dc.documentRevert),
		registry.Def("programs_check", "Check programs",
			"Looks again for the programs the scripts of a project need, after the owner installed one, and updates the health of its commands.",
			registry.OwnerSide, dc.programsCheck),
	}
}

// MaxFile is the largest file deck takes into a document from a folder.
const MaxFile = 1 << 20

// Skills returns the files of the named skills by path under the skill folder; a project skill hides a
// workspace skill of the same name.
func (dc *Service) Skills(ctx context.Context, projectID int64, names []string) (map[string]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	p, err := dc.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return nil, err
	}
	rows, err := dc.St.Q.ListSkillFiles(ctx, db.ListSkillFilesParams{WorkspaceID: p.WorkspaceID, ProjectID: store.ID(p.ID)})
	if err != nil {
		return nil, err
	}
	has := map[string]bool{}
	for _, r := range rows {
		if s := docs.SkillOf(r.Name); s != "" && r.Name == docs.SkillsDir+s+"/SKILL.md" {
			has[fmt.Sprint(r.ProjectID, "/", s)] = true
		}
	}
	out := map[string]string{}
	for _, n := range names {
		from := p.ID
		if !has[fmt.Sprint(p.ID, "/", n)] {
			if from = 0; !has[fmt.Sprint(0, "/", n)] {
				return nil, registry.Invalid("deck: skill %q is missing: no skills/%s/SKILL.md in the project or the workspace", n, n)
			}
		}
		for _, r := range rows {
			if r.ProjectID == from && docs.SkillOf(r.Name) == n {
				out[strings.TrimPrefix(r.Name, docs.SkillsDir)] = r.Text
			}
		}
	}
	return out, nil
}

// SeedSkills stores the skills built into deck as skills of every workspace; a skill someone else edited stays theirs.
func (dc *Service) SeedSkills(ctx context.Context) error {
	ws, err := dc.St.Q.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	for _, w := range ws {
		if err := dc.seedSkills(ctx, w); err != nil {
			return err
		}
	}
	return nil
}

func (dc *Service) seedSkills(ctx context.Context, workspace int64) error {
	return fs.WalkDir(skills.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.Contains(path, "/") {
			return err
		}
		b, err := fs.ReadFile(skills.FS, path)
		if err != nil {
			return err
		}
		return dc.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			ref := docs.Ref{Scope: docs.Scope{Workspace: workspace}, Name: docs.SkillsDir + path}
			d, err := q.FindAnyDocument(ctx, db.FindAnyDocumentParams(ref.Find()))
			if err == nil {
				if d.DeletedAt.Valid {
					return nil
				}
				r, err := docs.Revision(ctx, q, d, 0)
				if err != nil || r.Author != "deck" || r.Text == string(b) {
					return err
				}
			}
			_, _, _, err = docs.Save(ctx, q, ref, docs.KindSkill, string(b), "deck", "built-in skill")
			return err
		})
	})
}

const sandboxHealth = "sandbox"

func commandHealth(project, command string) string { return "command:" + project + "." + command }

// PrepareLater records the health of the script commands of a project in the background.
func (dc *Service) PrepareLater(projectID int64) {
	dc.BG.Add(1)
	go func() {
		defer dc.BG.Done()
		if err := dc.Programs(dc.Ctx, projectID); err != nil && dc.Ctx.Err() == nil {
			slog.Error("prepare scripts", "project", projectID, "err", err)
		}
	}()
}

type programsCheckIn struct {
	Project string `type:"project" desc:"Project name"`
}

func (dc *Service) programsCheck(ctx context.Context, act registry.Actor, in programsCheckIn) (any, error) {
	p, err := access.Project(ctx, dc.St.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	if err := dc.Programs(ctx, p.ID); err != nil {
		return nil, err
	}
	return map[string]any{"project": p.Name, "checked": true}, nil
}

// Programs looks for the programs of the scripts of a project and writes the health of its script commands.
func (dc *Service) Programs(ctx context.Context, projectID int64) error {
	p, err := dc.St.Q.GetProject(ctx, projectID)
	if err != nil {
		return err
	}
	m, err := docs.Manifest(ctx, dc.St.Q, p)
	if err != nil {
		return err
	}
	problems := script.Check(m)
	sbErr := dc.Sandbox.Check()
	known, err := dc.St.Q.ListHealth(ctx, store.ID(p.WorkspaceID))
	if err != nil {
		return err
	}
	err = dc.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		status, text := "ok", ""
		if sbErr != nil {
			status, text = "error", dc.Sandbox.Name()+": "+sbErr.Error()
		}
		if err := health.Set(ctx, q, health.Of{}, sandboxHealth, status, text); err != nil {
			return err
		}
		keep := map[string]bool{}
		for _, c := range m.Commands {
			if c.Executor.Type != "script" {
				continue
			}
			subject := commandHealth(p.Name, c.Name)
			keep[subject] = true
			status, text := scriptHealth(&c, problems, sbErr)
			if err := health.Set(ctx, q, health.Project(p), subject, status, text); err != nil {
				return err
			}
		}
		for _, row := range known {
			if h := row.Health; h.ProjectID.Int64 == p.ID && strings.HasPrefix(h.Subject, commandHealth(p.Name, "")) && !keep[h.Subject] {
				if err := q.DeleteHealth(ctx, db.DeleteHealthParams{ProjectID: store.ID(p.ID), Subject: h.Subject}); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return err
}

func scriptHealth(c *manifest.Command, problems map[string]error, sbErr error) (string, string) {
	if sbErr != nil {
		return "error", "the script sandbox does not work: " + sbErr.Error()
	}
	if err := problems[c.Executor.Script]; err != nil {
		return "error", err.Error()
	}
	return "ok", ""
}
