package app

import (
	"context"
	"database/sql"
	"errors"

	"deck2/internal/app/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
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
	p, err := a.Store.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: name})
	if errors.Is(err, sql.ErrNoRows) {
		return db.Project{}, registry.NotFound("deck: no project %q", name)
	}
	if err != nil {
		return p, err
	}
	return p, inside(act, p.ID)
}

// projectOf is the project a row of projectID belongs to, when it is of the actor's workspace and the actor
// may touch it; a project of another workspace is missing as the row is.
func projectOf(ctx context.Context, q *db.Queries, act registry.Actor, projectID int64, missing error) (db.Project, error) {
	p, err := q.GetProject(ctx, projectID)
	if err != nil || p.WorkspaceID != act.Workspace {
		return p, missing
	}
	return p, inside(act, projectID)
}

func inside(act registry.Actor, projectID int64) error {
	if (act.Kind == registry.Agent || act.Kind == registry.Trigger) && projectID != act.ProjectID {
		return registry.Refuse("deck: this process cannot touch another project")
	}
	return nil
}

// docScope is where a document call works: a project, or the workspace for the owner side.
func (a *App) docScope(ctx context.Context, act registry.Actor, project string) (docs.Scope, error) {
	if project == "" && (act.Kind == registry.Owner || act.Kind == registry.Master) {
		return docs.Scope{Workspace: act.Workspace}, nil
	}
	p, err := a.scope(ctx, act, project)
	return docs.ProjectScope(p), err
}

type projectView struct {
	ID            int64        `json:"id"`
	Name          string       `json:"name"`
	Status        string       `json:"status"`
	Description   string       `json:"description"`
	Screens       []screenView `json:"screens"`
	ManifestError string       `json:"manifest_error,omitempty"`
	// Rights are what enabling a draft at Revision of its manifest grants.
	Rights   []string `json:"rights,omitempty"`
	Revision int64    `json:"revision"`
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
		v, _ := a.viewProject(ctx, p)
		out = append(out, v)
	}
	return out, nil
}

// viewProject is a project as the owner sees it, with its manifest when that loads.
func (a *App) viewProject(ctx context.Context, p db.Project) (projectView, *manifest.Manifest) {
	v := projectView{ID: p.ID, Name: p.Name, Status: p.Status, Screens: []screenView{}}
	if prompt, err := docs.CurrentText(ctx, a.Store.Q, docs.ProjectScope(p), docs.KindPrompt, docs.PromptName); err == nil {
		v.Description = docs.Description(prompt)
	}
	m, err := loadManifest(ctx, a.Store.Q, p)
	if err != nil {
		v.ManifestError = err.Error()
		return v, nil
	}
	v.Revision = m.Revision
	for _, s := range m.Screens {
		v.Screens = append(v.Screens, screenView{Name: s.Name, Title: s.Title})
	}
	if p.Status == store.ProjectDraft {
		v.Rights = rights(m)
	}
	return v, m
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
	ref := docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName}
	d, err := a.Store.Q.FindDocument(ctx, ref.Find())
	if err != nil {
		return nil, err
	}
	r, err := docs.Revision(ctx, a.Store.Q, d, 0)
	if err != nil {
		return nil, err
	}
	return map[string]any{"project": p.Name, "document": d.ID, "revision": d.Current, "manifest": r.Text}, nil
}

func (a *App) manifestSave(ctx context.Context, act registry.Actor, in struct {
	Project, Manifest, Reason string
	IfRevision                int64 `json:"if_revision"`
}) (any, error) {
	p, err := a.scope(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	return a.SaveManifest(ctx, act, p, []byte(in.Manifest), in.Reason, 0, in.IfRevision,
		Call{Command: registry.Builtin + ".manifest_save", Input: map[string]any{"project": p.Name, "manifest": in.Manifest, "reason": in.Reason}})
}
