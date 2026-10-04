package app

import (
	"cmp"
	"context"
	"deck2/internal/app/access"
	"deck2/internal/app/board"
	_ "embed"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"

	"deck2/internal/app/screens"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/process"
	"deck2/internal/registry"
	"deck2/internal/screen"
	"deck2/internal/store/db"
)

//go:embed screens.json
var deckScreensJSON []byte

type shippedScreens struct {
	// I18n translates deck's own screens, by language and then by text.
	I18n    map[string]map[string]string
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
	var words struct {
		I18n map[string]map[string]string `json:"i18n"`
	}
	if err := json.Unmarshal(deckScreensJSON, &words); err != nil {
		panic(err)
	}
	d.I18n = words.I18n
	return d
})

// words are the translations into a language of deck's screens and then of the manifest, which wins.
func words(lang string, m *manifest.Manifest) map[string]string {
	if lang == "" {
		return nil
	}
	out := maps.Clone(shipped().I18n[lang])
	if m != nil {
		if out == nil {
			out = map[string]string{}
		}
		maps.Copy(out, m.I18n[lang])
	}
	return out
}

// tr is a text in a language by words, or the text itself.
func tr(ws map[string]string, s string) string {
	if t := ws[s]; t != "" {
		return t
	}
	return s
}

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
	Icon    string       `json:"icon,omitempty"`
	Screens []screenLink `json:"screens"`
	// Extra are the screens deck adds to every project, shown apart from the project's own.
	Extra []screenLink `json:"extra,omitempty"`
	// Running, Asks and Errors are the project's running processes, open questions and failing health rows.
	Running int64 `json:"running,omitempty"`
	Asks    int64 `json:"asks,omitempty"`
	Errors  int64 `json:"errors,omitempty"`
}

