package app

import (
	"context"
	"database/sql"
	"errors"

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
