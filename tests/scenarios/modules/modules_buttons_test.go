package modules

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"deck2/tests/harness"
)

func TestButtonsOfARecordPageFollowTheRecordAfterAnEvent(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebAgent()))
	e.Project("notes", harness.WebManifest("http://127.0.0.1:1"))
	var up struct{ IDs []int64 }
	e.Must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes", "records": harness.Jsonl(map[string]any{"title": "alpha", "score": 3})}, &up)
	score := func(topic string) any {
		in := map[string]any{"record": up.IDs[0]}
		var block map[string]json.RawMessage
		if topic == "" {
			block = e.Screen("notes", "note", in)["b2"]
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
	e.Project("notes", harness.Manifest(`{"collections": [{"name": "notes", "description": "Notes", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "score": {"type": "integer"}}, "required": ["title"]}}],
  "commands": [{"name": "touch", "title": "Touch", "description": "Touches a note.", "class": "internal", "result": {"type": "object"},
    "input": {"type": "object", "properties": {"note": {"type": "integer", "format": "record"}, "score": {"type": "string"}}, "required": ["note"]},
    "executor": {"type": "agent", "prompt": "Touch."}}],
  "screens": [{"name": "main", "title": "Notes", "blocks": [{"type": "table", "title": "Notes", "source": {"collection": "notes", "sort": "title"},
    "columns": [{"label": "Title", "bind": "title"}],
    "actions": [{"label": "Boost", "command": "touch", "params": {"note": "=_id", "score": "=string(score * 2)"}, "when": "score >= 5", "confirm": true},
      {"label": "Rate", "command": "touch", "params": {"note": "=_id"}, "input": "score"}]}]}]}`))
	e.Must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes",
		"records": harness.Jsonl(map[string]any{"title": "a", "score": 1}, map[string]any{"title": "b", "score": 7})}, nil)
	rows := harness.BlockData[[]harness.ScreenRow](t, e.Screen("notes", "main", nil)["b0"]["rows"])
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
	for _, v := range e.Screen("web", "main", nil) {
		for _, raw := range v {
			found = found || strings.Contains(string(raw), `"undo":{"command":"web.tag","params":{"tag":"alpha"}`)
		}
	}
	if !found {
		t.Fatal("the undo of a module's button calls the project's command with params from the row")
	}
}

var fixedClips = harness.Manifest(`{"collections": [{"name": "clips", "description": "Clips", "key": "slug", "display": "title",
    "schema": {"type": "object", "properties": {"slug": {"type": "string"}, "title": {"type": "string"}}, "required": ["slug"]}}],
  "commands": [{"name": "retitle", "title": "Retitle", "description": "Retitles a clip.", "class": "internal", "result": {"type": "object"},
    "input": {"type": "object", "properties": {"slug": {"type": "string"}, "title": {"type": "string"}}, "required": ["slug", "title"]},
    "executor": {"type": "agent", "prompt": "Retitle."}}],
  "screens": [{"name": "clip", "title": "Clip", "record": "clips", "blocks": [
    {"type": "form", "command": "retitle", "values": {"slug": "=slug", "title": "=title"}, "fixed": [%s]}]}]}`)

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
	}](t, e.Screen("clips", "clip", map[string]any{"record": recs[0].ID})["b0"]["value"])
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

