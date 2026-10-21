package scenarios

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"deck2/internal/health"
	"deck2/internal/provider"
)

type menuView struct {
	Project string
	Icon    string
	Screens []struct{ Name, Title, Icon string }
	Open    *struct{ Name string }
	Running int64
	Asks    int64
	Errors  int64
}

func (e *Env) menuEntry(project string) menuView {
	e.T.Helper()
	var menu []menuView
	e.menuList(nil, true, &menu)
	for _, m := range menu {
		if m.Project == project {
			return m
		}
	}
	e.T.Fatalf("no %q in the menu: %+v", project, menu)
	return menuView{}
}

// TestMenuShowsIconsAndWhatEachProjectIsBusyWith: icons come from manifests and deck's screens, down to blocks and
// buttons, and each project carries its running processes, open questions and failing health rows.
func TestMenuShowsIconsAndWhatEachProjectIsBusyWith(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		switch c.command() {
		case "score":
			select {
			case <-release:
			case <-ctx.Done():
			}
			return provider.Result{Structured: json.RawMessage(`[]`)}
		default:
			c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{
				{"question": "Which one?", "header": "One", "options": []map[string]any{{"label": "a"}, {"label": "b"}}}}})
			return provider.Result{Structured: json.RawMessage(`{}`)}
		}
	})
	defer close(release)
	manifest := strings.Replace(webNotes, `{"version": 12,`, `{"version": 12, "icon": "rocket",`, 1)
	manifest = strings.Replace(manifest, `{"name": "main", "title": "Web",`, `{"name": "main", "title": "Web", "icon": "chart-line",`, 1)
	manifest = strings.Replace(manifest, `{"type": "table", "title": "Notes",`, `{"type": "table", "title": "Notes", "icon": "notes",`, 1)
	manifest = strings.Replace(manifest, `{"label": "Open", "screen": "note"}`, `{"label": "Open", "icon": "arrow-right", "screen": "note"}`, 1)
	p := e.createProject("web", manifest)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>x</p>"}, nil)
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": jsonl(map[string]any{"title": "alpha"})}, nil)
	drawn, _ := json.Marshal(e.screenMessages("web", "main", nil))
	for _, want := range []string{`"icon":"chart-line"`, `"icon":"notes"`, `"icon":"arrow-right"`} {
		if !strings.Contains(string(drawn), want) {
			t.Fatalf("the screen, its block and its button carry their icons: no %s in %s", want, drawn)
		}
	}

	var menu []menuView
	e.menuOf(&menu)
	if menu[0].Screens[0].Icon != "calendar-time" {
		t.Fatalf("deck's own screens carry their icons: %+v", menu[0].Screens)
	}
	if m := e.menuEntry("web"); m.Icon != "rocket" || m.Screens[0].Icon != "chart-line" || m.Running+m.Asks+m.Errors != 0 ||
		m.Open == nil || m.Open.Name != "main" {
		t.Fatalf("a quiet project carries the icons of its manifest and no status, and its one screen opens from its header: %+v", m)
	}
	if code, out := e.call("deck.manifest_save", map[string]any{"project": "web", "manifest": strings.Replace(manifest, `"rocket"`, `"Rocket Ship"`, 1)}); code != 422 {
		t.Fatalf("an icon is a name of the icon set: %d %s", code, out)
	}

	e.start("web", "score", map[string]any{"title": "x"})
	e.waitFor("a running process in the menu", func() bool { return e.menuEntry("web").Running == 1 })
	e.start("web", "tell", nil)
	e.waitAsk()
	if m := e.menuEntry("web"); m.Asks != 1 || m.Running != 2 {
		t.Fatalf("the menu counts the open question and both running processes, the one that asks too: %+v", m)
	}
	health.Write(context.Background(), e.App.Store, health.Of{Workspace: 1, Project: p}, "server:x", "error", "down")
	if m := e.menuEntry("web"); m.Errors != 1 {
		t.Fatalf("the menu counts a failing health row of the project: %+v", m)
	}
}

// TestTheCockpitTheHeroAndTheMenuCountAlike: what waits, runs and fails is one count for the tiles of the cockpit,
// the pulse the hero says and the badges of the menu, a check deck works out and a run that waits included.
func TestTheCockpitTheHeroAndTheMenuCountAlike(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	defer close(release)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.Result{Text: "cut"}
	})
	e.createProject("clips", strings.Replace(cutModule, `{"version": 12,`, `{"version": 12, "tools": {"http": [
    {"name": "photos", "description": "d", "method": "GET", "url": "https://example.com/p", "class": "internal", "headers": {"X-Key": "$secret.pexels"}}]},`, 1))
	e.waitState(e.start("clips", "cut", map[string]any{"clip": "a"}), "running")
	var m struct {
		Pulse struct {
			Asks, Running, Errors int64
			State                 string
		}
		Projects []menuView
	}
	e.must("deck.menu", nil, &m)
	h := e.hero("")
	lines := []string{}
	for _, l := range h.Lines {
		lines = append(lines, l.Text)
	}
	if m.Pulse.Running != 1 || m.Pulse.Errors != 1 || m.Pulse.Asks != 0 || m.Pulse.State != "errors" ||
		!slices.Equal(lines, []string{"Failing checks: 1", "Running: 1"}) || len(m.Projects) != 1 || m.Projects[0].Running != 1 || m.Projects[0].Errors != 1 {
		t.Fatalf("the pulse, the top of the cockpit and the menu count one run and one unset secret: %+v; %v", m, lines)
	}
}

