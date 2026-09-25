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
)

// scope resolves the project a call is about; agents and triggers stay inside their own project.
func (a *App) scope(ctx context.Context, act registry.Actor, name string) (db.Project, error) {
	if name == "" {
		if act.ProjectID == 0 {
			return db.Project{}, registry.Invalid("deck: project is required")
		}
		return a.Store.Q.GetProject(ctx, act.ProjectID)
	}
	p, err := a.Store.Q.GetProjectByName(ctx, name)
	if errors.Is(err, sql.ErrNoRows) || err == nil && p.WorkspaceID != act.Workspace {
		return db.Project{}, registry.NotFound("deck: no project %q", name)
	}
	if err != nil {
		return p, err
	}
	return p, inside(act, p.ID)
}

func inside(act registry.Actor, projectID int64) error {
	if (act.Kind == registry.Agent || act.Kind == registry.Trigger) && projectID != act.ProjectID {
		return registry.Refuse("deck: this process cannot touch another project")
	}
	return nil
}

// docScope is where a document call works: a project, or the workspace for the owner side.
func (a *App) docScope(ctx context.Context, act registry.Actor, project string) (Scope, error) {
	if project == "" && (act.Kind == registry.Owner || act.Kind == registry.Master) {
		return Scope{Workspace: act.Workspace}, nil
	}
	p, err := a.scope(ctx, act, project)
	return projectScope(p), err
}

type projectView struct {
	ID            int64        `json:"id"`
	Name          string       `json:"name"`
	Status        string       `json:"status"`
	Description   string       `json:"description"`
	Screens       []screenView `json:"screens"`
	ManifestError string       `json:"manifest_error,omitempty"`
}

type screenView struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

func (a *App) projectsList(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
	ps, err := a.Store.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	out := make([]projectView, 0, len(ps))
	for _, p := range ps {
		v := projectView{ID: p.ID, Name: p.Name, Status: p.Status, Screens: []screenView{}}
		if prompt, err := currentText(ctx, a.Store.Q, projectScope(p), KindPrompt, PromptName); err == nil {
			v.Description = description(prompt)
		}
		if m, err := loadManifest(ctx, a.Store.Q, p); err != nil {
			v.ManifestError = err.Error()
		} else {
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
	ref := DocRef{Scope: projectScope(p), Name: ManifestName}
	d, err := a.Store.Q.FindDocument(ctx, ref.find())
	if err != nil {
		return nil, err
	}
	r, err := getRevision(ctx, a.Store.Q, d, 0)
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": p.Name, "document": d.ID, "revision": d.Current, "manifest": r.Text}, nil
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

func (a *App) recordsList(ctx context.Context, act registry.Actor, in struct {
	Project, Collection string
	Limit               int
}) (any, error) {
	p, err := a.scope(ctx, act, in.Project)
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
		return UpdateRecord(ctx, q, rec, m, data)
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
	ps, err := a.Store.Q.ListProcessesFiltered(ctx, db.ListProcessesFilteredParams{WorkspaceID: act.Workspace, ProjectID: pid, State: in.State, Lim: in.Limit})
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
	if proj, err := a.Store.Q.GetProject(ctx, p.ProjectID); err != nil || proj.WorkspaceID != act.Workspace {
		return nil, registry.NotFound("deck: no process %d", in.Process)
	}
	if err := inside(act, p.ProjectID); err != nil {
		return nil, err
	}
	return procView(p), nil
}

func (a *App) asksList(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
	as, err := a.Store.Q.ListWorkspaceOpenAsks(ctx, act.Workspace)
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

func (a *App) askAnswer(ctx context.Context, act registry.Actor, in struct {
	Ask    int64
	Answer string
}) (any, error) {
	if ws, err := a.Store.Q.GetAskWorkspace(ctx, in.Ask); err != nil || ws != act.Workspace {
		return nil, registry.NotFound("deck: no ask %d", in.Ask)
	}
	if err := a.Asks.Answer(ctx, in.Ask, in.Answer); err != nil {
		if errors.Is(err, ask.ErrNotOpen) {
			return nil, &registry.Error{Status: 409, Text: "deck: this ask is already answered or expired"}
		}
		return nil, err
	}
	return map[string]any{"ok": true}, nil
}
