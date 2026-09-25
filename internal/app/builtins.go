package app

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"deck2/internal/ask"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/schemas"
)

var (
	everyone   = []string{registry.Owner, registry.Master, registry.Agent}
	ownerSide  = []string{registry.Owner, registry.Master}
	ownerOnly  = []string{registry.Owner}
	projectOpt = registry.Opt("project", "project", "Project name; defaults to the caller's own project")
)

func (a *App) addBuiltins() {
	r := a.Registry
	add := func(name, title, desc string, callers []string, in json.RawMessage, h registry.Handler) {
		r.Add(&registry.Command{Name: registry.Builtin + "." + name, Title: title, Description: desc,
			Class: registry.Internal, Callers: callers, Input: in, Handler: h})
	}

	add("projects_list", "List projects", "Lists projects with their description and screens.", ownerSide,
		registry.Params(), registry.Typed(a.projectsList))
	add("project_create", "Create project", "Creates a project from a manifest and an optional md prompt.", ownerSide,
		registry.Params(registry.P("name", "string", "Project name: lowercase letters, digits and '-'"),
			registry.P("manifest", "text", "Manifest JSON"), registry.Opt("prompt", "text", "Project md prompt")),
		registry.Typed(a.projectCreate))
	add("manifest_get", "Get manifest", "Returns the current manifest of a project.", everyone,
		registry.Params(projectOpt), registry.Typed(a.manifestGet))
	add("manifest_format", "Manifest format", "Returns the JSON Schema of project manifests.", everyone,
		registry.Params(), registry.Typed(func(context.Context, registry.Actor, struct{}) (any, error) {
			return json.RawMessage(schemas.Manifest), nil
		}))
	add("manifest_save", "Save manifest", "Validates a manifest and stores it as a new revision.", ownerSide,
		registry.Params(registry.P("project", "project", "Project name"), registry.P("manifest", "text", "Manifest JSON"),
			registry.Opt("reason", "string", "Why it changed")),
		registry.Typed(a.manifestSave))

	add("documents_list", "List documents", "Lists documents of a project, or workspace documents when no project is given.", everyone,
		registry.Params(projectOpt, registry.Param{Name: "kind", Type: "enum", Desc: "Only this kind", Enum: DocumentKinds, Optional: true}),
		registry.Typed(a.documentsList))
	add("document_get", "Get document", "Returns a document by id, or by project and name; the current revision unless one is given.", everyone,
		registry.Params(registry.Opt("document", "document", "Document id"), projectOpt,
			registry.Opt("name", "string", "Document name (path)"), registry.Opt("revision", "integer", "Revision number")),
		registry.Typed(a.documentGet))
	add("document_save", "Save document", "Writes a new revision of a document, creating it on first save.", everyone,
		registry.Params(registry.P("name", "string", "Document name (path), e.g. notes/criteria.md"),
			registry.Param{Name: "kind", Type: "enum", Desc: "Document kind", Enum: DocumentKinds},
			registry.P("text", "text", "Full new text"), projectOpt,
			registry.Opt("record", "record", "Record the document belongs to"), registry.Opt("reason", "string", "Why it changed")),
		registry.Typed(a.documentSave))
	add("document_revisions", "Document history", "Lists revisions of a document: who, when and why.", everyone,
		registry.Params(registry.P("document", "document", "Document id")), registry.Typed(a.documentRevisions))
	add("document_revert", "Revert document", "Makes an earlier revision current again as a new revision.", ownerSide,
		registry.Params(registry.P("document", "document", "Document id"), registry.P("revision", "integer", "Revision to return to"),
			registry.Opt("reason", "string", "Why")),
		registry.Typed(a.documentRevert))

	add("records_list", "List records", "Lists records of a collection, newest last.", everyone,
		registry.Params(projectOpt, registry.P("collection", "string", "Collection name"), registry.Opt("limit", "integer", "At most this many")),
		registry.Typed(a.recordsList))
	add("records_upsert", "Write records", "Writes records into a collection; records with an existing key are updated.", everyone,
		registry.Params(projectOpt, registry.P("collection", "string", "Collection name"),
			registry.P("records", "text", "Records as JSONL: one JSON object per line")),
		registry.Typed(a.recordsUpsert))
	add("record_update", "Edit record", "Replaces the data of one record; key fields cannot change.", everyone,
		registry.Params(registry.P("record", "record", "Record id"), registry.P("data", "text", "New record JSON")),
		registry.Typed(a.recordUpdate))

	r.Add(&registry.Command{Name: registry.Builtin + ".collection_delete", Title: "Delete collection records",
		Description: "Deletes every record of a collection, so the collection can be removed from the manifest.",
		Class:       registry.External, Callers: ownerOnly,
		Input:   registry.Params(registry.P("project", "project", "Project name"), registry.P("collection", "string", "Collection name")),
		Handler: registry.Typed(a.collectionDelete)})

	add("processes_list", "List processes", "Lists recent processes, newest first.", everyone,
		registry.Params(projectOpt, registry.Opt("state", "string", "Only this state"), registry.Opt("limit", "integer", "At most this many")),
		registry.Typed(a.processesList))
	add("process_get", "Process status", "Returns the state and result of a process.", everyone,
		registry.Params(registry.P("process", "process", "Process id")), registry.Typed(a.processGet))

	add("asks_list", "Waiting for you", "Lists open questions and confirmations for the owner.", ownerSide,
		registry.Params(), registry.Typed(a.asksList))
	add("ask_answer", "Answer", "Answers a question or confirmation: text, allow, deny or deny:<reason>.", ownerOnly,
		registry.Params(registry.P("ask", "ask", "Ask id"), registry.P("answer", "text", "The answer")),
		registry.Typed(a.askAnswer))
}

