package scenarios

import (
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestButtonsOfARecordPageFollowTheRecordAfterAnEvent(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(webAgent()))
	e.Project("notes", webManifest("http://127.0.0.1:1"))
	var up struct{ IDs []int64 }
	e.Must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes", "records": harness.Jsonl(map[string]any{"title": "alpha", "score": 3})}, &up)
	score := func(topic string) any {
		in := map[string]any{"record": up.IDs[0]}
		var block map[string]json.RawMessage
		if topic == "" {
			block = e.ScreenData("notes", "note", in)["b2"]
		} else {
			in["topic"] = topic
			for _, m := range e.ScreenMessages("notes", "note", in) {
				var dm struct {
					Path  string
					Value map[string]json.RawMessage
				}
				if json.Unmarshal(m["updateDataModel"], &dm) == nil && dm.Path == "/b2" {
					block = dm.Value
				}
			}
		}
		acts := harness.BlockData[[]map[string]any](t, block["value"])
		return acts[0]["params"].(map[string]any)["score"]
	}
	if got := fmt.Sprint(score("")); got != "3" {
		t.Fatalf("the button of the page takes the field of its record: %v", got)
	}
	e.Must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes", "records": harness.Jsonl(map[string]any{"title": "alpha", "score": 9})}, nil)
	if got := fmt.Sprint(score("collection")); got != "9" {
		t.Fatalf("after a change of the record its buttons are read again with the rest: %v", got)
	}
}

// TestAModuleButtonShowsWhereItHoldsAndTakesValuesFromTheRow: a button of a module screen shows only on the rows
// its when holds for, takes a param from an expression over the row, asks first or asks for a typed value.
func TestAModuleButtonShowsWhereItHoldsAndTakesValuesFromTheRow(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("notes", `{"version": 13,
  "collections": [{"name": "notes", "description": "Notes", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "score": {"type": "integer"}}, "required": ["title"]}}],
  "commands": [{"name": "touch", "title": "Touch", "description": "Touches a note.", "class": "internal", "result": {"type": "object"},
    "input": {"type": "object", "properties": {"note": {"type": "integer", "format": "record"}, "score": {"type": "string"}}, "required": ["note"]},
    "executor": {"type": "agent", "prompt": "Touch."}}],
  "screens": [{"name": "main", "title": "Notes", "blocks": [{"type": "table", "title": "Notes", "source": {"collection": "notes", "sort": "title"},
    "columns": [{"label": "Title", "bind": "title"}],
    "actions": [{"label": "Boost", "command": "touch", "params": {"note": "=_id", "score": "=string(score * 2)"}, "when": "score >= 5", "confirm": true},
      {"label": "Rate", "command": "touch", "params": {"note": "=_id"}, "input": "score"}]}]}]}`)
	e.Must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes",
		"records": harness.Jsonl(map[string]any{"title": "a", "score": 1}, map[string]any{"title": "b", "score": 7})}, nil)
	rows := harness.BlockData[[]harness.ScreenRow](t, e.ScreenData("notes", "main", nil)["b0"]["rows"])
	if len(rows) != 2 || len(rows[0].Actions) != 1 || rows[0].Actions[0]["label"] != "Rate" || rows[0].Actions[0]["input"] != "score" {
		t.Fatalf("a button whose when does not hold is not on the row; the other asks for its input: %+v", rows)
	}
	boost := rows[1].Actions[0]
	params, _ := boost["params"].(map[string]any)
	if boost["label"] != "Boost" || boost["confirm"] != true || boost["command"] != "notes.touch" || params["score"] != "14" || params["note"] != rows[1].ID {
		t.Fatalf("a button takes a param from an expression over the row and asks first: %+v", boost)
	}
	var cur struct{ Manifest string }
	e.Must("deck.manifest_get", map[string]any{"project": "notes"}, &cur)
	if code, out := e.Call("deck.manifest_save", map[string]any{"project": "notes", "manifest": strings.Replace(cur.Manifest,
		`"=string(score * 2)"`, `"=string(scor * 2)"`, 1)}); code != 422 || !strings.Contains(string(out), `param "score": unknown field "scor"`) {
		t.Fatalf("an expression of a param reads fields of the row: %d %s", code, out)
	}
}

