package scenarios

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// routedManifest has a dialog with a form its address fills, and rows whose button opens it with their title.
var routedManifest = strings.Replace(boardWebManifest, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "dialog", "name": "rescore", "title": "Score again", "icon": "refresh", "primary": true, "blocks": [
        {"type": "form", "title": "Score", "command": "score", "values": {"title": "$param.title", "n": "3"}}]},
      {"type": "actions", "title": "More", "icon": "dots", "menu": true, "actions": [{"label": "Tell", "command": "tell"}]},
      {"type": "table", "title": "Again", "source": {"collection": "notes"}, "columns": [{"label": "Title", "bind": "title"}],
        "actions": [{"label": "Again", "screen": "main", "params": {"title": "$record.title", "dialog": "rescore"}}]},`, 1)

// TestScreenParamsFillFormsAndTravelWithButtons: the address of a screen carries parameters; a form takes them
// as its values in the type of its fields, and a button to a screen takes them from its row.
func TestScreenParamsFillFormsAndTravelWithButtons(t *testing.T) {
	e := newEnv(t, boardWebAgent())
	e.createProject("web", routedManifest)
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": jsonl(map[string]any{"title": "alpha"})}, nil)

	data := e.screenData("web", "main", map[string]any{"params": `{"title": "alpha"}`})
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
	drawn, _ := json.Marshal(e.screenMessages("web", "main", nil))
	if !strings.Contains(string(drawn), `"params":{"dialog":"rescore","title":"alpha"}`) {
		t.Fatalf("a button to a screen carries the parameters of its row: %s", drawn)
	}
	if !strings.Contains(string(drawn), `"component":"dialog"`) || !strings.Contains(string(drawn), `"name":"rescore"`) {
		t.Fatalf("a dialog is drawn with its name: %s", drawn)
	}
	if !strings.Contains(string(drawn), `"dialogs":["b0","b1"]`) || !strings.Contains(string(drawn), `"menu":true`) {
		t.Fatalf("a dialog and a menu stand beside the title of the screen: %s", drawn)
	}
	without := e.screenData("web", "main", nil)
	for _, d := range without {
		if v := string(d["value"]); strings.Contains(v, `"commands"`) && strings.Contains(v, `"title":"`) && strings.Contains(v, `"values"`) && strings.Contains(v, `"alpha"`) {
			t.Fatalf("without the parameter the field is left to the owner: %s", v)
		}
	}
	if code, out := e.call("deck.screen", map[string]any{"project": "web", "screen": "main", "params": `[1]`}); code != 422 {
		t.Fatalf("params are an object of names to text: %d %s", code, out)
	}
}

// TestValuesIconsAndDialogNamesAreCheckedOnSave: a form fills only fields of its input, from fields the record
// has; the icon of a line is an expression; the dialogs of a screen have their own names.
func TestValuesIconsAndDialogNamesAreCheckedOnSave(t *testing.T) {
	e := newEnv(t, boardWebAgent())
	cases := []struct{ name, old, new, want string }{
		{"values field", `"values": {"title": "$param.title", "n": "3"}`, `"values": {"titel": "$param.title"}`, `no field "titel"`},
		{"record field", `"values": {"title": "$record.title"}`, `"values": {"title": "$record.name"}`, `unknown field "name"`},
		{"item icon", `{"type": "actions", "title": "More"`, `{"type": "list", "title": "Lines", "source": {"collection": "notes"}, "item": {"title": "title", "icon": "shout(title)"}},
      {"type": "actions", "title": "More"`, `unknown function "shout"`},
		{"dialog name", `{"type": "actions", "title": "More"`, `{"type": "dialog", "name": "rescore", "title": "Twice", "blocks": [{"type": "form", "command": "tell"}]},
      {"type": "actions", "title": "More"`, `dialog "rescore": duplicate`},
	}
	for i, c := range cases {
		base := strings.Replace(routedManifest, `{"type": "form", "title": "Edit", "command": "deck.record_update"}`,
			`{"type": "form", "title": "Edit", "command": "deck.record_update"}, {"type": "form", "title": "Again", "command": "score", "values": {"title": "$record.title"}}`, 1)
		m := strings.Replace(base, c.old, c.new, 1)
		if m == base {
			t.Fatalf("%s: fixture replacement did not apply", c.name)
		}
		code, out := e.call("deck.project_create", map[string]any{"name": fmt.Sprintf("web%d", i), "manifest": m})
		if code != 422 || !strings.Contains(string(out), c.want) {
			t.Errorf("%s: want 422 containing %q, got %d %s", c.name, c.want, code, out)
		}
	}
}
