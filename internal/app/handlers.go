package app

import (
	"context"
	"fmt"
	"strings"

	"deck2/internal/app/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

type projectView struct {
	ID            int64        `json:"id"`
	Name          string       `json:"name"`
	Status        string       `json:"status"`
	Description   string       `json:"description"`
	Screens       []screenView `json:"screens"`
	ManifestError string       `json:"manifest_error,omitempty"`
	// Rights are what enabling a draft at Revision of its manifest grants.
	Rights   []string     `json:"rights,omitempty"`
	Revision int64        `json:"revision"`
	Folders  []folderView `json:"folders"`
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
	v := projectView{ID: p.ID, Name: p.Name, Status: p.Status, Screens: []screenView{}, Folders: a.folders(ctx, p.ID)}
	if prompt, err := docs.CurrentText(ctx, a.Store.Q, docs.ProjectScope(p), docs.KindPrompt, docs.PromptName); err == nil {
		v.Description = docs.Description(prompt)
	}
	m, err := docs.Manifest(ctx, a.Store.Q, p)
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

// baseManifest is a project with only the base board and its screen.
const baseManifest = `{"version": %d,
  "board": {"lanes": [{"name": "backlog", "title": "Backlog", "role": "inbox"}, {"name": "ready", "title": "Ready", "role": "ready"},
    {"name": "running", "title": "Running", "role": "running"}, {"name": "review", "title": "Review", "role": "review"},
    {"name": "done", "title": "Done", "role": "done"}]},
  "screens": [{"name": "board", "title": "Board", "blocks": [{"type": "board", "title": "Board"}]}],
  "menu": ["board"]}`

func (a *App) projectCreate(ctx context.Context, act registry.Actor, in struct{ Name, Manifest, Prompt string }) (any, error) {
	if strings.TrimSpace(in.Manifest) == "" {
		in.Manifest = fmt.Sprintf(baseManifest, manifest.FormatVersion)
	}
	p, err := a.CreateProject(ctx, act, in.Name, []byte(in.Manifest), in.Prompt)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": p.ID, "name": p.Name}, nil
}

func (a *App) manifestGet(ctx context.Context, act registry.Actor, in struct{ Project string }) (any, error) {
	p, err := a.project(ctx, act, in.Project)
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
	p, err := a.project(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	return a.SaveManifest(ctx, act, p, []byte(in.Manifest), in.Reason, 0, in.IfRevision,
		Call{Command: registry.Builtin + ".manifest_save", Input: map[string]any{"project": p.Name, "manifest": in.Manifest, "reason": in.Reason}})
}
