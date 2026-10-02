package app

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"sync"

	"deck2/internal/app/screens"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/screen"
)

//go:embed screens.json
var deckScreensJSON []byte

type shippedScreens struct {
	Menu    []string
	Screens []manifest.Screen
	// Project are the screens deck adds to every project, after the project's own; Card the buttons of a card.
	Project []manifest.Screen
	Card    []manifest.Action
}

// shipped are the screens deck ships: the cockpit, settings, the journal and the page of a process; they are
// checked once the commands they read are known.
var shipped = sync.OnceValue(func() (d shippedScreens) {
	var err error
	if d.Menu, d.Screens, d.Project, d.Card, err = manifest.DeckScreens(deckScreensJSON); err != nil {
		panic(err)
	}
	return d
})

func deckScreen(name string) *manifest.Screen {
	for i := range shipped().Screens {
		if shipped().Screens[i].Name == name {
			return &shipped().Screens[i]
		}
	}
	return nil
}

// projectScreen is a screen deck adds to every project whose manifest has none of that name.
func projectScreen(name string) *manifest.Screen {
	for i := range shipped().Project {
		if shipped().Project[i].Name == name {
			return &shipped().Project[i]
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
	for _, name := range shipped().Menu {
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
		for _, s := range shipped().Project {
			if s.Record == "" && m.Screen(s.Name) == nil {
				e.Screens = append(e.Screens, screenLink{Name: s.Name, Title: s.Title})
			}
		}
		out = append(out, e)
	}
	return out, nil
}

type screenGetIn struct {
	Project string `type:"project" opt:"" desc:"Project name; deck's own screens without one"`
	Screen  string `desc:"Screen name"`
	Record  int64  `opt:"" desc:"Record id of a record page"`
	Topic   string `opt:"" desc:"Event topic that changed"`
	Filter  string `type:"text" opt:"" desc:"Values the owner picked in block filters, as JSON: block id to field to value"`
}

// screenGet resolves a screen of a project, or of deck without one, into A2UI messages.
func (a *App) screenGet(ctx context.Context, act registry.Actor, in screenGetIn) (any, error) {
	req := screen.Request{Record: in.Record, Topic: in.Topic, Owner: act.Kind == registry.Owner, Card: shipped().Card}
	if strings.TrimSpace(in.Filter) != "" {
		if err := json.Unmarshal([]byte(in.Filter), &req.Filter); err != nil {
			return nil, registry.Invalid("deck: filter: a JSON object of block id to field to value: %v", err)
		}
	}
	if in.Project == "" {
		req.Screen = deckScreen(in.Screen)
		req.Project.WorkspaceID = act.Workspace
	} else {
		p, err := a.project(ctx, act, in.Project)
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
	out, err := screen.Render(ctx, a.Store, a.sources(act), req)
	if err != nil {
		return nil, registry.NotFound("deck: %v", err)
	}
	return out, nil
}

// screenSources read what a screen shows as the one who looks at it.
type screenSources struct {
	screens.Sources
	a   *App
	act registry.Actor
}

func (a *App) sources(act registry.Actor) screenSources {
	return screenSources{Sources: screens.Sources{Store: a.Store, Registry: a.Registry, Deck: screenDeck{a}}, a: a, act: act}
}

// Read calls a command that only reads with the project of a project screen and the values picked in filters
// that it takes.
func (s screenSources) Read(ctx context.Context, command string, input map[string]any, sc screen.Scope) (any, error) {
	c, err := s.a.Registry.Lookup(ctx, sc.Workspace, command)
	if err != nil {
		return nil, err
	}
	if !c.Reads {
		return nil, fmt.Errorf("%s is not a command that only reads", command)
	}
	types := paramTypes(c.Input)
	in := maps.Clone(input)
	if in == nil {
		in = map[string]any{}
	}
	if _, ok := types["project"]; ok && sc.Project != "" && in["project"] == nil {
		in["project"] = sc.Project
	}
	for k, v := range sc.Filter {
		switch types[k] {
		case "":
		case "integer":
			if n, err := strconv.ParseInt(v, 10, 64); err == nil {
				in[k] = n
			}
		case "boolean":
			in[k] = v == "true"
		default:
			in[k] = v
		}
	}
	raw, _ := json.Marshal(in)
	out, err := s.a.Registry.Invoke(ctx, s.act, command, raw)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return manifest.DecodeExact(b)
}

// paramTypes are the JSON types of the input fields of a command.
func paramTypes(schema json.RawMessage) map[string]string {
	var in struct {
		Properties map[string]struct{ Type any } `json:"properties"`
	}
	_ = json.Unmarshal(schema, &in)
	out := map[string]string{}
	for k, p := range in.Properties {
		out[k], _ = p.Type.(string)
		if out[k] == "" {
			out[k] = "any"
		}
	}
	return out
}

type screenDeck struct{ a *App }

func (d screenDeck) Waits(ctx context.Context, project int64) (map[int64][]int64, error) {
	return waitsOf(ctx, d.a.Store.Q, project)
}

func (d screenDeck) Chat(ctx context.Context, workspace int64, topic string) (any, error) {
	return d.a.Chat(ctx, workspace, topic)
}

// commandRow is a command the caller may call, with its project, its file fields and what runs it.
type commandRow struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Project     string   `json:"project"`
	Files       []string `json:"files"`
	Executor    string   `json:"executor"`
}

type commandsListIn struct {
	Project string `type:"project" opt:"" desc:"Only the commands of this project"`
}

// commandsList is what the caller may call: deck's commands and those of the projects it reaches.
func (a *App) commandsList(ctx context.Context, act registry.Actor, in commandsListIn) ([]commandRow, error) {
	pid, err := a.filter(ctx, act, in.Project)
	if err != nil {
		return nil, err
	}
	cmds, err := a.Registry.List(ctx, act)
	if err != nil {
		return nil, err
	}
	names, err := projectNames(ctx, a.Store.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	manifests := map[int64]*manifest.Manifest{}
	out := []commandRow{}
	for _, c := range cmds {
		if pid != 0 && c.ProjectID != pid {
			continue
		}
		r := commandRow{Name: c.Name, Title: c.Title, Description: c.Description, Project: names[c.ProjectID],
			Files: append([]string{}, process.FileFields(c.Input)...)}
		if c.ProjectID != 0 {
			m, ok := manifests[c.ProjectID]
			if !ok {
				if p, err := a.Store.Q.GetProject(ctx, c.ProjectID); err == nil {
					m, _ = docs.Manifest(ctx, a.Store.Q, p)
				}
				manifests[c.ProjectID] = m
			}
			if m != nil {
				if mc := m.Command(strings.TrimPrefix(c.Name, r.Project+".")); mc != nil {
					r.Executor = mc.Executor.Type
				}
			}
		}
		out = append(out, r)
	}
	return out, nil
}

func (a *App) screensCommands() []*registry.Command {
	return []*registry.Command{
		registry.Def("menu", "Menu",
			"Lists the screens to navigate: deck's own, then each project's menu.",
			registry.OwnerSide, a.menu),
		registry.Def("screen", "Screen",
			"Resolves a stored screen with its data into A2UI messages; with a topic, only the data of the blocks it changes.",
			registry.OwnerSide, a.screenGet),
		registry.Read("commands_list", "Commands",
			"Lists the commands the caller may call, deck's and those of projects, with their file fields and what runs them.",
			registry.OwnerSide, a.commandsList),
	}
}
