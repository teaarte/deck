package project

import (
	"context"
	"deck2/internal/events"
	"fmt"
	"maps"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/files"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/words"
	"deck2/schemas"
)

// folderView is a folder of the owner a project works in.
type folderView struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

type projectView struct {
	ID            int64                 `json:"id"`
	Name          string                `json:"name"`
	Status        string                `json:"status" enum:"enabled|draft"`
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
	Models   []string     `json:"models"`
	Revision int64        `json:"revision"`
	Folders  []folderView `json:"folders"`
	// Icon and Title are the icon and the name the owner sees that its manifest gives; Title is the name without one.
	Icon  string `json:"icon,omitempty"`
	Title string `json:"title"`
	// Cover is the picture of its card in the theme in use.
	Cover *files.Cover `json:"cover,omitempty"`
	// Meta is one line of its state, folders and what enabling a draft grants, in the language asked for.
	Meta string `json:"meta"`
}

type projectsListIn struct {
	Project string `type:"project" opt:"" desc:"Only this project"`
	Lang    string `opt:"" desc:"Language of the titles and words, such as ru; English without one"`
}

func (s *Service) projectsList(ctx context.Context, act registry.Actor, in projectsListIn) ([]projectView, error) {
	ps, err := s.St.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	if in.Project != "" {
		ps = slices.DeleteFunc(ps, func(p db.Project) bool { return p.Name != in.Project })
	}
	ms, failed := docs.All(ctx, s.St.Q, ps)
	pics, _ := s.St.Q.ListWorkspacePictures(ctx, db.ListWorkspacePicturesParams{WorkspaceID: act.Workspace, Slot: files.CoverSlot})
	theme := s.Theme(ctx, act.Workspace)
	out := make([]projectView, 0, len(ps))
	for _, p := range ps {
		v := s.viewProject(ctx, p, ms[p.ID], failed[p.ID], registry.Lang(ctx))
		v.Cover = files.CoverOf(pics, p.ID, theme)
		out = append(out, v)
	}
	return out, nil
}

// viewProject is a project as the owner sees it, with its manifest when that loads.
func (s *Service) viewProject(ctx context.Context, p db.Project, m *manifest.Manifest, err error, lang string) projectView {
	v := projectView{ID: p.ID, Name: p.Name, Title: p.Name, Status: p.Status, Paused: p.Paused == 1, Builtin: p.Builtin == 1, Screens: []manifest.ScreenLink{},
		Rights: []string{}, Asks: []string{}, Models: []string{}}
	fs, _ := s.St.Q.ListProjectFolders(ctx, p.ID)
	v.Folders = make([]folderView, 0, len(fs))
	for _, f := range fs {
		v.Folders = append(v.Folders, folderView{Name: f.Name, Path: f.Path})
	}
	if prompt, err := docs.CurrentText(ctx, s.St.Q, docs.ProjectScope(p), docs.KindPrompt, docs.PromptName); err == nil {
		v.Description = docs.Description(prompt)
	}
	ws := maps.Clone(words.In(lang))
	if err != nil {
		v.ManifestError = err.Error()
		v.Meta = s.meta(words.In(lang), v)
		return v
	}
	if lang != "" {
		if ws == nil {
			ws = map[string]string{}
		}
		maps.Copy(ws, m.I18n[lang])
		v.Description = manifest.Tr(ws, v.Description)
	}
	v.Revision = m.Revision
	v.Title, v.Icon, v.Screens = manifest.Nav(p.Name, m, ws)
	if p.Status == store.ProjectDraft {
		v.Rights, v.Asks = append(v.Rights, rights(m)...), append(v.Asks, m.AskEachTime()...)
	}
	if f, err := docs.For(ctx, s.St.Q, registry.Actor{}, p.ID); err == nil {
		v.Models = append(v.Models, f.Uses()...)
	}
	v.Meta = s.meta(words.In(lang), v)
	return v
}

// meta is the line of a project under its title in deck's words alone: its state, its network, its folders and what
// enabling a draft grants and asks.
func (s *Service) meta(ws map[string]string, v projectView) string {
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

func (s *Service) projectCreate(ctx context.Context, act registry.Actor, in projectCreateIn) (any, error) {
	if strings.TrimSpace(in.Manifest) == "" {
		in.Manifest = fmt.Sprintf(emptyManifest, manifest.FormatVersion)
	}
	p, err := s.CreateProject(ctx, act, in.Name, []byte(in.Manifest), in.Prompt)
	if err != nil {
		return nil, err
	}
	return map[string]any{"id": p.ID, "name": p.Name}, nil
}

func (s *Service) projectsCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("projects_list", "List projects",
			"Lists projects with their description (the first line of the md prompt), screens, folders and what enabling a draft grants.",
			registry.OwnerSide, s.projectsList).On(events.Projects, events.Folders, events.Documents, events.Files, events.Settings),
		registry.Def("project_create", "Create module",
			"Creates a project from its name, an md prompt and a manifest; without a manifest it is empty.",
			registry.OwnerSide, s.projectCreate),
	}
}

// CreateProject validates the manifest, stores it and the prompt as first revisions and registers triggers.
// The owner's project is enabled; anyone else's is a draft.
func (s *Service) CreateProject(ctx context.Context, act registry.Actor, name string, raw []byte, prompt string) (db.Project, error) {
	status := store.ProjectDraft
	if act.Kind == registry.Owner {
		status = store.ProjectEnabled
	}
	return s.createProject(ctx, act, name, raw, prompt, status, nil)
}

// createProject stores a new project; more stores further documents in the same transaction before the checks.
func (s *Service) createProject(ctx context.Context, act registry.Actor, name string, raw []byte, prompt, status string,
	more func(ctx context.Context, q *db.Queries, p db.Project) error) (db.Project, error) {
	if !schemas.ProjectName.MatchString(name) || name == registry.Builtin {
		return db.Project{}, registry.Invalid("project name %q must be lowercase letters, digits and '-', start with a letter, and not be %q", name, registry.Builtin)
	}
	pm, err := parseManifest(raw, false)
	if err != nil {
		return db.Project{}, err
	}
	var p db.Project
	err = s.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		if _, err := q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: name}); err == nil {
			return registry.Invalid("project %q already exists", name)
		}
		if err := checkReads(ctx, q, act.Workspace, name, pm.m); err != nil {
			return err
		}
		if err := s.checkToolFit(ctx, q, act.Workspace, 0, pm.m); err != nil {
			return err
		}
		if p, err = q.CreateProject(ctx, db.CreateProjectParams{WorkspaceID: act.Workspace, Name: name, Status: status}); err != nil {
			return err
		}
		ref := docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName}
		_, n, _, err := docs.Save(ctx, q, ref, docs.KindManifest, string(pm.text), act.Author(), "create project")
		if err != nil {
			return err
		}
		if err := grant(ctx, q, p, n, pm.m); err != nil {
			return err
		}
		if prompt != "" {
			ref.Name = docs.PromptName
			if _, _, _, err := docs.Save(ctx, q, ref, docs.KindPrompt, prompt, act.Author(), "create project"); err != nil {
				return err
			}
		}
		if more != nil {
			if err := more(ctx, q, p); err != nil {
				return err
			}
		}
		return s.Scheduler.Sync(ctx, q, p.ID, pm.m)
	})
	if err != nil {
		return db.Project{}, err
	}
	s.PrepareLater(p.ID)
	return p, nil
}
