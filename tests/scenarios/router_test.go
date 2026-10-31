package scenarios

import (
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// routedManifest has a dialog with a form its address fills, and rows whose button opens it with their title.
var routedManifest = strings.Replace(harness.WebNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "params": {"title": "string"}, "blocks": [
      {"type": "dialog", "name": "rescore", "title": "Score again", "icon": "refresh", "primary": true, "blocks": [
        {"type": "form", "title": "Score", "command": "score", "values": {"title": "=param.title", "n": "3"}}]},
      {"type": "actions", "title": "More", "icon": "dots", "actions": [{"label": "Tell", "command": "tell"}]},
      {"type": "table", "title": "Again", "source": {"collection": "notes"}, "columns": [{"label": "Title", "bind": "title"}],
        "actions": [{"label": "Again", "screen": "main", "params": {"title": "=title", "dialog": "rescore"}}]},`, 1)

// TestScreenParamsFillFormsAndTravelWithButtons: the address of a screen carries parameters; a form takes them
// as its values in the type of its fields, and a button to a screen takes them from its row.
func TestScreenParamsFillFormsAndTravelWithButtons(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	e.Project("web", routedManifest)
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": harness.Jsonl(map[string]any{"title": "alpha"})}, nil)

	data := e.ScreenData("web", "main", map[string]any{"params": `{"title": "alpha"}`, "dialog": "rescore"})
	var form struct {
		Value struct{ Values map[string]any }
	}
	for _, d := range data {
		if v, ok := d["value"]; ok && strings.Contains(string(v), `"commands"`) && strings.Contains(string(v), `"values"`) {
			raw, _ := json.Marshal(d)
			_ = json.Unmarshal(raw, &form)
		}
	}
	if form.Value.Values["title"] != "alpha" || form.Value.Values["n"] != float64(3) {
		t.Fatalf("a form takes its values from the address and the manifest, typed by its fields: %+v", form.Value.Values)
	}
	drawn, _ := json.Marshal(e.ScreenMessages("web", "main", nil))
	if !strings.Contains(string(drawn), `"params":{"dialog":"rescore","title":"alpha"}`) {
		t.Fatalf("a button to a screen carries the parameters of its row: %s", drawn)
	}
	if !strings.Contains(string(drawn), `"component":"Modal"`) || !strings.Contains(string(drawn), `"name":"rescore"`) {
		t.Fatalf("a dialog is drawn with its name: %s", drawn)
	}
	if !strings.Contains(string(drawn), `"dialogs":["b0"]`) {
		t.Fatalf("a dialog stands beside the title of the screen: %s", drawn)
	}
	if code, out := e.Call("deck.project_create", map[string]any{"name": "menus", "manifest": strings.Replace(routedManifest, `"icon": "dots", "actions"`, `"icon": "dots", "menu": true, "actions"`, 1)}); code != 422 {
		t.Fatalf("a group of buttons has no menu of its own: %d %s", code, out)
	}
	without := e.ScreenData("web", "main", nil)
	for _, d := range without {
		if v := string(d["value"]); strings.Contains(v, `"commands"`) && strings.Contains(v, `"title":"`) && strings.Contains(v, `"values"`) && strings.Contains(v, `"alpha"`) {
			t.Fatalf("without the parameter the field is left to the owner: %s", v)
		}
	}
	if code, out := e.Call("deck.screen", map[string]any{"project": "web", "screen": "main", "params": `[1]`}); code != 422 {
		t.Fatalf("params are an object of names to text: %d %s", code, out)
	}
}

// TestValuesIconsAndDialogNamesAreCheckedOnSave: a form fills only fields of its input, from fields the record
// has; the icon of a line is an expression; the dialogs of a screen have their own names.
func TestValuesIconsAndDialogNamesAreCheckedOnSave(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	cases := []struct{ name, old, new, want string }{
		{"values field", `"values": {"title": "=param.title", "n": "3"}`, `"values": {"titel": "=param.title"}`, `no field "titel"`},
		{"undeclared param", `"values": {"title": "=param.title", "n": "3"}`, `"values": {"title": "=param.name"}`, `no param "name"`},
		{"old param", `"values": {"title": "=param.title", "n": "3"}`, `"values": {"title": "$param.title"}`, `=param.title`},
		{"param type", `"params": {"title": "string"}`, `"params": {"title": "text"}`, `params`},
		{"record field", `"values": {"title": "=title"}`, `"values": {"title": "=name"}`, `unknown field "name"`},
		{"item icon", `{"type": "actions", "title": "More"`, `{"type": "list", "title": "Lines", "source": {"collection": "notes"}, "item": {"title": "title", "icon": "shout(title)"}},
      {"type": "actions", "title": "More"`, `unknown function "shout"`},
		{"dialog name", `{"type": "actions", "title": "More"`, `{"type": "dialog", "name": "rescore", "title": "Twice", "blocks": [{"type": "form", "command": "tell"}]},
      {"type": "actions", "title": "More"`, `dialog "rescore": duplicate`},
	}
	for i, c := range cases {
		base := strings.Replace(routedManifest, `{"type": "form", "title": "Edit", "command": "deck.record_update"}`,
			`{"type": "form", "title": "Edit", "command": "deck.record_update"}, {"type": "form", "title": "Again", "command": "score", "values": {"title": "=title"}}`, 1)
		m := strings.Replace(base, c.old, c.new, 1)
		if m == base {
			t.Fatalf("%s: fixture replacement did not apply", c.name)
		}
		code, out := e.Call("deck.project_create", map[string]any{"name": fmt.Sprintf("web%d", i), "manifest": m})
		if code != 422 || !strings.Contains(string(out), c.want) {
			t.Errorf("%s: want 422 containing %q, got %d %s", c.name, c.want, code, out)
		}
	}
}

