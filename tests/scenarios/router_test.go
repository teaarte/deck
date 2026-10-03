package scenarios

import (
	"encoding/json"
	"strings"
	"testing"
)

// routedManifest has a dialog with a form its address fills, and rows whose button opens it with their title.
var routedManifest = strings.Replace(boardWebManifest, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "dialog", "name": "rescore", "title": "Score again", "icon": "refresh", "primary": true, "blocks": [
        {"type": "form", "title": "Score", "command": "score", "values": {"title": "$param.title", "n": "3"}}]},
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
