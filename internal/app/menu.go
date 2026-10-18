package app

import (
	"context"
	_ "embed"
	"log/slog"
	"slices"
	"sync"

	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/master"
)

type menuEntry struct {
	Project string                `json:"project,omitempty"`
	Title   string                `json:"title"`
	Icon    string                `json:"icon,omitempty"`
	Screens []manifest.ScreenLink `json:"screens"`
	// Open is the one screen of a project that has one: its header opens it.
	Open *manifest.ScreenLink `json:"open,omitempty"`
	// Extra are the screens deck adds to every project, shown apart from the project's own.
	Extra []manifest.ScreenLink `json:"extra,omitempty"`
	// Running, Asks and Errors are the project's open processes, what waits for the owner and its failing checks
	// not read, counted as the cockpit counts them.
	Running int64 `json:"running,omitempty"`
	Asks    int64 `json:"asks,omitempty"`
	Errors  int64 `json:"errors,omitempty"`
}

// pulse is what waits for the owner, runs and fails in the whole workspace, and State which of them the cockpit
// says first: asks, errors, talking (only the master works), running or calm.
type pulse struct {
	Asks    int64  `json:"asks"`
	Running int64  `json:"running"`
	Errors  int64  `json:"errors"`
	State   string `json:"state"`
}

// menuGroup is a group of links at the foot of the menu: work (deck's screens), master (the master's) and settings.
type menuGroup struct {
	Key   string     `json:"key"`
	Title string     `json:"title,omitempty"`
	Links []menuLink `json:"links"`
}

type menuLink struct {
	Project string `json:"project,omitempty"`
	manifest.ScreenLink
}

// menuOut is the navigation of the client: the pulse of the workspace, deck's screens, the projects of the side
// list, the master, the groups at the foot, and the event topics after which it is read again.
type menuOut struct {
	Pulse    pulse       `json:"pulse"`
	Deck     menuEntry   `json:"deck"`
	Projects []menuEntry `json:"projects"`
	Master   *menuEntry  `json:"master,omitempty"`
	System   []menuGroup `json:"system"`
	Topics   []string    `json:"topics"`
}

// menuTopics are the topics of what the menu reads: the projects, their manifests and what it counts.
var menuTopics = sync.OnceValue(func() []string {
	out := []string{"projects", "documents"}
	for _, name := range []string{"deck.asks_list", "deck.processes_list", "deck.health_list"} {
		c, _ := manifest.Reads(name)
		for _, t := range c.Topics {
			if !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	return out
})

type menuIn struct {
	Lang string `opt:"" desc:"Language of the titles, such as ru; as written without one"`
}

func (a *App) menu(ctx context.Context, act registry.Actor, in menuIn) (any, error) {
	out := menuOut{Deck: menuEntry{Title: "deck"}, Projects: []menuEntry{}, System: []menuGroup{}, Topics: menuTopics()}
	deckWords := words(in.Lang, nil)
	for _, name := range shipped().Menu {
		s := deckScreen(name)
		out.Deck.Screens = append(out.Deck.Screens, manifest.ScreenLink{Name: name, Title: manifest.Tr(deckWords, s.Title), Icon: s.Icon})
	}
	ps, err := a.Store.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	asks, running, failing := a.count(ctx, "asks", act, a.Answers.Waiting), a.count(ctx, "running", act, a.Processes.Open),
		a.count(ctx, "failing", act, a.Health.Failing)
	out.Pulse = pulse{Asks: total(asks), Running: total(running), Errors: total(failing)}
	switch {
	case out.Pulse.Asks > 0:
		out.Pulse.State = "asks"
	case out.Pulse.Errors > 0:
		out.Pulse.State = "errors"
	case out.Pulse.Running > 0 && running[master.Name] == out.Pulse.Running:
		out.Pulse.State = "talking"
	case out.Pulse.Running > 0:
		out.Pulse.State = "running"
	default:
		out.Pulse.State = "calm"
	}
	ms, _ := docs.All(ctx, a.Store.Q, ps)
	for _, p := range ps {
		m := ms[p.ID]
		if m == nil {
			continue
		}
		ws := words(in.Lang, m)
		e := menuEntry{Project: p.Name, Running: running[p.Name], Asks: asks[p.Name], Errors: failing[p.Name]}
		e.Title, e.Icon, e.Screens = manifest.Nav(p.Name, m, ws)
		for _, s := range shipped().Project {
			if s.Record == "" && m.Screen(s.Name) == nil {
				e.Extra = append(e.Extra, manifest.ScreenLink{Name: s.Name, Title: manifest.Tr(ws, s.Title), Icon: s.Icon})
			}
		}
		if p.Builtin == 1 {
			out.Master = &e
			continue
		}
		if len(e.Screens) == 1 {
			e.Open = &e.Screens[0]
		}
		out.Projects = append(out.Projects, e)
	}
	out.System = a.foot(out, in.Lang)
	return out, nil
}

// foot are the groups at the foot of the menu: deck's work, the master's screens but its documents, which are a
// tab of Files, and deck's settings; an empty group is left out.
func (a *App) foot(m menuOut, lang string) []menuGroup {
	work := menuGroup{Key: "work", Title: manifest.Tr(phrases(lang), "Work")}
	settings := menuGroup{Key: "settings"}
	for _, s := range m.Deck.Screens {
		if s.Name == "settings" {
			settings.Links = append(settings.Links, menuLink{ScreenLink: s})
		} else {
			work.Links = append(work.Links, menuLink{ScreenLink: s})
		}
	}
	groups := []menuGroup{work}
	if e := m.Master; e != nil {
		own := menuGroup{Key: "master", Title: e.Title}
		for _, s := range append(append([]manifest.ScreenLink{}, e.Screens...), e.Extra...) {
			if s.Name != "documents" {
				own.Links = append(own.Links, menuLink{Project: e.Project, ScreenLink: s})
			}
		}
		groups = append(groups, own)
	}
	out := []menuGroup{}
	for _, g := range append(groups, settings) {
		if len(g.Links) > 0 {
			out = append(out, g)
		}
	}
	return out
}

// count is one count of the menu by project; one that fails is none, so the menu still shows the way.
func (a *App) count(ctx context.Context, what string, act registry.Actor, f func(context.Context, registry.Actor) (map[string]int64, error)) map[string]int64 {
	by, err := f(ctx, act)
	if err != nil {
		slog.Error("menu: count", "what", what, "err", err)
		return map[string]int64{}
	}
	return by
}

// total is the sum of counts by project.
func total(by map[string]int64) int64 {
	var n int64
	for _, v := range by {
		n += v
	}
	return n
}
