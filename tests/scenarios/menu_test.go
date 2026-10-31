package scenarios

import (
	"context"
	"deck2/internal/health"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func menuEntry(e *harness.Env, project string) harness.MenuView {
	e.T.Helper()
	var menu []harness.MenuView
	e.MenuList(nil, true, &menu)
	for _, m := range menu {
		if m.Project == project {
			return m
		}
	}
	e.T.Fatalf("no %q in the menu: %+v", project, menu)
	return harness.MenuView{}
}

// TestMenuShowsIconsAndWhatEachProjectIsBusyWith: icons come from manifests and deck's screens, down to blocks and
// buttons, and each project carries its running processes, open questions and failing health rows.
func TestMenuShowsIconsAndWhatEachProjectIsBusyWith(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch c.Command() {
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
	}))

	defer close(release)
	manifest := strings.Replace(harness.WebNotes, `{"version": 13,`, `{"version": 13, "icon": "rocket",`, 1)
	manifest = strings.Replace(manifest, `{"name": "main", "title": "Web",`, `{"name": "main", "title": "Web", "icon": "chart-line",`, 1)
	manifest = strings.Replace(manifest, `{"type": "table", "title": "Notes",`, `{"type": "table", "title": "Notes", "icon": "notes",`, 1)
	manifest = strings.Replace(manifest, `{"label": "Open", "screen": "note"}`, `{"label": "Open", "icon": "arrow-right", "screen": "note"}`, 1)
	p := e.Project("web", manifest)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>x</p>"}, nil)
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": harness.Jsonl(map[string]any{"title": "alpha"})}, nil)
	drawn, _ := json.Marshal(e.ScreenMessages("web", "main", nil))
	for _, want := range []string{`"icon":"chart-line"`, `"icon":"notes"`, `"icon":"arrow-right"`} {
		if !strings.Contains(string(drawn), want) {
			t.Fatalf("the screen, its block and its button carry their icons: no %s in %s", want, drawn)
		}
	}

	var menu []harness.MenuView
	e.MenuOf(&menu)
	if menu[0].Screens[0].Icon != "calendar-time" {
		t.Fatalf("deck's own screens carry their icons: %+v", menu[0].Screens)
	}
	if m := menuEntry(e, "web"); m.Icon != "rocket" || m.Screens[0].Icon != "chart-line" || m.Running+m.Asks+m.Errors != 0 ||
		m.Open == nil || m.Open.Name != "main" {
		t.Fatalf("a quiet project carries the icons of its manifest and no status, and its one screen opens from its header: %+v", m)
	}
	if code, out := e.Call("deck.manifest_save", map[string]any{"project": "web", "manifest": strings.Replace(manifest, `"rocket"`, `"Rocket Ship"`, 1)}); code != 422 {
		t.Fatalf("an icon is a name of the icon set: %d %s", code, out)
	}

	e.Start("web", "score", map[string]any{"title": "x"})
	e.Until("a running process in the menu", func() bool { return menuEntry(e, "web").Running == 1 })
	e.Start("web", "tell", nil)
	e.WaitAsk()
	if m := menuEntry(e, "web"); m.Asks != 1 || m.Running != 2 {
		t.Fatalf("the menu counts the open question and both running processes, the one that asks too: %+v", m)
	}
	health.Write(context.Background(), e.App.Store, health.Of{Workspace: 1, Project: p}, "server:x", "error", "down")
	if m := menuEntry(e, "web"); m.Errors != 1 {
		t.Fatalf("the menu counts a failing health row of the project: %+v", m)
	}
}

// TestTheCockpitTheHeroAndTheMenuCountAlike: what waits, runs and fails is one count for the line and the feed of
// Today and the badges of the tiles, a check deck works out and a run that waits included.
func TestTheCockpitTheHeroAndTheMenuCountAlike(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	defer close(release)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.Result{Text: "cut"}
	}))

	e.Project("clips", strings.Replace(cutModule, `{"version": 13,`, `{"version": 13, "tools": {"http": [
    {"name": "photos", "description": "d", "method": "GET", "url": "https://example.com/p", "class": "internal", "headers": {"X-Key": "$secret.pexels"}}]},`, 1))
	e.WaitState(e.Start("clips", "cut", map[string]any{"clip": "a"}), "running")
	var m struct {
		Pulse struct {
			Asks, Running, Errors int64
			State                 string
		}
		Projects []harness.MenuView
	}
	e.Must("deck.menu", nil, &m)
	h := today(e, "")
	var feed []struct{ Kind, Check string }
	e.Must("deck.feed_list", map[string]any{"failures": true}, &feed)
	if m.Pulse.Running != 1 || m.Pulse.Errors != 1 || m.Pulse.Asks != 0 || m.Pulse.State != "errors" || h.Line != "Nothing to decide. One failure is in the feed below." ||
		len(feed) != 1 || feed[0].Kind != "failure" || feed[0].Check == "" || len(m.Projects) != 1 || m.Projects[0].Running != 1 || m.Projects[0].Errors != 1 {
		t.Fatalf("the pulse, the line and the feed of Today and the menu count one run and one unset secret: %+v; %+v; %+v", m, h, feed)
	}
}

// TestThePulseSaysTalkingWhileOnlyTheMasterWorks: a reply of the master alone is talking, Deki's dots; a run of a
// module beside it is running.
func TestThePulseSaysTalkingWhileOnlyTheMasterWorks(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	defer close(release)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.Result{Text: "done"}
	}))

	state := func() string {
		var m struct{ Pulse struct{ State string } }
		e.Must("deck.menu", nil, &m)
		return m.Pulse.State
	}
	e.Say(0, "plan my week")
	e.Until("the master at work", func() bool { return state() == "talking" })
	e.Project("clips", cutModule)
	e.WaitState(e.Start("clips", "cut", map[string]any{"clip": "a"}), "running")
	if got := state(); got != "running" {
		t.Fatalf("a run of a module beside the master is running: %s", got)
	}
}

// TestAPausedProjectLeavesTheMenu: a paused project is not in the side list, its resume brings it back, and the master
// stays at the foot, paused or not.
func TestAPausedProjectLeavesTheMenu(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("alpha", harness.NotesManifest)
	e.Project("beta", harness.PlainManifest)
	e.Must("deck.project_pause", map[string]any{"project": "alpha", "paused": true}, nil)
	e.Must("deck.project_pause", map[string]any{"project": "master", "paused": true}, nil)
	if names, master := e.SideMenu(); !slices.Equal(names, []string{"beta"}) || master != "master" {
		t.Fatalf("a paused project leaves the side list, the master stays: %v %q", names, master)
	}
	e.Must("deck.project_pause", map[string]any{"project": "alpha", "paused": false}, nil)
	if names, _ := e.SideMenu(); !slices.Equal(names, []string{"alpha", "beta"}) {
		t.Fatalf("a resumed project is in the side list again: %v", names)
	}
}
