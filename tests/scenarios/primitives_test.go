package scenarios

import (
	"strings"
	"testing"
)

// TestTheOwnerDeletesARecordFromAButtonOfItsModule: a row of the module's own collection or its record page may carry
// deck.record_delete, always asking first; elsewhere it is refused, and agents still may not delete.
func TestTheOwnerDeletesARecordFromAButtonOfItsModule(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	open := `{"label": "Open", "screen": "note"}`
	del := `{"label": "Delete", "command": "deck.record_delete"}`
	m := strings.Replace(webNotes, open, open+`, `+del, 1)
	m = strings.Replace(m, `{"type": "form", "title": "Edit", "command": "deck.record_update"}]}`,
		`{"type": "form", "title": "Edit", "command": "deck.record_update"}, {"type": "actions", "actions": [`+del+`]}]}`, 1)
	e.createProject("web", m)
	for _, bad := range []struct{ from, to, want string }{
		{del, `{"label": "Delete", "command": "deck.record_delete", "params": {"record": "=1"}}`, "record_delete takes the record of its row"},
		{`"actions": [{"label": "Mark", "command": "mark"},`, `"actions": [` + del + `, {"label": "Mark", "command": "mark"},`, "not on a custom block"},
	} {
		if code, out := e.call("deck.manifest_save", map[string]any{"project": "web", "manifest": strings.Replace(m, bad.from, bad.to, 1)}); code != 422 || !strings.Contains(string(out), bad.want) {
			t.Fatalf("%s is refused: %d %s", bad.to, code, out)
		}
	}
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "a"}` + "\n" + `{"title": "b"}`}, nil)
	rows := blockData[[]screenRow](t, e.screenBlock("web", "main", "Notes", nil)["rows"])
	var act map[string]any
	for _, a := range rows[0].Actions {
		if a["command"] == "deck.record_delete" {
			act = a
		}
	}
	params, _ := act["params"].(map[string]any)
	if act == nil || act["confirm"] != true || params["record"] == nil {
		t.Fatalf("the button deletes the record of its row and asks first: %+v", rows[0].Actions)
	}
	e.must("deck.record_delete", params, nil)
	if rows := blockData[[]screenRow](t, e.screenBlock("web", "main", "Notes", nil)["rows"]); len(rows) != 1 {
		t.Fatalf("the owner's press deletes the record: %+v", rows)
	}
}
