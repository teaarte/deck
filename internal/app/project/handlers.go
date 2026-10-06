package project

import (
	"cmp"
	"context"
	"deck2/internal/app/folders"
	"fmt"
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
	Screens       []ScreenLink `json:"screens"`
	ManifestError string       `json:"manifest_error"`
	// Rights are what enabling a draft at Revision of its manifest grants.
	Rights []string `json:"rights"`
	// Asks are the tools and commands of a draft that will wait for the owner on every call once it is enabled.
	Asks     []string      `json:"asks"`
	Revision int64         `json:"revision"`
	Folders  []folders.Row `json:"folders"`
	// NoNetwork is of the manifest; false when it does not load.
	NoNetwork bool `json:"no_network"`
	// Icon and Title are the icon and the name the owner sees that its manifest gives; Title is the name without one.
	Icon  string `json:"icon,omitempty"`
	Title string `json:"title"`
}

type projectsListIn struct {
	Project string `type:"project" opt:"" desc:"Only this project"`
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
		out = append(out, pj.viewProject(ctx, p, ms[p.ID], failed[p.ID]))
	}
	return out, nil
}

// viewProject is a project as the owner sees it, with its manifest when that loads.
func (pj *Service) viewProject(ctx context.Context, p db.Project, m *manifest.Manifest, err error) projectView {
	v := projectView{ID: p.ID, Name: p.Name, Title: p.Name, Status: p.Status, Paused: p.Paused == 1, Builtin: p.Builtin == 1, Screens: []ScreenLink{},
		Rights: []string{}, Asks: []string{}}
	v.Folders, _ = pj.Folders(ctx, p.ID)
	if prompt, err := docs.CurrentText(ctx, pj.St.Q, docs.ProjectScope(p), docs.KindPrompt, docs.PromptName); err == nil {
		v.Description = docs.Description(prompt)
	}
	if err != nil {
		v.ManifestError = err.Error()
		return v
	}
	v.Revision, v.NoNetwork = m.Revision, m.NoNetwork
	v.Title, v.Icon, v.Screens = Nav(p, m, nil)
	if p.Status == store.ProjectDraft {
		v.Rights, v.Asks = append(v.Rights, rights(m)...), append(v.Asks, m.AskEachTime()...)
	}
	return v
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
	p, err := pj.Save.CreateProject(ctx, act, in.Name, []byte(in.Manifest), in.Prompt)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": p.ID, "name": p.Name}, nil
}

func (pj *Service) projectsCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("projects_list", "List projects",
			"Lists projects with their description (the first line of the md prompt), screens, folders and what enabling a draft grants.",
			registry.OwnerSide, pj.projectsList).On("projects"),
		registry.Def("project_create", "Create project",
			"Creates a project from its name, an md prompt and a manifest; without a manifest it is empty.",
			registry.OwnerSide, pj.projectCreate),
	}
}

// Nav is how a project shows in the menu and in the list of projects: its title, its icon and the
// screens of its menu, in the owner's words.
func Nav(p db.Project, m *manifest.Manifest, ws map[string]string) (string, string, []ScreenLink) {
	names := m.Menu
	if len(names) == 0 {
		for _, s := range m.Screens {
			if s.Record == "" {
				names = append(names, s.Name)
			}
		}
	}
	links := []ScreenLink{}
	for _, n := range names {
		s := m.Screen(n)
		links = append(links, ScreenLink{Name: n, Title: Tr(ws, s.Title), Icon: s.Icon})
	}
	return Tr(ws, cmp.Or(m.Title, p.Name)), m.Icon, links
}

// ScreenLink is a screen in the menu by its name, title and icon.
type ScreenLink struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
}

// Tr is a text in a language by words, or the text itself.
func Tr(ws map[string]string, s string) string {
	if t := ws[s]; t != "" {
		return t
	}
	return s
}