type screenLink struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
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
		out[0].Screens = append(out[0].Screens, screenLink{Name: name, Title: tr(deckWords, s.Title), Icon: s.Icon})
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
	for _, p := range ps {
		m, err := docs.Manifest(ctx, a.Store.Q, p)
		if err != nil {
			continue
		}
		r := pulse[p.ID]
		ws := words(in.Lang, m)
		e := menuEntry{Project: p.Name, Title: tr(ws, cmp.Or(m.Title, p.Name)), Icon: m.Icon, Screens: []screenLink{}, Running: r.Running, Asks: r.Asks, Errors: r.Errors}
		names := m.Menu
		if len(names) == 0 {
			for _, s := range m.Screens {
				if s.Record == "" {
					names = append(names, s.Name)
				}
			}
		}
		for _, n := range names {
			s := m.Screen(n)
			e.Screens = append(e.Screens, screenLink{Name: n, Title: tr(ws, s.Title), Icon: s.Icon})
		}
		for _, s := range shipped().Project {
			if s.Record == "" && m.Screen(s.Name) == nil {
				e.Extra = append(e.Extra, screenLink{Name: s.Name, Title: tr(ws, s.Title), Icon: s.Icon})
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
	Changed string `opt:"" desc:"Project the topic changed in; blocks that read no part of it are left out"`
	Filter  string `type:"json" opt:"" desc:"Values the owner picked in block filters, as JSON: block id to field to value"`
	Params  string `type:"json" opt:"" desc:"Parameters of the screen from its address, as a JSON object of names to text: $param.<name> in the screen"`
	Lang    string `opt:"" desc:"Language of the texts of the screen, such as ru; as written without one"`
}

// screenGet resolves a screen of a project, or of deck without one, into A2UI messages.
func (a *App) screenGet(ctx context.Context, act registry.Actor, in screenGetIn) (any, error) {
	req := screen.Request{Record: in.Record, Topic: in.Topic, Changed: in.Changed, Card: shipped().Card}
	if strings.TrimSpace(in.Filter) != "" {
		if err := json.Unmarshal([]byte(in.Filter), &req.Filter); err != nil {
			return nil, registry.Invalid("deck: filter: a JSON object of block id to field to value: %v", err)
		}
	}
	if strings.TrimSpace(in.Params) != "" {
		var ps map[string]any
		if err := json.Unmarshal([]byte(in.Params), &ps); err != nil {
			return nil, registry.Invalid("deck: params: a JSON object of names to text: %v", err)
		}
		req.Params = make(map[string]string, len(ps))
		for k, v := range ps {
			req.Params[k] = fmt.Sprint(v)
		}
	}
	if in.Project == "" {
		req.Screen = deckScreen(in.Screen)
		req.Project.WorkspaceID = act.Workspace
	} else {
		p, err := access.Project(ctx, a.Store.Q, act, in.Project)
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
	req.Words = words(in.Lang, req.Manifest)
	out, err := screen.Render(ctx, a.Store, a.sources(act), req)
	if err != nil {
		return nil, registry.NotFound("deck: %v", err)
	}
	return out, nil
}

// checkScreens resolves each screen of a manifest as act on the data there is now, a record page on the newest
// record, and names the blocks that fail.
func (a *App) checkScreens(ctx context.Context, act registry.Actor, p db.Project, m *manifest.Manifest) []string {
	var out []string
	for i := range m.Screens {
		sc := &m.Screens[i]
		req := screen.Request{Project: p, Manifest: m, Screen: sc, Card: shipped().Card}
		switch sc.Record {
		case "":
		case manifest.DocumentRecord:
			continue
		default:
			rec, err := a.Store.Q.NewestRecord(ctx, db.NewestRecordParams{ProjectID: p.ID, Collection: sc.Record})
			if err != nil {
				continue
			}
			req.Record = rec.ID
		}
		errs, err := screen.Check(ctx, a.Store, a.sources(act), req)
		if err != nil {
			errs = []string{err.Error()}
		}
		for _, e := range errs {
			out = append(out, fmt.Sprintf("screen %q: %s", sc.Name, e))
		}
	}
	return out
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
	in := map[string]any{}
	for k, v := range input {
		if t, ok := v.(string); ok {
			v = manifest.Typed(t, types[k])
		}
		in[k] = v
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
	return board.Waits(ctx, d.a.Store.Q, project)
}

// commandRow is a command the caller may call, with its project, its file fields and what runs it: an executor
// of a command of the manifest, the provider an agent command runs on, or the kind of a tool, http or mcp.
type commandRow struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Project     string   `json:"project"`
	Files       []string `json:"files"`
	Executor    string   `json:"executor"`
	Provider    string   `json:"provider"`
	Tool        string   `json:"tool"`
	// Providers are the providers of the workspace an agent command may be set to run on.
	Providers []string `json:"providers,omitempty"`
}

type commandDescribeIn struct {
	Command string `desc:"Command name, such as deck.provider_add or project.command"`
}

type commandSpec struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Class       string          `json:"class"`
	Callers     []string        `json:"callers"`
	Input       json.RawMessage `json:"input"`
	Result      json.RawMessage `json:"result,omitempty"`
}

// commandDescribe tells what a command takes whoever may call it, so the master can say what to fill in.
func (a *App) commandDescribe(ctx context.Context, act registry.Actor, in commandDescribeIn) (commandSpec, error) {
	c, err := a.Registry.Lookup(ctx, act.Workspace, in.Command)
	if err != nil {
		return commandSpec{}, err
	}
	return commandSpec{Name: c.Name, Title: c.Title, Description: c.Description, Class: c.Class, Callers: c.Callers,
		Input: c.Input, Result: c.Result}, nil
}

type commandsListIn struct {
	Project string `type:"project" opt:"" desc:"Only the commands of this project"`
}

// commandsList is what the caller may call: deck's commands and those of the projects it reaches.
func (a *App) commandsList(ctx context.Context, act registry.Actor, in commandsListIn) ([]commandRow, error) {
	pid, err := access.Filter(ctx, a.Store.Q, act, in.Project)
	if err != nil {
		return nil, err
	}
	cmds, err := a.Registry.List(ctx, act)
	if err != nil {
		return nil, err
	}
	names, err := access.Names(ctx, a.Store.Q, act.Workspace)
	if err != nil {
		return nil, err
	}
	manifests := map[int64]*manifest.Manifest{}
	var all []string
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
			short := strings.TrimPrefix(c.Name, r.Project+".")
			switch {
			case m == nil:
			case m.Command(short) != nil:
				mc := m.Command(short)
				r.Executor = mc.Executor.Type
				if pv, err := a.For(ctx, c.ProjectID, mc); err == nil && r.Executor == "agent" {
					r.Provider = pv.Name()
				}
				if r.Executor == "agent" {
					if all == nil {
						all = []string{}
						if pvs, err := a.Providers.All(ctx, act.Workspace); err == nil {
							for _, pv := range pvs {
								all = append(all, pv.Name())
							}
						}
					}
					r.Providers = all
				}
			case slices.ContainsFunc(m.Tools.HTTP, func(t manifest.HTTPTool) bool { return t.Name == short }):
				r.Tool = "http"
			case c.Fingerprint != nil:
				r.Tool = "mcp"
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
			registry.OwnerSide, a.commandsList).On("projects"),
		registry.Read("command_describe", "Describe command",
			"Gives the parameters of any command of the workspace, also of one only the owner calls, such as the forms of Settings: its input and result schemas, class and callers.",
			registry.OwnerSide, a.commandDescribe).On(registry.Static),
	}
}
