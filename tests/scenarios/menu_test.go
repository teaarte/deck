package scenarios

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"deck2/internal/health"
	"deck2/internal/provider"
)

type menuView struct {
	Project string
	Icon    string
	Screens []struct{ Name, Title, Icon string }
	Running int64
	Asks    int64
	Errors  int64
}

func (e *Env) menuEntry(project string) menuView {
	e.T.Helper()
	var menu []menuView
	e.must("deck.menu", nil, &menu)
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
	manifest := strings.Replace(webNotes, `{"version": 10,`, `{"version": 10, "icon": "rocket",`, 1)
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
	e.must("deck.menu", nil, &menu)
	if menu[0].Screens[0].Icon != "calendar-time" {
		t.Fatalf("deck's own screens carry their icons: %+v", menu[0].Screens)
	}
	if m := e.menuEntry("web"); m.Icon != "rocket" || m.Screens[0].Icon != "chart-line" || m.Running+m.Asks+m.Errors != 0 {
		t.Fatalf("a quiet project carries the icons of its manifest and no status: %+v", m)
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
