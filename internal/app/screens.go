package app

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/screen"
	"deck2/internal/store/db"
)

//go:embed screens.json
var deckScreensJSON []byte

// deckScreens are the screens deck ships: the cockpit, providers and the page of a process.
var deckScreens = func() (d struct {
	Menu    []string
	Screens []manifest.Screen
	// Project are the screens deck adds to every project, after the project's own.
	Project []manifest.Screen
}) {
	var err error
	if d.Menu, d.Screens, d.Project, err = manifest.DeckScreens(deckScreensJSON); err != nil {
		panic(err)
	}
	return d
}()

func deckScreen(name string) *manifest.Screen {
	for i := range deckScreens.Screens {
		if deckScreens.Screens[i].Name == name {
			return &deckScreens.Screens[i]
		}
	}
	return nil
}

// projectScreen is a screen deck adds to every project whose manifest has none of that name.
func projectScreen(name string) *manifest.Screen {
	for i := range deckScreens.Project {
		if deckScreens.Project[i].Name == name {
			return &deckScreens.Project[i]
		}
	}
	return nil
}

type menuEntry struct {
	Project string       `json:"project,omitempty"`
	Title   string       `json:"title"`
	Screens []screenLink `json:"screens"`
}

type screenLink struct {
	Name  string `json:"name"`
	Title string `json:"title"`
}

// menu is the navigation of the client: deck's screens, then each project's menu, or all its screens without one.
func (a *App) menu(ctx context.Context, act registry.Actor, _ struct{}) (any, error) {
	out := []menuEntry{{Title: "deck"}}
	for _, name := range deckScreens.Menu {
		out[0].Screens = append(out[0].Screens, screenLink{Name: name, Title: deckScreen(name).Title})
	}
	ps, err := a.Store.Q.ListWorkspaceProjects(ctx, act.Workspace)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		m, err := loadManifest(ctx, a.Store.Q, p)
		if err != nil {
			continue
		}
		e := menuEntry{Project: p.Name, Title: p.Name, Screens: []screenLink{}}
		names := m.Menu
		if len(names) == 0 {
			for _, s := range m.Screens {
				if s.Record == "" {
					names = append(names, s.Name)
				}
			}
		}
		for _, n := range names {
			e.Screens = append(e.Screens, screenLink{Name: n, Title: m.Screen(n).Title})
		}
		for _, s := range deckScreens.Project {
			if s.Record == "" && m.Screen(s.Name) == nil {
				e.Screens = append(e.Screens, screenLink{Name: s.Name, Title: s.Title})
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// screenGet resolves a screen of a project, or of deck without one, into A2UI messages.
func (a *App) screenGet(ctx context.Context, act registry.Actor, in struct {
	Project, Screen string
	Record          int64
	Topic, Filter   string
}) (any, error) {
	req := screen.Request{Record: in.Record, Topic: in.Topic}
	if strings.TrimSpace(in.Filter) != "" {
		if err := json.Unmarshal([]byte(in.Filter), &req.Filter); err != nil {
			return nil, registry.Invalid("deck: filter: a JSON object of block id to field to value: %v", err)
		}
	}
	if in.Project == "" {
		req.Screen = deckScreen(in.Screen)
		req.Project.WorkspaceID = act.Workspace
	} else {
		p, err := a.scope(ctx, act, in.Project)
		if err != nil {
			return nil, err
		}
		m, err := loadManifest(ctx, a.Store.Q, p)
		if err != nil {
			return nil, err
		}
		req.Project, req.Manifest, req.Screen = p, m, m.Screen(in.Screen)
		if req.Screen == nil {
			req.Screen = projectScreen(in.Screen)
		}
	}
	if req.Screen == nil {
		return nil, registry.NotFound("deck: no screen %q", in.Screen)
	}
	out, err := screen.Render(ctx, a.Store, sources{a}, req)
	if err != nil {
		return nil, registry.NotFound("deck: %v", err)
	}
	return out, nil
}

type sources struct{ a *App }

func (s sources) Items(ctx context.Context, from string, sc screen.Scope) ([]map[string]any, error) {
	names, err := s.projectNames(ctx, sc.Workspace)
	if err != nil {
		return nil, err
	}
	switch from {
	case "asks":
		return s.a.askItems(ctx, sc, names)
	case "processes":
		return s.processItems(ctx, sc, names)
	case "unattended":
		return s.unattendedItems(ctx, sc, names)
	case "providers":
		return s.providerItems(ctx, sc)
	case "log":
		return s.logItems(sc.Record)
	case "triggers":
		return s.triggerItems(ctx, sc)
	case "projects":
		return s.projectItems(ctx, sc)
	case "notifications":
		return s.notificationItems(ctx, sc, names)
	case "secrets":
		return s.secretItems(ctx, sc)
	case "tokens":
		return s.tokenItems(ctx, sc)
	case "health":
		return s.healthItems(ctx, sc)
	case "settings":
		return s.a.settingItems(ctx, sc.Workspace), nil
	case "documents":
		return s.documentItems(ctx, sc, names)
	case "calls":
		return s.a.calls(ctx, sc.Workspace, filterProject(sc, names), sc.Filter["outcome"], 0)
	case "spend":
		return s.spendItems(ctx, sc, names)
	case "files":
		return s.fileItems(ctx, sc, names)
	case "snapshots":
		return s.snapshotItems()
	case "commands":
		return s.commandItems(ctx, sc, names)
	}
	return nil, fmt.Errorf("unknown source %q", from)
}

func (s sources) Board(ctx context.Context, p db.Project, m *manifest.Manifest) (any, error) {
	cards, err := s.a.Store.Q.ListCards(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	waits, err := waitsOf(ctx, s.a.Store.Q, p.ID)
	if err != nil {
		return nil, err
	}
	lanes := []map[string]any{}
	for _, l := range m.Board.Lanes {
		shown := []map[string]any{}
		for _, c := range cards {
			if c.Lane != l.Name {
				continue
			}
			v := viewCard(c, waits)
			card := map[string]any{"id": v.ID, "title": v.Title, "text": v.Text, "waits": v.Waits}
			if v.Record != 0 {
				card["record"] = v.Record
			}
			shown = append(shown, card)
		}
		lanes = append(lanes, map[string]any{"name": l.Name, "title": l.Title, "role": l.Role, "cards": shown})
	}
	deleted, err := s.a.Store.Q.ListDeletedCards(ctx, p.ID)
	if err != nil {
		return nil, err
	}
	gone := []map[string]any{}
	for _, c := range deleted {
		gone = append(gone, map[string]any{"id": c.ID, "title": c.Title})
	}
	return map[string]any{"project": p.Name, "lanes": lanes, "deleted": gone}, nil
}

func (s sources) projectNames(ctx context.Context, workspace int64) (map[int64]string, error) {
	ps, err := s.a.Store.Q.ListWorkspaceProjects(ctx, workspace)
	if err != nil {
		return nil, err
	}
	out := map[int64]string{}
	for _, p := range ps {
		out[p.ID] = p.Name
	}
	return out, nil
}

// filterProject is the project a filter picks by name within the scope, -1 for none, the scope without a name.
func filterProject(sc screen.Scope, names map[int64]string) int64 {
	name := sc.Filter["project"]
	if name == "" {
		return sc.Project
	}
	for id, n := range names {
		if n == name && (sc.Project == 0 || id == sc.Project) {
			return id
		}
	}
	return -1
}
