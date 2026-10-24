package project

import (
	"context"
	"deck2/internal/app/folders"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"fmt"
	"maps"
	"slices"
	"strings"
)

type projectView struct {
	ID            int64                 `json:"id"`
	Name          string                `json:"name"`
	Status        string                `json:"status"`
	Paused        bool                  `json:"paused"`
	Builtin       bool                  `json:"builtin"`
	Description   string                `json:"description"`
	Screens       []manifest.ScreenLink `json:"screens"`
	ManifestError string                `json:"manifest_error"`
	// Rights are what enabling a draft at Revision of its manifest grants.
	Rights []string `json:"rights"`
	// Asks are the tools and commands of a draft that will wait for the owner on every call once it is enabled.
	Asks []string `json:"asks"`
	// Models are what the manifest in force lets the project call: connections, models and @kinds.
	Models   []string      `json:"models"`
	Revision int64         `json:"revision"`
	Folders  []folders.Row `json:"folders"`
	// NoNetwork is of the manifest; false when it does not load.
	NoNetwork bool `json:"no_network"`
	// Network is what its agents reach beyond its domains, in words; empty when nothing.
	Network string `json:"network"`
	// Icon and Title are the icon and the name the owner sees that its manifest gives; Title is the name without one.
	Icon  string `json:"icon,omitempty"`
	Title string `json:"title"`
	// Meta is one line of its state, folders and what enabling a draft grants, in the language asked for.
	Meta string `json:"meta"`
}

type projectsListIn struct {
	Project string `type:"project" opt:"" desc:"Only this project"`
	Lang    string `opt:"" desc:"Language of the titles and words, such as ru; English without one"`
}

func (pj *Service) projectsList(ctx context.Context, act registry.Actor, in projectsListIn) ([]projectView, error) {
	ps, err := pj.St.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	if in.Project != "" {
		ps = slices.DeleteFunc(ps, func(p db.Project) bool { return p.Name != in.Project })
	}
	ms, failed := docs.All(ctx, pj.St.Q, ps)
	out := make([]projectView, 0, len(ps))
	for _, p := range ps {
		out = append(out, pj.viewProject(ctx, p, ms[p.ID], failed[p.ID], in.Lang))
	}
	return out, nil
}

// viewProject is a project as the owner sees it, with its manifest when that loads.
func (pj *Service) viewProject(ctx context.Context, p db.Project, m *manifest.Manifest, err error, lang string) projectView {
	v := projectView{ID: p.ID, Name: p.Name, Title: p.Name, Status: p.Status, Paused: p.Paused == 1, Builtin: p.Builtin == 1, Screens: []manifest.ScreenLink{},
		Rights: []string{}, Asks: []string{}, Models: []string{}}
	v.Folders, _ = pj.Folders(ctx, p.ID)
	if prompt, err := docs.CurrentText(ctx, pj.St.Q, docs.ProjectScope(p), docs.KindPrompt, docs.PromptName); err == nil {
		v.Description = docs.Description(prompt)
	}
	ws := maps.Clone(pj.Words(lang))
	if err != nil {
		v.ManifestError = err.Error()
		v.Meta = pj.meta(ws, v)
		return v
	}
	if lang != "" {
		if ws == nil {
			ws = map[string]string{}
		}
		maps.Copy(ws, m.I18n[lang])
	}
	v.Revision, v.NoNetwork, v.Network = m.Revision, m.NoNetwork, manifest.AgentNetwork(m)
	v.Title, v.Icon, v.Screens = manifest.Nav(p.Name, m, ws)
	if p.Status == store.ProjectDraft {
		v.Rights, v.Asks = append(v.Rights, rights(m)...), append(v.Asks, m.AskEachTime()...)
	}
	if f, err := docs.For(ctx, pj.St.Q, registry.Actor{}, p.ID); err == nil {
		v.Models = append(v.Models, f.Uses()...)
	}
	v.Meta = pj.meta(ws, v)
	return v
}

// meta is the line of a project under its title: its state, its folders and what enabling a draft grants and asks.
func (pj *Service) meta(ws map[string]string, v projectView) string {
	say := func(phrase string, a ...any) string { return fmt.Sprintf(manifest.Tr(ws, phrase), a...) }
	parts := []string{manifest.Tr(ws, v.Status)}
	if v.Paused {
		parts[0] = say("%s, paused", parts[0])
	}
	if len(v.Folders) > 0 {
		names := make([]string, 0, len(v.Folders))
		for _, f := range v.Folders {
			names = append(names, f.Name)
		}
		parts = append(parts, say("folders: %s", strings.Join(names, ", ")))
	}
	if v.Status == store.ProjectDraft && len(v.Rights) > 0 {
		parts = append(parts, say("enabling grants: %s", strings.Join(v.Rights, "; ")))
	}
	if v.Status == store.ProjectDraft && len(v.Asks) > 0 {
		parts = append(parts, say("asks you each time: %s", strings.Join(v.Asks, ", ")))
	}
	return strings.Join(parts, " · ")
}

// emptyManifest is a project with nothing in it yet.
const emptyManifest = `{"version": %d}`

type projectCreateIn struct {
	Name     string `desc:"Project name: lowercase letters, digits and '-'"`
	Manifest string `type:"json" opt:"" desc:"Manifest JSON; an empty project when omitted"`
	Prompt   string `type:"text" opt:"" desc:"Project md prompt"`
}

func (pj *Service) projectCreate(ctx context.Context, act registry.Actor, in projectCreateIn) (any, error) {
	if strings.TrimSpace(in.Manifest) == "" {
		in.Manifest = fmt.Sprintf(emptyManifest, manifest.FormatVersion)
	}
	p, err := pj.CreateProject(ctx, act, in.Name, []byte(in.Manifest), in.Prompt)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": p.ID, "name": p.Name}, nil
}

func (pj *Service) projectsCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("projects_list", "List projects",
			"Lists projects with their description (the first line of the md prompt), screens, folders and what enabling a draft grants.",
			registry.OwnerSide, pj.projectsList).On("projects", "folders", "documents"),
		registry.Def("project_create", "Create project",
			"Creates a project from its name, an md prompt and a manifest; without a manifest it is empty.",
			registry.OwnerSide, pj.projectCreate),
	}
}
