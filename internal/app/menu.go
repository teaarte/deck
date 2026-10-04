package app

import (
	"context"
	_ "embed"

	"deck2/internal/app/project"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

type menuEntry struct {
	Project string               `json:"project,omitempty"`
	Title   string               `json:"title"`
	Icon    string               `json:"icon,omitempty"`
	Screens []project.ScreenLink `json:"screens"`
	// Extra are the screens deck adds to every project, shown apart from the project's own.
	Extra []project.ScreenLink `json:"extra,omitempty"`
	// Running, Asks and Errors are the project's running processes, open questions and failing health rows.
	Running int64 `json:"running,omitempty"`
	Asks    int64 `json:"asks,omitempty"`
	Errors  int64 `json:"errors,omitempty"`
}

// menu is the navigation of the client: deck's screens, then each project's menu, or all its screens without one.
type menuIn struct {
	Lang string `opt:"" desc:"Language of the titles, such as ru; as written without one"`
}

func (a *App) menu(ctx context.Context, act registry.Actor, in menuIn) (any, error) {
	out := []menuEntry{{Title: "deck"}}
	deckWords := words(in.Lang, nil)
	for _, name := range shipped().Menu {
		s := deckScreen(name)
		out[0].Screens = append(out[0].Screens, project.ScreenLink{Name: name, Title: project.Tr(deckWords, s.Title), Icon: s.Icon})
	}
	ps, err := a.Store.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	pulses, err := a.Store.Q.ProjectPulses(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	pulse := map[int64]db.ProjectPulsesRow{}
	for _, r := range pulses {
		pulse[r.ID] = r
	}
	ms, _ := a.Manifests.All(ctx, ps)
	for _, p := range ps {
		m := ms[p.ID]
		if m == nil {
			continue
		}
		r := pulse[p.ID]
		ws := words(in.Lang, m)
		e := menuEntry{Project: p.Name, Running: r.Running, Asks: r.Asks, Errors: r.Errors}
		e.Title, e.Icon, e.Screens = project.Nav(p, m, ws)
		for _, s := range shipped().Project {
			if s.Record == "" && m.Screen(s.Name) == nil {
				e.Extra = append(e.Extra, project.ScreenLink{Name: s.Name, Title: project.Tr(ws, s.Title), Icon: s.Icon})
			}
		}
		out = append(out, e)
	}
	return out, nil
}
