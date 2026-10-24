package screens

import (
	"context"
	"database/sql"
	"deck2/internal/docs"
	"deck2/internal/files"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
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
	// Cover is where the client reads the picture of the project's card, none without one.
	Cover string `json:"cover,omitempty"`
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

// menuTopics are the topics of what the menu reads: the projects, their manifests, their order and what it counts.
var menuTopics = sync.OnceValue(func() []string {
	out := []string{"projects", "documents", "settings", "files"}
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
	order, err := ss.menuOrder(ctx, ss.St.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	slices.SortStableFunc(ps, func(x, y db.Project) int { return rank(order, x.ID) - rank(order, y.ID) })
	ms, _ := docs.All(ctx, ss.St.Q, ps)
	for _, p := range ps {
		m := ms[p.ID]
		if m == nil {
			continue
		}
		ws := words(in.Lang, m)
		e := menuEntry{Project: p.Name, Running: running[p.Name], Asks: asks[p.Name], Errors: failing[p.Name]}
		if id := files.Cover(ctx, ss.St.Q, p.ID); id != 0 {
			e.Cover = fmt.Sprintf("/api/files/%d", id)
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

// menuOrderKey is the setting of the order of the side list of the menu: a JSON list of ids of projects, so a new
// project of the name of a deleted one goes last.
const menuOrderKey = "menu_order"

type menuOrderIn struct {
	Projects []string `desc:"Names of the projects of the side list in the order to show them; a project not named keeps its place after the one before it, a new one goes last"`
}

// menuOrder is the order of the side list the owner set, ids of projects; none when not set.
func (ss *Service) menuOrder(ctx context.Context, q *db.Queries, workspace int64) ([]int64, error) {
	v, err := q.GetSetting(ctx, db.GetSettingParams{WorkspaceID: workspace, Key: menuOrderKey})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var order []int64
	if err := json.Unmarshal([]byte(v), &order); err != nil {
		slog.Error("menu: order", "err", err)
		return nil, nil
	}
	return order, nil
}

// rank is the place of a project in the order; one not in it goes after all that are.
func rank(order []int64, id int64) int {
	if i := slices.Index(order, id); i >= 0 {
		return i
	}
	return len(order)
}

// menuOrderSet keeps the order of the side list: the projects named, and each one the menu had but not named, such
// as a paused one, after the one it followed; names of no project are dropped.
func (ss *Service) menuOrderSet(ctx context.Context, act registry.Actor, in menuOrderIn) (any, error) {
	out := []string{}
	err := ss.St.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		ps, err := q.ListWorkspaceProjects(ctx, act.Workspace)
		if err != nil {
			return err
		}
		ps = slices.DeleteFunc(ps, func(p db.Project) bool { return p.Builtin != 0 })
		saved, err := ss.menuOrder(ctx, q, act.Workspace)
		if err != nil {
			return err
		}
		slices.SortStableFunc(ps, func(x, y db.Project) int { return rank(saved, x.ID) - rank(saved, y.ID) })
		var order []db.Project
		for _, n := range in.Projects {
			if k := slices.IndexFunc(ps, func(p db.Project) bool { return p.Name == n }); k >= 0 && !slices.Contains(order, ps[k]) {
				order = append(order, ps[k])
			}
		}
		for i, p := range ps {
			if slices.Contains(order, p) {
				continue
			}
			at := 0
			for j := i - 1; j >= 0; j-- {
				if k := slices.Index(order, ps[j]); k >= 0 {
					at = k + 1
					break
				}
			}
			order = slices.Insert(order, at, p)
		}
		ids := []int64{}
		for _, p := range order {
			ids, out = append(ids, p.ID), append(out, p.Name)
		}
		v, _ := json.Marshal(ids)
		return q.SetSetting(ctx, db.SetSettingParams{WorkspaceID: act.Workspace, Key: menuOrderKey, Value: string(v)})
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"projects": out}, nil
}

// foot are the groups at the foot of the menu: deck's work, the master's screens but its documents, which are a
// tab of Files, and deck's settings; an empty group is left out.
func (ss *Service) foot(m menuOut, lang string) []menuGroup {
	work := menuGroup{Key: "work", Title: manifest.Tr(Phrases(lang), "Work")}
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
