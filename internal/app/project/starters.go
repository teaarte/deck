package project

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/script"
	"deck2/internal/store/db"
	"deck2/internal/words"
	"deck2/starters"
)

// starter is the files of a module deck offers on its first run, by the name of its folder.
func starter(name string) ([]folderFile, error) {
	if d, err := fs.Stat(starters.FS, name); !fs.ValidPath(name) || strings.Contains(name, "/") || err != nil || !d.IsDir() {
		return nil, registry.NotFound("deck: no starter module %q; deck.starters_list names them", name)
	}
	sub, err := fs.Sub(starters.FS, name)
	if err != nil {
		return nil, err
	}
	shipped, err := docs.Files(sub, "", func(string) string { return "" })
	files := make([]folderFile, 0, len(shipped))
	for _, f := range shipped {
		files = append(files, folderFile{path: f.Name, text: f.Text})
	}
	return files, err
}

// starterRow is a module deck offers on its first run; Status and Revision are of its draft once it is imported.
type starterRow struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Revision    int64  `json:"revision"`
}

type startersIn struct {
	Lang string `opt:"" desc:"Language of the titles, such as ru; English without one"`
}

// startersList offers the starter modules while deck has no module of the owner's, and keeps one that is a draft
// until the owner enables it.
func (pj *Service) startersList(ctx context.Context, act registry.Actor, in startersIn) ([]starterRow, error) {
	ps, err := pj.St.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	es, err := starters.FS.ReadDir(".")
	if err != nil {
		return nil, err
	}
	names := map[string]bool{}
	for _, e := range es {
		names[e.Name()] = e.IsDir()
	}
	own := false
	for _, p := range ps {
		if p.Builtin == 0 && !names[p.Name] {
			own = true
		}
	}
	var out []starterRow
	for _, e := range es {
		name := e.Name()
		if !e.IsDir() {
			continue
		}
		row := starterRow{Name: name, Title: name}
		p, err := pj.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: name})
		switch {
		case err == nil && p.Status != "draft", err != nil && !errors.Is(err, sql.ErrNoRows), errors.Is(err, sql.ErrNoRows) && own:
			continue
		case err == nil:
			row.Status = p.Status
			if d, err := docs.Find(ctx, pj.St.Q, docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName}); err == nil {
				row.Revision = d.Current
			}
		}
		files, err := starter(name)
		if err != nil {
			return nil, err
		}
		var ws map[string]string
		for _, f := range files {
			switch f.path {
			case docs.PromptName:
				row.Description = docs.Description(f.text)
			case docs.ManifestName:
				if m, err := manifest.Parse([]byte(f.text)); err == nil {
					ws = m.I18n[in.Lang]
					row.Title, row.Icon = cmp.Or(manifest.Tr(ws, m.Title), name), m.Icon
				}
			}
		}
		row.Description = manifest.Tr(ws, row.Description)
		out = append(out, row)
	}
	return out, nil
}

// needRow is one thing a module needs to work: a right enabling grants, a secret, a program or a model.
type needRow struct {
	Kind   string `json:"kind" enum:"right|secret|program|model"`
	What   string `json:"what"`
	Detail string `json:"detail"`
	Done   bool   `json:"done"`
	// Optional is a connection or a model it can call besides its roles, which it works without.
	Optional bool `json:"optional,omitempty"`
}

// projectNeeds sums up what a module needs to work: the rights enabling grants, the secrets it names, the programs
// its scripts miss with how to install them, and its work no model takes.
type needsIn struct {
	Project string `type:"project" desc:"Project name"`
	Lang    string `opt:"" desc:"Language of the words, such as ru; English without one"`
}

func (pj *Service) projectNeeds(ctx context.Context, act registry.Actor, in needsIn) ([]needRow, error) {
	rights, err := pj.projectRights(ctx, act, projectExportIn{Project: in.Project})
	if err != nil {
		return nil, err
	}
	p, err := pj.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: in.Project})
	if err != nil {
		return nil, err
	}
	ms, _ := docs.All(ctx, pj.St.Q, []db.Project{p})
	return pj.needs(ctx, p, ms[p.ID], rights, words.In(in.Lang)), nil
}

func (pj *Service) needs(ctx context.Context, p db.Project, m *manifest.Manifest, rights []rightRow, ws map[string]string) []needRow {
	out := []needRow{}
	for _, r := range rights {
		out = append(out, needRow{Kind: "right", What: r.What, Detail: r.Detail, Done: p.Status != "draft"})
	}
	if m == nil {
		return out
	}
	for _, s := range m.Secrets() {
		out = append(out, needRow{Kind: "secret", What: s, Done: pj.Secret(p.WorkspaceID, s)})
	}
	missing := script.Check(m)
	for _, name := range slices.Sorted(maps.Keys(missing)) {
		out = append(out, needRow{Kind: "program", What: name, Detail: missing[name].Error()})
	}
	out = append(out, pj.missingModels(ctx, p.WorkspaceID, m, ws)...)
	work := pj.Unassigned(ctx, p.WorkspaceID, p.ID, m)
	for _, name := range slices.Sorted(maps.Keys(work)) {
		out = append(out, needRow{Kind: "model", What: name, Detail: work[name]})
	}
	return out
}

func (pj *Service) startersCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("starters_list", "Starter modules",
			"Lists the modules deck offers on its first run while it has none of the owner's, and one imported as a draft until the owner enables it.",
			registry.OwnerSide, pj.startersList).On("projects", "documents"),
		registry.Read("project_needs_get", "What a module needs",
			"Sums up what a module needs to work: the rights enabling it grants, the secrets it names and whether they are set, the programs its scripts miss with how to install them, and its work no model of deck takes.",
			registry.OwnerSide, pj.projectNeeds).On("projects", "documents", "secrets", "models", "assignments"),
	}
}

// missingModels are the connections, models and @roles a module calls models through that deck lacks: a role it needs,
// a connection or a model it works without, as another owner has their own.
func (pj *Service) missingModels(ctx context.Context, workspace int64, m *manifest.Manifest, ws map[string]string) []needRow {
	have := map[string]bool{}
	if cs, err := pj.St.Q.ListConnections(ctx, workspace); err == nil {
		for _, c := range cs {
			have[c.Name] = true
		}
	}
	if ms, err := pj.St.Q.ListModels(ctx, workspace); err == nil {
		for _, x := range ms {
			have[x.Model.Name] = true
		}
	}
	if as, err := pj.St.Q.ListAssignments(ctx, workspace); err == nil {
		for _, a := range as {
			have["@"+a.Role] = a.Models != "" && a.Models != "[]"
		}
	}
	var out []needRow
	for _, u := range m.Models {
		conn, _, _ := strings.Cut(u, "/")
		switch {
		case have[u] || have[conn] && !strings.HasPrefix(u, "@"):
		case strings.HasPrefix(u, "@"):
			out = append(out, needRow{Kind: "model", What: u, Detail: manifest.Tr(ws, "Assign a model to the role in Settings → Models.")})
		default:
			out = append(out, needRow{Kind: "model", What: u, Detail: fmt.Sprintf(cmp.Or(ws[addConnection], addConnection), conn), Optional: true})
		}
	}
	return out
}

const addConnection = "Optional: add the connection %s in Settings → Models → Connections, or a model of that name, and it calls its models too."