// TestAButtonGivesOnlyTheParamsOfItsScreen: a move to a screen gives the params that screen declares and the
// client's tab and dialog; a typo fails the save, while a module saved with one still reads and is warned of.
func TestAButtonGivesOnlyTheParamsOfItsScreen(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	typo := strings.Replace(routedManifest, `"params": {"title": "=title", "dialog": "rescore"}`, `"params": {"titel": "=title", "dialog": "rescore"}`, 1)
	if typo == routedManifest {
		t.Fatal("fixture replacement did not apply")
	}
	code, out := e.Call("deck.project_create", map[string]any{"name": "web", "manifest": typo})
	if code != 422 || !strings.Contains(string(out), `param "titel"`) || !strings.Contains(string(out), "title") {
		t.Fatalf("an unknown param of a move to a screen fails the save with the params it takes: %d %s", code, out)
	}
	e.Project("web", routedManifest)
	doc := `(SELECT id FROM document WHERE name = 'manifest.json' AND project_id = (SELECT id FROM project WHERE name = 'web'))`
	if _, err := e.App.Store.DB.Exec(`INSERT INTO document_revision (document_id, n, text, author) VALUES (`+doc+`, 2, ?, 'old deck');
		UPDATE document SET current = 2 WHERE id = `+doc, typo); err != nil {
		t.Fatal(err)
	}
	e.ScreenMessages("web", "main", nil)
	var got struct{ Warnings []string }
	e.Must("deck.manifest_get", map[string]any{"project": "web"}, &got)
	if len(got.Warnings) != 1 || !strings.Contains(got.Warnings[0], `param "titel"`) {
		t.Fatalf("a saved module with the typo reads, and manifest_get warns of it: %+v", got)
	}
}

// TestAModuleSavedBeforeANewCheckStillLoads: a check newer than a stored manifest (param.<name> in a text) only
// warns on read, so the module stays in the menu; a save refuses it.
func TestAModuleSavedBeforeANewCheckStillLoads(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("ideas", ideasManifest)
	old := strings.Replace(ideasManifest, `{"type": "table", "title": "Best ideas",`,
		`{"type": "table", "title": "Best ideas", "description": "Open with param.day to see a day.",`, 1)
	if old == ideasManifest {
		t.Fatal("fixture replacement did not apply")
	}
	doc := `(SELECT id FROM document WHERE name = 'manifest.json' AND project_id = (SELECT id FROM project WHERE name = 'ideas'))`
	if _, err := e.App.Store.DB.Exec(`INSERT INTO document_revision (document_id, n, text, author) VALUES (`+doc+`, 2, ?, 'old deck');
		UPDATE document SET current = 2 WHERE id = `+doc, old); err != nil {
		t.Fatal(err)
	}
	var menu []struct{ Project string }
	e.MenuOf(&menu)
	if !slices.ContainsFunc(menu, func(m struct{ Project string }) bool { return m.Project == "ideas" }) {
		t.Fatalf("the module stays in the menu: %+v", menu)
	}
	if code, out := e.Call("deck.screen", map[string]any{"project": "ideas", "screen": "main"}); code != 200 {
		t.Fatalf("its screen shows: %d %s", code, out)
	}
	var got struct{ Warnings []string }
	e.Must("deck.manifest_get", map[string]any{"project": "ideas"}, &got)
	if w := strings.Join(got.Warnings, "|"); !strings.Contains(w, `param "day"`) {
		t.Fatalf("manifest_get warns of it: %+v", got)
	}
	e.Refused("deck.manifest_save", map[string]any{"project": "ideas", "manifest": old}, 422, `param "day"`)
}
