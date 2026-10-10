package app

import (
	"context"
	"deck2/internal/app/access"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"

	"deck2/internal/app/screens"
	"deck2/internal/docs"
	"deck2/internal/manifest"
	"deck2/internal/registry"
	"deck2/internal/screen"
	"deck2/internal/store/db"
)

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

type screenGetIn struct {
	Project string `type:"project" opt:"" desc:"Project name; deck's own screens without one"`
	Screen  string `desc:"Screen name"`
	Record  int64  `opt:"" desc:"Record id of a record page"`
	Topic   string `opt:"" desc:"Event topic that changed"`
	Changed string `opt:"" desc:"Project the topic changed in; blocks that read no part of it are left out"`
	Filter  string `type:"json" opt:"" desc:"Values the owner picked in block filters, as JSON: block id to field to value"`
	Params  string `type:"json" opt:"" desc:"Parameters of the screen from its address, as a JSON object of names to text: param.<name> in the screen, typed as it declares them"`
	Lang    string `opt:"" desc:"Language of the texts of the screen, such as ru; as written without one"`
}

// screenGet resolves a screen of a project, or of deck without one, into A2UI messages.
func (a *App) screenGet(ctx context.Context, act registry.Actor, in screenGetIn) (any, error) {
	req := screen.Request{Record: in.Record, Topic: in.Topic, Changed: in.Changed, Brief: act.Kind != registry.Owner}
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
		m, err := docs.For(ctx, a.Store.Q, act, p.ID)
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
	req.Words, req.Lang = words(in.Lang, req.Manifest), in.Lang
	out, errs, err := screen.Render(ctx, a.sources(act), req)
	if err != nil {
		return nil, registry.NotFound("deck: %v", err)
	}
	switch own := req.Manifest != nil && req.Manifest.Screen(in.Screen) != nil; {
	case own && len(errs) > 0:
		a.Failures.Screen(ctx, req.Project, req.Manifest.Revision, in.Screen, errs)
	case own:
		a.Failures.ScreenDrawn(ctx, req.Project, in.Screen)
	}
	return out, nil
}

// checkScreens resolves each screen of a manifest as act on the data there is now, a record page on the newest
// record, and names the blocks that fail.
func (a *App) checkScreens(ctx context.Context, act registry.Actor, p db.Project, m *manifest.Manifest) []string {
	var out []string
	for i := range m.Screens {
		sc := &m.Screens[i]
		req := screen.Request{Project: p, Manifest: m, Screen: sc}
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
		errs, err := screen.Check(ctx, a.sources(act), req)
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
	return screenSources{Sources: screens.Sources{Store: a.Store, Registry: a.Registry}, a: a, act: act}
}

// File is a file a media block of the project shows.
func (s screenSources) File(ctx context.Context, p db.Project, ref any) (map[string]any, error) {
	if p.ID == 0 {
		p.WorkspaceID = s.act.Workspace
	}
	return s.a.Files.File(ctx, p, ref)
}

// Read calls a command that only reads with the project of a project screen, the language and the values picked in
// filters that it takes, as the viewer or, for another project, with the rights of the project of the screen.
func (s screenSources) Read(ctx context.Context, command string, input map[string]any, sc screen.Scope) (any, error) {
	c, err := s.a.Registry.Lookup(ctx, sc.Workspace, command)
	if err != nil {
		return nil, err
	}
	if !c.Reads {
		return nil, fmt.Errorf("%s is not a command that only reads", command)
	}
	types := manifest.FieldTypes(c.Input)
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
	if _, ok := types["lang"]; ok && sc.Lang != "" && in["lang"] == nil {
		in["lang"] = sc.Lang
	}
	for k, v := range sc.Filter {
		if types[k] != "" {
			in[k] = manifest.Typed(v, types[k])
		}
	}
	act := s.act
	if sc.By != 0 {
		act = registry.Actor{Kind: registry.Agent, Workspace: act.Workspace, ProjectID: sc.By}
	}
	raw, _ := json.Marshal(in)
	out, err := s.a.Registry.Invoke(ctx, act, command, raw)
	if err != nil {
		return nil, err
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return manifest.DecodeExact(b)
}