// TestUndoOfAModuleButtonIsACallOfItsOwn: a module's button takes back its call by a command of the project; undo
// stands in for a yes and takes nothing the owner types.
func TestUndoOfAModuleButtonIsACallOfItsOwn(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	with := func(undo string) string {
		return strings.Replace(harness.WebNotes, `"actions": [{"label": "Open", "screen": "note"}]`,
			`"actions": [{"label": "Open", "screen": "note"}, {"label": "Mark", "command": "mark"`+undo+`}]`, 1)
	}
	for undo, want := range map[string]string{
		`, "confirm": true, "undo": {"command": "tag"}`:                 "asks no yes first",
		`, "undo": {"command": "tag", "input": "tag"}`:                  "a command with params, nothing else",
		`, "undo": {"screen": "note"}`:                                  "a command with params, nothing else",
		`, "undo": {"command": "tag", "until": "2026-01-01T00:00:00Z"}`: "a command with params, nothing else",
		`, "undo": {"command": "nope"}`:                                 "nope",
	} {
		e.Refused("deck.project_create", map[string]any{"name": "bad", "manifest": with(undo)}, 422, want)
	}
	e.Project("web", with(`, "undo": {"command": "tag", "params": {"tag": "=title"}}`))
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "alpha"}`}, nil)
	found := false
	for _, v := range e.ScreenData("web", "main", nil) {
		for _, raw := range v {
			found = found || strings.Contains(string(raw), `"undo":{"command":"web.tag","params":{"tag":"alpha"}`)
		}
	}
	if !found {
		t.Fatal("the undo of a module's button calls the project's command with params from the row")
	}
}

const fixedClips = `{"version": 13,
  "collections": [{"name": "clips", "description": "Clips", "key": "slug", "display": "title",
    "schema": {"type": "object", "properties": {"slug": {"type": "string"}, "title": {"type": "string"}}, "required": ["slug"]}}],
  "commands": [{"name": "retitle", "title": "Retitle", "description": "Retitles a clip.", "class": "internal", "result": {"type": "object"},
    "input": {"type": "object", "properties": {"slug": {"type": "string"}, "title": {"type": "string"}}, "required": ["slug", "title"]},
    "executor": {"type": "agent", "prompt": "Retitle."}}],
  "screens": [{"name": "clip", "title": "Clip", "record": "clips", "blocks": [
    {"type": "form", "command": "retitle", "values": {"slug": "=slug", "title": "=title"}, "fixed": [%s]}]}]}`

// TestAFormKeepsItsFixedFieldsFromTheOwner: a fixed field of a form is not in the input the form shows; its value,
// resolved by the server, goes apart for the call; a fixed field without a value is refused on save.
func TestAFormKeepsItsFixedFieldsFromTheOwner(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	check := harness.NewCatalogCheck(t)
	e.Project("clips", fmt.Sprintf(fixedClips, `"slug"`))
	e.Must("deck.records_upsert", map[string]any{"project": "clips", "collection": "clips",
		"records": harness.Jsonl(map[string]any{"slug": "a-1", "title": "First"})}, nil)
	var recs []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "clips", "collection": "clips"}, &recs)
	check.Messages(t, e.ScreenMessages("clips", "clip", map[string]any{"record": recs[0].ID}))
	form := harness.BlockData[struct {
		Values, Fixed map[string]any
		Commands      []struct {
			Input struct {
				Properties map[string]any
				Required   []string
			}
			Order []string
		}
	}](t, e.ScreenData("clips", "clip", map[string]any{"record": recs[0].ID})["b0"]["value"])
	in := form.Commands[0]
	if form.Fixed["slug"] != "a-1" || form.Values["slug"] != nil || form.Values["title"] != "First" || in.Input.Properties["slug"] != nil ||
		slices.Contains(in.Input.Required, "slug") || slices.Contains(in.Order, "slug") {
		t.Fatalf("the fixed slug goes apart and is no field the owner sees: %+v", form)
	}
	e.Refused("deck.manifest_save", map[string]any{"project": "clips", "manifest": fmt.Sprintf(fixedClips, `"slug", "sid"`)}, 422,
		`fixed "sid": a fixed field takes its value from values`)
}

// TestAButtonOfAModuleOpensAScreenOfDeckFilled: screen deck/<name> opens a screen of deck with its params, a tab or a
// dialog filled from the item, nothing called; an unknown screen or param of it is refused on save.
func TestAButtonOfAModuleOpensAScreenOfDeckFilled(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	open := `"actions": [{"label": "Open", "screen": "note"}]`
	add := func(button string) string {
		return strings.Replace(harness.WebNotes, open, `"actions": [{"label": "Open", "screen": "note"}, `+button+`]`, 1)
	}
	e.Project("web", add(`{"label": "Add", "screen": "deck/settings", "params": {"tab": "Models", "dialog": "model", "model": "=title", "connection": "openrouter"}, "when": "score == nil"}`))
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "qwen"}`}, nil)
	rows := harness.BlockData[[]harness.ScreenRow](t, e.ScreenBlock("web", "main", "Notes", nil)["rows"])
	if len(rows) != 1 || len(rows[0].Actions) != 2 {
		t.Fatalf("the row has both buttons: %+v", rows)
	}
	if b := rows[0].Actions[1]; b["screen"] != "settings" || b["project"] != nil || b["record"] != nil || b["command"] != nil ||
		fmt.Sprint(b["params"]) != "map[connection:openrouter dialog:model model:qwen tab:Models]" {
		t.Fatalf("the button opens the screen of deck with its form filled from the row: %+v", b)
	}
	for button, want := range map[string]string{
		`{"label": "Add", "screen": "deck/nowhere"}`:                        `unknown screen of deck "nowhere"`,
		`{"label": "Add", "screen": "deck/settings", "params": {"x": "1"}}`: `screen deck/settings takes params`,
	} {
		e.Refused("deck.manifest_save", map[string]any{"project": "web", "manifest": add(button)}, 422, want)
	}
}