// scope resolves the project a call is about; agents and triggers stay inside their own project.
func (a *App) scope(ctx context.Context, act registry.Actor, name string) (db.Project, error) {
	if name == "" {
		if act.ProjectID == 0 {
			return db.Project{}, registry.Invalid("deck: project is required")
		}
		return a.Store.Q.GetProject(ctx, act.ProjectID)
	}
	p, err := a.Store.Q.GetProjectByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) {
		return p, registry.NotFound("deck: no project %q", name)
	}
	if err != nil {
		return p, err
	}
	return p, a.inside(act, p.ID)
}

func (a *App) inside(act registry.Actor, projectID int64) error {
	if (act.Kind == registry.Agent || act.Kind == registry.Trigger) && projectID != act.ProjectID {
		return registry.Refuse("deck: this process cannot touch another project")
	}
	return nil
}

type projectView struct {
	ID          int64        `json:"id"`
	Name        string       `json:"name"`
	Status      string       `json:"status"`
	Description string       `json:"description"`
	Screens     []screenView `json:"screens"`

	ManifestError string `json:"manifest_error,omitempty"`
}

type screenView struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

func (a *App) projectsList(ctx context.Context, _ registry.Actor, _ struct{}) (any, error) {
	ps, err := a.Store.Q.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]projectView, 0, len(ps))
	for _, p := range ps {
		v := projectView{ID: p.ID, Name: p.Name, Status: p.Status, Screens: []screenView{}}
		if m, err := a.Manifest(ctx, p.ID); err != nil {
			v.ManifestError = err.Error()
		} else {
			v.Description = m.Description
			for _, s := range m.Screens {
				v.Screens = append(v.Screens, screenView{Name: s.Name, Title: s.Title})
			}
		}
		out = append(out, v)
	}
	return out, nil
}

func (a *App) projectCreate(ctx context.Context, act registry.Actor, in struct{ Name, Manifest, Prompt string }) (any, error) {
	p, err := a.CreateProject(ctx, act, in.Name, []byte(in.Manifest), in.Prompt)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": p.ID, "name": p.Name}, nil
}

