package app

import (
	"context"
	_ "embed"
	"encoding/json"
	"strings"

	"deck2/internal/app/docs"
	"deck2/internal/app/screens"
	"deck2/internal/manifest"
	"deck2/internal/provider"
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
		m, err := docs.Manifest(ctx, a.Store.Q, p)
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
	req := screen.Request{Record: in.Record, Topic: in.Topic, Owner: act.Kind == registry.Owner}
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
		m, err := docs.Manifest(ctx, a.Store.Q, p)
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
	out, err := screen.Render(ctx, a.Store, a.sources(), req)
	if err != nil {
		return nil, registry.NotFound("deck: %v", err)
	}
	return out, nil
}

// sources are the rows of screens, read from this deck.
func (a *App) sources() screens.Sources {
	return screens.Sources{Store: a.Store, Registry: a.Registry, Deck: screenDeck{a}, Now: a.Cfg.Now, Tail: a.Runner.Tail}
}

type screenDeck struct{ a *App }

func (d screenDeck) Project(ctx context.Context, p db.Project) (screens.Project, *manifest.Manifest) {
	v, m := d.a.viewProject(ctx, p)
	var folders []string
	for _, f := range v.Folders {
		folders = append(folders, f.Name+": "+f.Path)
	}
	return screens.Project{Description: v.Description, Revision: v.Revision, Rights: v.Rights, ManifestError: v.ManifestError, Folders: folders}, m
}

func (d screenDeck) Providers(ctx context.Context, workspace int64) ([]screens.Provider, error) {
	got, err := d.a.providersList(ctx, owner(workspace), struct{ Command string }{})
	if err != nil {
		return nil, err
	}
	pvs, err := d.a.providers(ctx, workspace)
	if err != nil {
		return nil, err
	}
	checked, builtin := map[string]bool{}, map[string]bool{}
	for _, pv := range pvs {
		_, checked[pv.Name()] = pv.(provider.SelfChecker)
	}
	for _, pv := range d.a.builtin {
		builtin[pv.Name()] = true
	}
	views := got.(map[string]any)["providers"].([]providerView)
	out := make([]screens.Provider, 0, len(views))
	for _, v := range views {
		caps := make([]string, 0, len(v.Caps))
		for _, c := range v.Caps {
			caps = append(caps, string(c))
		}
		out = append(out, screens.Provider{Name: v.Name, Agent: v.Agent, Model: v.Model, URL: v.URL, Status: v.Status, Verdict: v.Verdict,
			Version: v.Version, Error: v.Error, Speed: v.Speed, Caps: caps, Builtin: builtin[v.Name], SelfCheck: checked[v.Name]})
	}
	return out, nil
}

func (d screenDeck) Health(ctx context.Context, workspace int64) ([]map[string]any, error) {
	got, err := d.a.healthList(ctx, owner(workspace), struct{}{})
	if err != nil {
		return nil, err
	}
	return got.([]map[string]any), nil
}

func (d screenDeck) Secrets(ctx context.Context, workspace int64) ([]string, error) {
	got, err := d.a.secretsList(ctx, owner(workspace), struct{}{})
	if err != nil {
		return nil, err
	}
	return got.([]string), nil
}

func (d screenDeck) Triggers(ctx context.Context, workspace int64) ([]screens.Trigger, error) {
	got, err := d.a.triggersList(ctx, owner(workspace), struct{ Project string }{})
	if err != nil {
		return nil, err
	}
	return got.([]screens.Trigger), nil
}

func (d screenDeck) Settings(ctx context.Context, workspace int64) []map[string]any {
	return d.a.settingItems(ctx, workspace)
}

func (d screenDeck) Calls(ctx context.Context, workspace, project int64, outcome string) ([]map[string]any, error) {
	return d.a.calls(ctx, workspace, project, outcome, 0)
}

func (d screenDeck) Snapshots() ([]map[string]any, error) { return d.a.snapshotItems() }

func (d screenDeck) Waits(ctx context.Context, project int64) (map[int64][]int64, error) {
	return waitsOf(ctx, d.a.Store.Q, project)
}

func owner(workspace int64) registry.Actor {
	return registry.Actor{Kind: registry.Owner, Workspace: workspace}
}

func (d screenDeck) Chat(ctx context.Context, workspace int64, topic string) (any, error) {
	return d.a.Chat(ctx, workspace, topic)
}

func (d screenDeck) Terminal(ctx context.Context, p db.Process) string { return d.a.terminal(ctx, p) }