// TestTheOwnerDeletesARecordFromAButtonOfItsModule: a row of the module's own collection or its record page may carry
// deck.record_delete, always asking first; elsewhere it is refused.
func TestTheOwnerDeletesARecordFromAButtonOfItsModule(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	open := `{"label": "Open", "screen": "note"}`
	del := `{"label": "Delete", "command": "deck.record_delete"}`
	m := strings.Replace(harness.WebNotes, open, open+`, `+del, 1)
	m = strings.Replace(m, `{"type": "form", "title": "Edit", "command": "deck.record_update"}]}`,
		`{"type": "form", "title": "Edit", "command": "deck.record_update"}, {"type": "actions", "actions": [`+del+`]}]}`, 1)
	e.Project("web", m)
	for _, bad := range []struct{ from, to, want string }{
		{del, `{"label": "Delete", "command": "deck.record_delete", "params": {"record": "=1"}}`, "record_delete takes the record of its row"},
		{`"actions": [{"label": "Mark", "command": "mark"},`, `"actions": [` + del + `, {"label": "Mark", "command": "mark"},`, "not on a custom block"},
	} {
		if code, out := e.Call("deck.manifest_save", map[string]any{"project": "web", "manifest": strings.Replace(m, bad.from, bad.to, 1)}); code != 422 || !strings.Contains(string(out), bad.want) {
			t.Fatalf("%s is refused: %d %s", bad.to, code, out)
		}
	}
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "a"}` + "\n" + `{"title": "b"}`}, nil)
	rows := harness.BlockData[[]harness.ScreenRow](t, e.ScreenBlock("web", "main", "Notes", nil)["rows"])
	var act map[string]any
	for _, a := range rows[0].Actions {
		if a["command"] == "deck.record_delete" {
			act = a
		}
	}
	params, _ := act["params"].(map[string]any)
	if act == nil || act["confirm"] != true || act["parts"] != "deck.record_parts_list" || params["record"] == nil {
		t.Fatalf("the button deletes the record of its row and asks first: %+v", rows[0].Actions)
	}
	e.Must("deck.record_delete", params, nil)
	if rows := harness.BlockData[[]harness.ScreenRow](t, e.ScreenBlock("web", "main", "Notes", nil)["rows"]); len(rows) != 1 {
		t.Fatalf("the owner's press deletes the record: %+v", rows)
	}
}

// TestAButtonOpensTheChatOfTheMaster: a button with chat and a command opens the topic its command made; one with chat
// alone repeats the button of Deki, a warning on read and refused on save, and a chat button opens no screen.
func TestAButtonOpensTheChatOfTheMaster(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	m := strings.Replace(harness.BoardManifest, `"actions": [{"label": "Move"`, `"actions": [{"label": "Talk it over", "command": "move", "params": {"id": "=_id"}, "chat": true}, {"label": "Move"`, 1)
	e.Project("work", m)
	e.Must("deck.records_upsert", map[string]any{"project": "work", "collection": "tasks", "records": `{"title": "a", "status": "doing"}`}, nil)
	raw, _ := json.Marshal(e.Screen("work", "board", nil))
	if !strings.Contains(string(raw), `"chat":true`) || !strings.Contains(string(raw), `"label":"Talk it over"`) {
		t.Fatalf("a chat button with a command is drawn with chat: %s", raw)
	}
	alone := strings.Replace(harness.BoardManifest, `"actions": [{"label": "Move"`, `"actions": [{"label": "New task", "chat": true}, {"label": "Move"`, 1)
	for _, cmd := range []string{"deck.manifest_save", "deck.project_create"} {
		in := map[string]any{"project": "work", "manifest": alone}
		if cmd == "deck.project_create" {
			in = map[string]any{"name": "other", "manifest": alone}
		}
		if code, out := e.Call(cmd, in); code != 422 || !strings.Contains(string(out), "repeats the button of Deki") {
			t.Fatalf("%s refuses a button that only opens the chat: %d %s", cmd, code, out)
		}
	}
	// A button an older deck saved before the check of the button of Deki.
	e.PlantRevision("work", "manifest.json", alone)
	var read struct{ Warnings []string }
	e.Must("deck.manifest_get", map[string]any{"project": "work"}, &read)
	if !slices.ContainsFunc(read.Warnings, func(w string) bool { return strings.Contains(w, "repeats the button of Deki") }) {
		t.Fatalf("a stored button that only opens the chat is a warning on read: %v", read.Warnings)
	}
	if code, out := e.Call("deck.manifest_save", map[string]any{"project": "work", "manifest": strings.Replace(m, `"chat": true}`, `"chat": true, "screen": "board"}`, 1)}); code != 422 || !strings.Contains(string(out), "chat") {
		t.Fatalf("a chat button opens no screen: %d %s", code, out)
	}
}

// TestACellPicksTheInputOfAButtonOfItsRow: a table column with pick names a button of the row with an input and
// options with their labels; the row says which button the cell is, and a pick presses it with the value.
func TestACellPicksTheInputOfAButtonOfItsRow(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	table := strings.Replace(harness.BoardManifest, `{"type": "list", "title": "Tasks", "board": "status", "source": {"collection": "tasks"}, "item": {"title": "title"},
     "actions": [{"label": "Move", "command": "move", "params": {"id": "=_id"}, "input": "status"}]}`,
		`{"type": "table", "title": "Tasks", "source": {"collection": "tasks"},
     "columns": [{"label": "Task", "bind": "title"}, {"label": "Status", "bind": "status", "pick": "Move"}, {"label": "Next", "bind": "status == 'todo' ? 'doing' : status"}],
     "actions": [{"label": "Move", "command": "move", "params": {"id": "=_id"}, "input": "status", "options": "=['todo', 'doing', 'done']"}]}`, 1)
	if table == harness.BoardManifest {
		t.Fatal("fixture lacks the board list")
	}
	code, out := e.Call("deck.project_create", map[string]any{"name": "bad", "manifest": strings.Replace(table, `"pick": "Move"`, `"pick": "Drop"`, 1)})
	if code != 422 || !strings.Contains(string(out), `pick "Drop" needs a button of the row`) {
		t.Fatalf("a pick of no button of the row is refused: %d %s", code, out)
	}
	e.Project("work", table)
	e.Must("deck.records_upsert", map[string]any{"project": "work", "collection": "tasks", "records": `{"title": "a", "status": "todo"}`}, nil)
	drawn, _ := json.Marshal(e.ScreenMessages("work", "board", nil))
	if !strings.Contains(string(drawn), `{"label":"Status","pick":"Move"}`) {
		t.Fatalf("the column says what it picks: %s", drawn)
	}
	rows := harness.BlockData[[]harness.ScreenRow](t, e.ScreenBlock("work", "board", "Tasks", nil)["rows"])
	if len(rows) != 1 || len(rows[0].Actions) != 1 || rows[0].Actions[0]["pick"] != "Move" || fmt.Sprint(rows[0].Actions[0]["options"]) != "[todo doing done]" {
		t.Fatalf("the button of the row is the one the cell picks with: %+v", rows)
	}
	rows = harness.BlockData[[]harness.ScreenRow](t, e.ScreenBlock("work", "board", "Tasks", map[string]any{"lang": "ru"})["rows"])
	if fmt.Sprint(rows[0].Cells...) != "atodoв работе" || fmt.Sprint(rows[0].Actions[0]["labels"]) != "[todo в работе готово]" {
		t.Fatalf("a picked cell keeps the value, its options say each in the owner's language, and an expression of one field is translated: %+v", rows)
	}
}

// TestAFieldOnlyDecksScreensUseIsAWarningOnReadAndRefusedOnSave: a module a model writes never sees what is marked
// deck_only; one an older deck saved with such a field still loads and says so, and a save refuses it.
func TestAFieldOnlyDecksScreensUseIsAWarningOnReadAndRefusedOnSave(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("work", harness.BoardManifest)
	var format struct {
		Defs map[string]struct {
			Properties map[string]any `json:"properties"`
		} `json:"$defs"`
	}
	e.Must("deck.manifest_format_get", nil, &format)
	for _, f := range []string{"verb", "until", "record", "project"} {
		if _, ok := format.Defs["action"].Properties[f]; ok {
			t.Fatalf("the format a model reads has no %s of a button", f)
		}
	}
	if _, ok := format.Defs["action"].Properties["label"]; !ok {
		t.Fatal("the format a model reads keeps the label of a button")
	}
	verb := strings.Replace(harness.BoardManifest, `{"label": "Move", "command": "move"`, `{"verb": "move", "command": "move"`, 1)
	if verb == harness.BoardManifest {
		t.Fatal("the fixture lost its button Move")
	}
	if code, out := e.Call("deck.manifest_save", map[string]any{"project": "work", "manifest": verb}); code != 422 || !strings.Contains(string(out), "only on deck's own screens") {
		t.Fatalf("a save refuses a verb of a module: %d %s", code, out)
	}
	// A button an older deck saved before verb was marked deck_only.
	e.PlantRevision("work", "manifest.json", verb)
	var read struct{ Warnings []string }
	e.Must("deck.manifest_get", map[string]any{"project": "work"}, &read)
	if !slices.ContainsFunc(read.Warnings, func(w string) bool {
		return strings.Contains(w, "only on deck's own screens") && strings.Contains(w, "verb")
	}) {
		t.Fatalf("a stored verb is a warning on read: %v", read.Warnings)
	}
}