func (a *App) manifestGet(ctx context.Context, act registry.Actor, in struct{ Project string }) (any, error) {
	p, err := a.scope(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	d, err := a.Store.Q.FindDocument(ctx, db.FindDocumentParams{WorkspaceID: 1, ProjectID: p.ID, Name: ManifestName})
	if err != nil {
		return nil, err
	}
	text, err := a.DocumentText(ctx, p.ID, KindManifest, ManifestName)
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": p.Name, "document": d.ID, "revision": d.Current, "manifest": text}, nil
}

func (a *App) manifestSave(ctx context.Context, act registry.Actor, in struct{ Project, Manifest, Reason string }) (any, error) {
	p, err := a.scope(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	n, err := a.SaveManifest(ctx, act, p, []byte(in.Manifest), in.Reason)
	if err != nil {
		return nil, err
	}
	return map[string]any{"revision": n}, nil
}

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

// docProject is the project a document call works in: 0 is the workspace, open to the owner side only.
func (a *App) docProject(ctx context.Context, act registry.Actor, name string) (int64, error) {
	if name == "" && (act.Kind == registry.Owner || act.Kind == registry.Master) {
		return 0, nil
	}
	p, err := a.scope(ctx, act, name)
	return p.ID, err
}

func (a *App) documentsList(ctx context.Context, act registry.Actor, in struct{ Project, Kind string }) (any, error) {
	pid, err := a.docProject(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	ds, err := a.Store.Q.ListDocuments(ctx, db.ListDocumentsParams{WorkspaceID: 1, ProjectID: pid, Kind: in.Kind})
	if err != nil {
		return nil, err
	}
	out := make([]documentView, 0, len(ds))
	for _, d := range ds {
		out = append(out, docView(d))
	}
	return out, nil
}

// readableDocument loads a document the actor may read: its own project's or the workspace's.
func (a *App) readableDocument(ctx context.Context, act registry.Actor, id int64) (db.Document, error) {
	d, err := a.document(ctx, id)
	if err != nil {
		return d, err
	}
	if d.ProjectID == 0 {
		return d, nil
	}
	return d, a.inside(act, d.ProjectID)
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
		d, err = a.readableDocument(ctx, act, in.Document)
	case in.Name != "":
		var pid int64
		if pid, err = a.docProject(ctx, act, in.Project); err == nil {
			d, err = a.Store.Q.FindDocument(ctx, db.FindDocumentParams{WorkspaceID: 1, ProjectID: pid, Name: in.Name})
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
	r, err := a.revision(ctx, d, in.Revision)
	if err != nil {
		return nil, err
	}
	v := docView(d)
	v.Revision, v.Text = r.N, r.Text
	return v, nil
}

func (a *App) documentSave(ctx context.Context, act registry.Actor, in struct {
	Name, Kind, Text, Project, Reason string
	Record                            int64
}) (any, error) {
	if in.Kind == KindManifest {
		return nil, registry.Invalid("deck: save manifests with deck.manifest_save")
	}
	if act.Kind == registry.Agent && in.Kind != KindArtifact {
		return nil, registry.Refuse("deck: agents write only artifacts")
	}
	pid, err := a.docProject(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	ref := DocRef{ProjectID: pid, Name: in.Name}
	if in.Record != 0 {
		rec, err := a.Store.Q.GetRecord(ctx, in.Record)
		if errors.Is(err, sql.ErrNoRows) || err == nil && rec.ProjectID != pid {
			return nil, registry.NotFound("deck: no record %d in this project", in.Record)
		}
		if err != nil {
			return nil, err
		}
		ref.OwnerKind, ref.OwnerID = "record", rec.ID
	}
	var d db.Document
	err = a.Store.Tx(ctx, func(q *db.Queries) error {
		d, _, err = a.SaveDocument(ctx, q, ref, in.Kind, in.Text, act.Author(), in.Reason)
		return err
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("documents")
	return docView(d), nil
}

func (a *App) documentRevisions(ctx context.Context, act registry.Actor, in struct{ Document int64 }) (any, error) {
	d, err := a.readableDocument(ctx, act, in.Document)
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
	d, err := a.readableDocument(ctx, act, in.Document)
	if err != nil {
		return nil, err
	}
	r, err := a.revision(ctx, d, in.Revision)
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
	err = a.Store.Tx(ctx, func(q *db.Queries) error {
		_, n, err = a.SaveDocument(ctx, q, DocRef{ProjectID: d.ProjectID, OwnerKind: d.OwnerKind, OwnerID: d.OwnerID, Name: d.Name}, d.Kind, r.Text, act.Author(), reason)
		return err
	})
	if err != nil {
		return nil, err
	}
	a.Bus.Publish("documents")
	return map[string]any{"revision": n}, nil
}

func (a *App) recordsList(ctx context.Context, act registry.Actor, in struct {
	Project, Collection string
	Limit               int
}) (any, error) {
	p, err := a.scope(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	m, err := a.Manifest(ctx, p.ID)
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

func (a *App) recordsUpsert(ctx context.Context, act registry.Actor, in struct{ Project, Collection, Records string }) (any, error) {
	p, err := a.scope(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	var out UpsertResult
	err = a.withRecords(ctx, p.ID, func(m *manifest.Manifest) error {
		out, err = a.UpsertJSONL(ctx, p.ID, m, in.Collection, in.Records)
		return err
	})
	return out, err
}

func (a *App) recordUpdate(ctx context.Context, act registry.Actor, in struct {
	Record int64
	Data   string
}) (any, error) {
	rec, err := a.Store.Q.GetRecord(ctx, in.Record)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, registry.NotFound("deck: no record %d", in.Record)
	}
	if err != nil {
		return nil, err
	}
	if err := a.inside(act, rec.ProjectID); err != nil {
		return nil, err
	}
	data, err := manifest.DecodeObject([]byte(in.Data))
	if err != nil {
		return nil, registry.Invalid("deck: data is not a JSON object: %v", err)
	}
	err = a.withRecords(ctx, rec.ProjectID, func(m *manifest.Manifest) error {
		return a.UpdateRecord(ctx, rec, m, data)
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}

type processView struct {
	ID          int64           `json:"id"`
	Project     int64           `json:"project"`
	Command     string          `json:"command"`
	State       string          `json:"state"`
	Result      json.RawMessage `json:"result,omitempty"`
	NotBySchema bool            `json:"not_by_schema"`
	Error       string          `json:"error,omitempty"`
	StartedAt   string          `json:"started_at"`
	FinishedAt  string          `json:"finished_at,omitempty"`
}

func procView(p db.Process) processView {
	v := processView{ID: p.ID, Project: p.ProjectID, Command: p.Command, State: p.State, NotBySchema: p.NotBySchema == 1,
		Error: p.Error, StartedAt: p.StartedAt, FinishedAt: p.FinishedAt.String}
	if p.Result != "" {
		if json.Valid([]byte(p.Result)) {
			v.Result = json.RawMessage(p.Result)
		} else {
			v.Result, _ = json.Marshal(p.Result)
		}
	}
	return v
}

func (a *App) processesList(ctx context.Context, act registry.Actor, in struct {
	Project, State string
	Limit          int64
}) (any, error) {
	var pid int64
	if in.Project != "" || act.Kind == registry.Agent {
		p, err := a.scope(ctx, act, in.Project)
		if err != nil {
			return nil, err
		}
		pid = p.ID
	}
	if in.Limit <= 0 || in.Limit > 500 {
		in.Limit = 50
	}
	ps, err := a.Store.Q.ListProcessesFiltered(ctx, db.ListProcessesFilteredParams{ProjectID: pid, State: in.State, Lim: in.Limit})
	if err != nil {
		return nil, err
	}
	out := make([]processView, 0, len(ps))
	for _, p := range ps {
		out = append(out, procView(p))
	}
	return out, nil
}

func (a *App) processGet(ctx context.Context, act registry.Actor, in struct{ Process int64 }) (any, error) {
	p, err := a.Store.Q.GetProcess(ctx, in.Process)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, registry.NotFound("deck: no process %d", in.Process)
	}
	if err != nil {
		return nil, err
	}
	if err := a.inside(act, p.ProjectID); err != nil {
		return nil, err
	}
	return procView(p), nil
}

func (a *App) asksList(ctx context.Context, _ registry.Actor, _ struct{}) (any, error) {
	as, err := a.Store.Q.ListOpenAsks(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(as))
	for _, q := range as {
		out = append(out, map[string]any{"id": q.ID, "process": q.ProcessID, "kind": q.Kind, "text": q.Text,
			"payload": json.RawMessage(q.Payload), "created_at": q.CreatedAt})
	}
	return out, nil
}

func (a *App) askAnswer(ctx context.Context, _ registry.Actor, in struct {
	Ask    int64
	Answer string
}) (any, error) {
	if err := a.Asks.Answer(ctx, in.Ask, in.Answer); err != nil {
		if errors.Is(err, ask.ErrNotOpen) {
			return nil, &registry.Error{Status: 409, Text: "deck: this ask is already answered or expired"}
		}
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
