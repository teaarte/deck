package app

import (
	"cmp"
	"context"
	"deck2/internal/app/access"
	"fmt"
	jsonpatch "github.com/evanphx/json-patch/v5"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

type projectView struct {
	ID            int64        `json:"id"`
	Name          string       `json:"name"`
	Status        string       `json:"status"`
	Paused        bool         `json:"paused"`
	Builtin       bool         `json:"builtin"`
	Description   string       `json:"description"`
	Screens       []screenView `json:"screens"`
	ManifestError string       `json:"manifest_error"`
	// Rights are what enabling a draft at Revision of its manifest grants.
	Rights   []string     `json:"rights"`
	Revision int64        `json:"revision"`
	Folders  []folderView `json:"folders"`
	// Autonomy and NoNetwork are of the manifest; empty and false when it does not load.
	Autonomy  string `json:"autonomy"`
	NoNetwork bool   `json:"no_network"`
	// Icon and Title are the icon and the name the owner sees that its manifest gives; Title is the name without one.
	Icon  string `json:"icon,omitempty"`
	Title string `json:"title"`
}

type screenView struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

type projectsListIn struct {
	Project string `type:"project" opt:"" desc:"Only this project"`
}

func (a *App) projectsList(ctx context.Context, act registry.Actor, in projectsListIn) ([]projectView, error) {
	ps, err := a.Store.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	if in.Project != "" {
		ps = slices.DeleteFunc(ps, func(p db.Project) bool { return p.Name != in.Project })
	}
	ms, failed := a.Manifests.All(ctx, ps)
	out := make([]projectView, 0, len(ps))
	for _, p := range ps {
		out = append(out, a.viewProject(ctx, p, ms[p.ID], failed[p.ID]))
	}
	return out, nil
}

// viewProject is a project as the owner sees it, with its manifest when that loads.
func (a *App) viewProject(ctx context.Context, p db.Project, m *manifest.Manifest, err error) projectView {
	v := projectView{ID: p.ID, Name: p.Name, Title: p.Name, Status: p.Status, Paused: p.Paused == 1, Builtin: p.Builtin == 1, Screens: []screenView{},
		Rights: []string{}, Folders: a.folders(ctx, p.ID)}
	if prompt, err := docs.CurrentText(ctx, a.Store.Q, docs.ProjectScope(p), docs.KindPrompt, docs.PromptName); err == nil {
		v.Description = docs.Description(prompt)
	}
	if err != nil {
		v.ManifestError = err.Error()
		return v
	}
	v.Revision, v.Autonomy, v.NoNetwork, v.Icon, v.Title = m.Revision, cmp.Or(m.Autonomy, "suggest"), m.NoNetwork, m.Icon, cmp.Or(m.Title, p.Name)
	for _, s := range m.Screens {
		v.Screens = append(v.Screens, screenView{Name: s.Name, Title: s.Title})
	}
	if p.Status == store.ProjectDraft {
		v.Rights = append(v.Rights, rights(m)...)
	}
	return v
}

// baseManifest is a project with only the base board and its screen.
const baseManifest = `{"version": %d,
  "board": {"lanes": [{"name": "backlog", "title": "Backlog", "role": "inbox"}, {"name": "ready", "title": "Ready", "role": "ready"},
    {"name": "running", "title": "Running", "role": "running"}, {"name": "review", "title": "Review", "role": "review"},
    {"name": "done", "title": "Done", "role": "done"}]},
  "screens": [{"name": "board", "title": "Board", "blocks": [{"type": "board", "title": "Board"}]}],
  "menu": ["board"]}`

type projectCreateIn struct {
	Name     string `desc:"Project name: lowercase letters, digits and '-'"`
	Manifest string `type:"json" opt:"" desc:"Manifest JSON; a project with only the base board when omitted"`
	Prompt   string `type:"text" opt:"" desc:"Project md prompt"`
}

func (a *App) projectCreate(ctx context.Context, act registry.Actor, in projectCreateIn) (any, error) {
	if strings.TrimSpace(in.Manifest) == "" {
		in.Manifest = fmt.Sprintf(baseManifest, manifest.FormatVersion)
	}
	p, err := a.CreateProject(ctx, act, in.Name, []byte(in.Manifest), in.Prompt)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": p.ID, "name": p.Name}, nil
}

type manifestGetIn struct {
	Project string `type:"project" opt:"" desc:"Project name; defaults to the caller's own project"`
}

func (a *App) manifestGet(ctx context.Context, act registry.Actor, in manifestGetIn) (any, error) {
	p, err := access.Project(ctx, a.Store.Q, act, in.Project)
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

type manifestSaveIn struct {
	Project    string   `type:"project" desc:"Project name"`
	Manifest   string   `type:"json" opt:"" desc:"Manifest JSON; leave it out to change parts of the current manifest with old and new"`
	Old        []string `type:"text[]" opt:"" desc:"Parts of the current manifest text to replace, each occurring in it exactly once"`
	New        []string `type:"text[]" opt:"" desc:"The text that replaces each old part, in the same order"`
	Patch      string   `type:"json" opt:"" desc:"JSON Patch (RFC 6902) of the current manifest, such as [{\"op\": \"replace\", \"path\": \"/screens/0/title\", \"value\": \"Overview\"}]: paths point at one place, so equal texts elsewhere stay"`
	Reason     string   `opt:"" desc:"Why it changed"`
	IfRevision int64    `json:"if_revision" opt:"" desc:"Only while the current revision is still this one"`
}

func (a *App) manifestSave(ctx context.Context, act registry.Actor, in manifestSaveIn) (any, error) {
	p, err := access.Project(ctx, a.Store.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	if len(in.Old) > 0 || len(in.New) > 0 || strings.TrimSpace(in.Patch) != "" {
		if in.Manifest != "" || (strings.TrimSpace(in.Patch) != "" && (len(in.Old) > 0 || len(in.New) > 0)) {
			return nil, registry.Invalid("deck: give the whole manifest, old and new parts, or a patch; one of them")
		}
		d, err := a.Store.Q.FindDocument(ctx, docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName}.Find())
		if err != nil {
			return nil, err
		}
		if in.IfRevision != 0 && d.Current != in.IfRevision {
			return nil, registry.Conflict("deck: the manifest of %s is at revision %d now, not %d", p.Name, d.Current, in.IfRevision)
		}
		r, err := docs.Revision(ctx, a.Store.Q, d, 0)
		if err != nil {
			return nil, err
		}
		if strings.TrimSpace(in.Patch) != "" {
			patch, err := jsonpatch.DecodePatch([]byte(in.Patch))
			if err != nil {
				return nil, registry.Invalid("deck: patch: %v", err)
			}
			out, err := patch.ApplyIndent([]byte(r.Text), "  ")
			if err != nil {
				return nil, registry.Invalid("deck: patch: %v", err)
			}
			in.Manifest = string(out)
		} else if in.Manifest, err = docs.Edit(r.Text, in.Old, in.New); err != nil {
			return nil, err
		}
		in.IfRevision = d.Current
	}
	if strings.TrimSpace(in.Manifest) == "" {
		return nil, registry.Invalid("deck: give the manifest JSON, or old and new parts of the current one")
	}
	return a.SaveManifest(ctx, act, p, []byte(in.Manifest), in.Reason, 0, in.IfRevision,
		Call{Command: registry.Builtin + ".manifest_save", Input: map[string]any{"project": p.Name, "manifest": in.Manifest, "reason": in.Reason}})
}

func (a *App) projectsCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("projects_list", "List projects",
			"Lists projects with their description (the first line of the md prompt), screens, folders and what enabling a draft grants.",
			registry.OwnerSide, a.projectsList).On("projects"),
		registry.Def("project_create", "Create project",
			"Creates a project from its name, an md prompt and a manifest; without a manifest it has only the base board.",
			registry.OwnerSide, a.projectCreate),
		registry.Def("manifest_get", "Get manifest",
			"Returns the current manifest of a project.",
			registry.Everyone, a.manifestGet),
		registry.Def("manifest_save", "Save manifest",
			"Validates a manifest and stores it as a new revision: the whole manifest, or old parts of the current text replaced with new ones.",
			registry.OwnerSide, a.manifestSave),
	}
}
