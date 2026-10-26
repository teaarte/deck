package screens

import (
	"context"
	"deck2/internal/docs"
	"deck2/internal/files"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"slices"
	"sync"
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
	// Cover is where the client reads the light copy of the picture of the project's card in the theme in use, for
	// the light scheme and the dark, none without one; the colors are their main ones, shown while they load.
	Cover          string `json:"cover,omitempty"`
	CoverDark      string `json:"cover_dark,omitempty"`
	CoverColor     string `json:"cover_color,omitempty"`
	CoverDarkColor string `json:"cover_dark_color,omitempty"`
	// Widgets are the least sizes of the views of the card of the project, such as 2x1.
	Widgets []string `json:"widgets,omitempty"`
	// Doing is the title of the command the newest open process of the project runs; Next the next run of its
	// schedule.
	Doing string   `json:"doing,omitempty"`
	Next  *nextRun `json:"next,omitempty"`
}

// nextRun is the command a trigger of the schedule runs next, by its title, and when.
type nextRun struct {
	Title string `json:"title"`
	At    string `json:"at"`
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

// menuOut is the navigation of the client: the pulse, deck's screens, the projects of the side list but those paused,
// the master, the foot, the language the workspace picked, and the topics after which it is read again.
type menuOut struct {
	Pulse    pulse       `json:"pulse"`
	Language string      `json:"language"`
	Deck     menuEntry   `json:"deck"`
	Projects []menuEntry `json:"projects"`
	Master   *menuEntry  `json:"master,omitempty"`
	System   []menuGroup `json:"system"`
	Topics   []string    `json:"topics"`
}

// menuTopics are the topics of what the menu reads: the projects, their manifests, order, covers and schedules, and
// what it counts.
var menuTopics = sync.OnceValue(func() []string {
	out := []string{"projects", "documents", "settings", "files", "triggers"}
	for _, name := range []string{"deck.asks_list", "deck.processes_list", "deck.health_list", "deck.feed_list"} {
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

func (ss *Service) menu(ctx context.Context, act registry.Actor, in menuIn) (any, error) {
	out := menuOut{Deck: menuEntry{Title: "deck"}, Projects: []menuEntry{}, System: []menuGroup{}, Topics: menuTopics(),
		Language: ss.Language(ctx, act.Workspace)}
	deckWords := words(in.Lang, nil)
	for _, name := range shipped().Menu {
		s := DeckScreen(name)
		out.Deck.Screens = append(out.Deck.Screens, manifest.ScreenLink{Name: name, Title: manifest.Tr(deckWords, s.Title), Icon: s.Icon})
	}
	ps, err := ss.St.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	var asks, running, failing map[string]int64
	out.Pulse, asks, running, failing = ss.pulse(ctx, act, in.Lang)
	if out.Pulse.State == "calm" && !slices.ContainsFunc(ps, func(p db.Project) bool { return p.Builtin == 0 }) {
		out.Pulse.Line = manifest.Tr(deckWords, "Tell Deki what to do: say a task in the chat and Deki builds a module for it, or start with a module below.")
	}
	order := ss.order(ctx, ss.St.Q, act.Workspace)
	slices.SortStableFunc(ps, func(x, y db.Project) int { return rank(order, x.ID) - rank(order, y.ID) })
	ms, _ := docs.All(ctx, ss.St.Q, ps)
	pics, _ := ss.St.Q.ListWorkspacePictures(ctx, db.ListWorkspacePicturesParams{WorkspaceID: act.Workspace, Slot: files.CoverSlot})
	open, _ := ss.St.Q.ListOpenProcesses(ctx, db.ListOpenProcessesParams{WorkspaceID: act.Workspace})
	triggers, _ := ss.St.Q.ListWorkspaceTriggers(ctx, db.ListWorkspaceTriggersParams{WorkspaceID: act.Workspace})
	theme := ss.Theme(ctx, act.Workspace)
	for _, p := range ps {
		m := ms[p.ID]
		if m == nil {
			continue
		}
		ws := words(in.Lang, m)
		e := menuEntry{Project: p.Name, Running: running[p.Name], Asks: asks[p.Name], Errors: failing[p.Name]}
		if c := files.CoverOf(pics, p.ID, theme); c != nil {
			e.Cover, e.CoverColor, e.CoverDark, e.CoverDarkColor = c.Light, c.Color, c.Dark, c.DarkColor
		}
		e.Doing, e.Next = doing(p, m, ws, open, triggers)
		for _, w := range m.Widgets {
			e.Widgets = append(e.Widgets, w.Min)
		}
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
		if p.Paused != 0 {
			continue
		}
		if len(e.Screens) == 1 {
			e.Open = &e.Screens[0]
		}
		out.Projects = append(out.Projects, e)
	}
	out.System = ss.foot(out, in.Lang)
	return out, nil
}

// doing is the title of the command the newest open process of a project runs, and the next run of its schedule.
func doing(p db.Project, m *manifest.Manifest, ws map[string]string, open []db.Process, triggers []db.Trigger) (string, *nextRun) {
	title := func(command string) string {
		if c := m.Command(command); c != nil && c.Title != "" {
			return manifest.Tr(ws, c.Title)
		}
		return command
	}
	var now string
	if i := slices.IndexFunc(open, func(x db.Process) bool { return x.ProjectID == p.ID && x.ParentID.Int64 == 0 }); i >= 0 {
		now = title(open[i].Command)
	}
	var next *nextRun
	for _, t := range triggers {
		if t.ProjectID == p.ID && t.Paused == 0 && t.NextRunAt.Valid && (next == nil || t.NextRunAt.String < next.At) {
			next = &nextRun{Title: title(t.Command), At: t.NextRunAt.String}
		}
	}
	return now, next
}

// rank is the place of a project in the order; one not in it goes after all that are.
func rank(order []int64, id int64) int {
	if i := slices.Index(order, id); i >= 0 {
		return i
	}
	return len(order)
}

// foot are the groups at the foot of the menu: deck's work, the master's screens but its documents, which are a
// folder of Files, and deck's look and settings; an empty group is left out.
func (ss *Service) foot(m menuOut, lang string) []menuGroup {
	work := menuGroup{Key: "work", Title: manifest.Tr(Phrases(lang), "Work")}
	settings := menuGroup{Key: "settings"}
	for _, s := range m.Deck.Screens {
		if s.Name == "settings" || s.Name == "look" {
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