// TestThePulseSaysTalkingWhileOnlyTheMasterWorks: a reply of the master alone is talking, Deki's dots; a run of a
// module beside it is running.
func TestThePulseSaysTalkingWhileOnlyTheMasterWorks(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	defer close(release)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.Result{Text: "done"}
	})
	state := func() string {
		var m struct{ Pulse struct{ State string } }
		e.must("deck.menu", nil, &m)
		return m.Pulse.State
	}
	e.say(0, "plan my week")
	e.waitFor("the master at work", func() bool { return state() == "talking" })
	e.createProject("clips", cutModule)
	e.waitState(e.start("clips", "cut", map[string]any{"clip": "a"}), "running")
	if got := state(); got != "running" {
		t.Fatalf("a run of a module beside the master is running: %s", got)
	}
}

// side is the projects of the side list of deck.menu by name, in their order, and the master's project.
func (e *Env) side() ([]string, string) {
	e.T.Helper()
	var menu struct {
		Projects []struct{ Project string }
		Master   *struct{ Project string }
	}
	e.must("deck.menu", map[string]any{}, &menu)
	names := []string{}
	for _, p := range menu.Projects {
		names = append(names, p.Project)
	}
	if menu.Master == nil {
		return names, ""
	}
	return names, menu.Master.Project
}

// TestAPausedProjectLeavesTheMenu: a paused project is not in the side list, its resume brings it back, and the master
// stays at the foot, paused or not.
func TestAPausedProjectLeavesTheMenu(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	e.createProject("alpha", notesManifest)
	e.createProject("beta", plainManifest)
	e.must("deck.project_pause", map[string]any{"project": "alpha", "paused": true}, nil)
	e.must("deck.project_pause", map[string]any{"project": "master", "paused": true}, nil)
	if names, master := e.side(); !slices.Equal(names, []string{"beta"}) || master != "master" {
		t.Fatalf("a paused project leaves the side list, the master stays: %v %q", names, master)
	}
	e.must("deck.project_pause", map[string]any{"project": "alpha", "paused": false}, nil)
	if names, _ := e.side(); !slices.Equal(names, []string{"alpha", "beta"}) {
		t.Fatalf("a resumed project is in the side list again: %v", names)
	}
}

// TestTheOwnerOrdersTheMenu: deck.menu_order sets the order of the side list; a new project goes last, a paused one
// keeps its place for its resume, and names of no project are dropped.
func TestTheOwnerOrdersTheMenu(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	for _, name := range []string{"alpha", "beta", "gamma"} {
		e.createProject(name, plainManifest)
	}
	if names, _ := e.side(); !slices.Equal(names, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("without an order the side list goes by name: %v", names)
	}
	var set struct{ Projects []string }
	e.must("deck.menu_order", map[string]any{"projects": []string{"gamma", "nowhere", "alpha", "beta", "master"}}, &set)
	if !slices.Equal(set.Projects, []string{"gamma", "alpha", "beta"}) {
		t.Fatalf("the order keeps the projects of the side list: %v", set.Projects)
	}
	if names, _ := e.side(); !slices.Equal(names, []string{"gamma", "alpha", "beta"}) {
		t.Fatalf("the menu goes in the owner's order: %v", names)
	}
	e.createProject("delta", plainManifest)
	if names, _ := e.side(); !slices.Equal(names, []string{"gamma", "alpha", "beta", "delta"}) {
		t.Fatalf("a new project goes last: %v", names)
	}
	e.must("deck.project_pause", map[string]any{"project": "alpha", "paused": true}, nil)
	names, _ := e.side()
	if !slices.Equal(names, []string{"gamma", "beta", "delta"}) {
		t.Fatalf("a paused project is not in the ordered menu: %v", names)
	}
	e.must("deck.menu_order", map[string]any{"projects": []string{"delta", "gamma", "beta"}}, nil)
	e.must("deck.project_pause", map[string]any{"project": "alpha", "paused": false}, nil)
	if names, _ := e.side(); !slices.Equal(names, []string{"delta", "gamma", "alpha", "beta"}) {
		t.Fatalf("a resumed project comes back after the one it followed: %v", names)
	}
	e.must("deck.project_delete", map[string]any{"project": "gamma"}, nil)
	if names, _ := e.side(); !slices.Equal(names, []string{"delta", "alpha", "beta"}) {
		t.Fatalf("a deleted project leaves the order: %v", names)
	}
}
