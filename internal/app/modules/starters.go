package modules

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"strings"

	"deck2/internal/docs"
	"deck2/internal/events"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/starters"
)

// Starter is the files of a module deck offers on its first run, by the name of its folder.
func Starter(name string) ([]docs.File, error) {
	if d, err := fs.Stat(starters.FS, name); !fs.ValidPath(name) || strings.Contains(name, "/") || err != nil || !d.IsDir() {
		return nil, registry.NotFound("no starter module %q; deck.starters_list names them", name)
	}
	sub, err := fs.Sub(starters.FS, name)
	if err != nil {
		return nil, err
	}
	return docs.Files(sub, "", func(string) string { return "" })
}

// starterRow is a module deck offers on its first run; Status and Revision are of its draft once it is imported.
type starterView struct {
	Name        string `json:"name"`
	Title       string `json:"title"`
	Icon        string `json:"icon"`
	Description string `json:"description"`
	Status      string `json:"status"`
	Revision    int64  `json:"revision"`
}

type startersListIn struct {
	Lang string `opt:"" desc:"Language of the titles, such as ru; English without one"`
}

// startersList offers the starter modules while deck has no module of the owner's, and keeps one that is a draft
// until the owner enables it.
func (s *Service) startersList(ctx context.Context, act registry.Actor, in startersListIn) ([]starterView, error) {
	ps, err := s.St.Q.ListWorkspaceProjects(ctx, act.Workspace)
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
	var out []starterView
	for _, e := range es {
		name := e.Name()
		if !e.IsDir() {
			continue
		}
		row := starterView{Name: name, Title: name}
		p, err := s.St.Q.GetProjectByName(ctx, db.GetProjectByNameParams{WorkspaceID: act.Workspace, Name: name})
		switch {
		case err == nil && p.Status != "draft", err != nil && !errors.Is(err, sql.ErrNoRows), errors.Is(err, sql.ErrNoRows) && own:
			continue
		case err == nil:
			row.Status = p.Status
			if d, err := docs.Find(ctx, s.St.Q, docs.Ref{Scope: docs.ProjectScope(p), Name: docs.ManifestName}); err == nil {
				row.Revision = d.Current
			}
		}
		files, err := Starter(name)
		if err != nil {
			return nil, err
		}
		var ws map[string]string
		for _, f := range files {
			switch f.Name {
			case docs.PromptName:
				row.Description = docs.Description(f.Text)
			case docs.ManifestName:
				if m, err := manifest.Parse([]byte(f.Text)); err == nil {
					ws = m.I18n[registry.Lang(ctx)]
					row.Title, row.Icon = cmp.Or(manifest.Tr(ws, m.Title), name), m.Icon
				}
			}
		}
		row.Description = manifest.Tr(ws, row.Description)
		out = append(out, row)
	}
	return out, nil
}

func (s *Service) startersCommands() []*registry.Command {
	return []*registry.Command{
		registry.Read("starters_list", "Starter modules",
			"Lists the modules deck offers on its first run while it has none of the owner's, and one imported as a draft until the owner enables it.",
			registry.OwnerSide, s.startersList).Brings(registry.FromDeck).On(events.Projects, events.Documents),
	}
}
