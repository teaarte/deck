package scenarios

import (
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
)

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
	if act == nil || act["confirm"] != true || act["parts"] != "deck.record_parts" || params["record"] == nil {
		t.Fatalf("the button deletes the record of its row and asks first: %+v", rows[0].Actions)
	}
	e.Must("deck.record_delete", params, nil)
	if rows := harness.BlockData[[]harness.ScreenRow](t, e.ScreenBlock("web", "main", "Notes", nil)["rows"]); len(rows) != 1 {
		t.Fatalf("the owner's press deletes the record: %+v", rows)
	}
}

// boardManifest is a module whose tasks move across the lanes of their status.
const boardManifest = `{"version": 13, "i18n": {"ru": {"doing": "в работе"}},
  "collections": [{"name": "tasks", "description": "Tasks.", "key": "title", "schema": {"type": "object",
    "properties": {"title": {"type": "string"}, "status": {"type": "string", "enum": ["todo", "doing", "done"]}}}}],
  "commands": [{"name": "move", "title": "Move", "description": "Moves a task.", "class": "internal",
    "input": {"type": "object", "properties": {"id": {"type": "integer"}, "status": {"type": "string", "enum": ["todo", "doing", "done"]}}},
    "executor": {"type": "agent", "prompt": "Move it."}}],
  "screens": [{"name": "board", "title": "Board", "blocks": [
    {"type": "list", "title": "Tasks", "board": "status", "source": {"collection": "tasks"}, "item": {"title": "title"},
     "actions": [{"label": "Move", "command": "move", "params": {"id": "=_id"}, "input": "status"}]}]}],
  "menu": ["board"]}`

// TestABoardLaysTheItemsOfAListInTheLanesOfAnEnum: a list with board names an enum field of its collection; its lanes
// are the values of the enum in order, in the owner's language, and each item says its lane.
func TestABoardLaysTheItemsOfAListInTheLanesOfAnEnum(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	for _, bad := range []struct{ from, to, want string }{
		{`"board": "status"`, `"board": "title"`, "board: title is not an enum field"},
		{`"source": {"collection": "tasks"}`, `"source": {"from": "deck.projects_list"}`, "board: lays out a collection of the module"},
	} {
		code, out := e.Call("deck.project_create", map[string]any{"name": "bad", "manifest": strings.Replace(boardManifest, bad.from, bad.to, 1)})
		if code != 422 || !strings.Contains(string(out), bad.want) {
			t.Fatalf("%s is refused: %d %s", bad.to, code, out)
		}
	}
	e.Project("work", boardManifest)
	e.Must("deck.records_upsert", map[string]any{"project": "work", "collection": "tasks", "records": `{"title": "a", "status": "doing"}` + "\n" + `{"title": "b", "status": "todo"}`}, nil)
	block := e.ScreenData("work", "board", map[string]any{"lang": "ru"})
	var data struct {
		Board struct {
			Field string `json:"field"`
			Lanes []struct{ Value, Label string }
		} `json:"board"`
		Items []struct {
			Title, Lane string
			Actions     []map[string]any
		} `json:"items"`
	}
	for _, b := range block {
		if _, ok := b["board"]; ok {
			raw, _ := json.Marshal(b)
			_ = json.Unmarshal(raw, &data)
		}
	}
	if data.Board.Field != "status" || fmt.Sprint(data.Board.Lanes) != "[{todo todo} {doing в работе} {done готово}]" {
		t.Fatalf("the lanes are the enum in order and in the owner's language: %+v", data.Board)
	}
	lanes := map[string]string{}
	for _, it := range data.Items {
		lanes[it.Title] = it.Lane
	}
	if lanes["a"] != "doing" || lanes["b"] != "todo" {
		t.Fatalf("each item says its lane: %+v", data.Items)
	}
	if move := data.Items[0].Actions[0]; fmt.Sprint(move["options"]) != "[todo doing done]" || fmt.Sprint(move["labels"]) != "[todo в работе готово]" {
		t.Fatalf("the button that moves a card picks among the lanes of the board: %+v", move)
	}
}

// TestAButtonOpensTheChatOfTheMaster: a button with chat and a command opens the topic its command made; one with chat
// alone repeats the button of Deki, a warning on read and refused on save, and a chat button opens no screen.
func TestAButtonOpensTheChatOfTheMaster(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	m := strings.Replace(boardManifest, `"actions": [{"label": "Move"`, `"actions": [{"label": "Talk it over", "command": "move", "params": {"id": "=_id"}, "chat": true}, {"label": "Move"`, 1)
	pid := e.Project("work", m)
	e.Must("deck.records_upsert", map[string]any{"project": "work", "collection": "tasks", "records": `{"title": "a", "status": "doing"}`}, nil)
	raw, _ := json.Marshal(e.ScreenData("work", "board", nil))
	if !strings.Contains(string(raw), `"chat":true`) || !strings.Contains(string(raw), `"label":"Talk it over"`) {
		t.Fatalf("a chat button with a command is drawn with chat: %s", raw)
	}
	alone := strings.Replace(boardManifest, `"actions": [{"label": "Move"`, `"actions": [{"label": "New task", "chat": true}, {"label": "Move"`, 1)
	for _, cmd := range []string{"deck.manifest_save", "deck.project_create"} {
		in := map[string]any{"project": "work", "manifest": alone}
		if cmd == "deck.project_create" {
			in = map[string]any{"name": "other", "manifest": alone}
		}
		if code, out := e.Call(cmd, in); code != 422 || !strings.Contains(string(out), "repeats the button of Deki") {
			t.Fatalf("%s refuses a button that only opens the chat: %d %s", cmd, code, out)
		}
	}
	if _, err := e.App.Store.DB.Exec(`UPDATE document_revision SET text = ?
		WHERE document_id IN (SELECT id FROM document WHERE project_id = ? AND kind = 'manifest')`, alone, pid); err != nil {
		t.Fatal(err)
	}
	var read struct{ Warnings []string }
	e.Must("deck.manifest_get", map[string]any{"project": "work"}, &read)
	if !slices.ContainsFunc(read.Warnings, func(w string) bool { return strings.Contains(w, "repeats the button of Deki") }) {
		t.Fatalf("a stored button that only opens the chat is a warning on read: %v", read.Warnings)
	}
	if code, out := e.Call("deck.manifest_save", map[string]any{"project": "work", "manifest": strings.Replace(m, `"chat": true}`, `"chat": true, "screen": "board"}`, 1)}); code != 422 || !strings.Contains(string(out), "chat") {
		t.Fatalf("a chat button opens no screen: %d %s", code, out)
	}
}

// TestABoardShowsItsLanesEmptyAndOnlyThoseItsButtonOffers: a board with no items still shows its lanes, its move
// button names the lanes it offers by options, and a board with no move button is refused.
func TestABoardShowsItsLanesEmptyAndOnlyThoseItsButtonOffers(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	if code, out := e.Call("deck.project_create", map[string]any{"name": "still", "manifest": strings.Replace(boardManifest,
		`, "input": "status"}`, `}`, 1)}); code != 422 || !strings.Contains(string(out), `input "status"`) {
		t.Fatalf("a board without a button that moves an item is refused with what to add: %d %s", code, out)
	}
	e.Project("work", strings.Replace(boardManifest, `"input": "status"}`, `"input": "status", "options": "=['todo', 'doing']"}`, 1))
	var lanes []string
	for _, b := range e.ScreenData("work", "board", nil) {
		var d struct {
			State string
			Board struct{ Lanes []struct{ Value string } }
		}
		raw, _ := json.Marshal(b)
		if json.Unmarshal(raw, &d) == nil && d.State == "ready" {
			for _, l := range d.Board.Lanes {
				lanes = append(lanes, l.Value)
			}
		}
	}
	if fmt.Sprint(lanes) != "[todo doing]" {
		t.Fatalf("an empty board shows the lanes its move button offers: %v", lanes)
	}
}

// TestAScreenThatDrawsAgainIsHealthy: a block that failed on the data is red in health until the screen draws
// without errors; one in a dialog turns red once the dialog is read and green once a read draws it again.
func TestAScreenThatDrawsAgainIsHealthy(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	m := strings.Replace(boardManifest, `"item": {"title": "title"}`, `"item": {"title": "title + note"}`, 1)
	m = strings.Replace(m, `"status": {"type": "string", "enum": ["todo", "doing", "done"]}}}}]`, `"status": {"type": "string", "enum": ["todo", "doing", "done"]}, "note": {"type": "string"}}}}]`, 1)
	e.Project("work", m)
	e.Must("deck.records_upsert", map[string]any{"project": "work", "collection": "tasks", "records": `{"title": "a", "status": "doing"}`}, nil)
	e.ScreenData("work", "board", nil)
	e.WaitHealth("screen:work.board", "error")
	e.Must("deck.records_upsert", map[string]any{"project": "work", "collection": "tasks", "records": `{"title": "a", "status": "doing", "note": "!"}`}, nil)
	e.ScreenData("work", "board", nil)
	for _, h := range e.Health() {
		if h.Subject == "screen:work.board" {
			t.Fatalf("a screen that draws again is healthy: %+v", h)
		}
	}
	e.Project("dialogs", strings.Replace(strings.Replace(m, `"item": {"title": "title + note"}`, `"item": {"title": "title"}`, 1), `"blocks": [
    {"type": "list"`, `"blocks": [
    {"type": "dialog", "name": "more", "title": "More", "blocks": [{"type": "table", "title": "Broken", "source": {"collection": "tasks"}, "columns": [{"label": "X", "bind": "title + note"}]}]},
    {"type": "list"`, 1))
	e.Must("deck.records_upsert", map[string]any{"project": "dialogs", "collection": "tasks", "records": `{"title": "a", "status": "doing"}`}, nil)
	e.ScreenMessages("dialogs", "board", map[string]any{"dialog": "more", "block": "b0-in"})
	e.WaitHealth("screen:dialogs.board", "error")
	e.ScreenData("dialogs", "board", nil)
	red := false
	for _, h := range e.Health() {
		red = red || h.Subject == "screen:dialogs.board"
	}
	if !red {
		t.Fatal("a draw that left the dialog unread does not say the screen is healthy")
	}
	e.Must("deck.records_upsert", map[string]any{"project": "dialogs", "collection": "tasks", "records": `{"title": "a", "status": "doing", "note": "!"}`}, nil)
	e.ScreenMessages("dialogs", "board", map[string]any{"dialog": "more", "block": "b0-in"})
	for _, h := range e.Health() {
		if h.Subject == "screen:dialogs.board" {
			t.Fatalf("a read of the dialog that draws its block again is healthy: %+v", h)
		}
	}
}

// TestValuesOfAnEnumSpeakTheLanguageOfTheOwnerAndListsFilterByEachValue: a cell, a line or a filter that shows a field
// of an enum shows its value in the owner's language; a filter of a list field offers and keeps each of its values.
func TestValuesOfAnEnumSpeakTheLanguageOfTheOwnerAndListsFilterByEachValue(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	m := strings.Replace(boardManifest, `"properties": {"title": {"type": "string"}, "status"`, `"properties": {"title": {"type": "string"}, "tags": {"type": "array", "items": {"type": "string", "enum": ["x", "y"]}}, "status"`, 1)
	m = strings.Replace(m, `"menu": ["board"]`, `"menu": ["board", "table"]`, 1)
	m = strings.Replace(m, `{"ru": {"doing": "в работе"}}`, `{"ru": {"doing": "в работе", "x": "икс"}}`, 1)
	m = strings.Replace(m, `"screens": [`, `"screens": [{"name": "table", "title": "Table", "blocks": [{"type": "table", "title": "All", "source": {"collection": "tasks"},
	  "filter": ["status", "tags"], "columns": [{"label": "Title", "bind": "title"}, {"label": "Status", "bind": "status"}]}]}, `, 1)
	e.Project("work", m)
	e.Must("deck.records_upsert", map[string]any{"project": "work", "collection": "tasks", "records": `{"title": "a", "status": "doing", "tags": ["x", "y"]}` + "\n" + `{"title": "b", "status": "todo", "tags": ["y"]}`}, nil)
	raw, _ := json.Marshal(e.ScreenData("work", "table", map[string]any{"lang": "ru"}))
	if !strings.Contains(string(raw), `"в работе"`) || strings.Contains(string(raw), `"cells":["a","doing"]`) {
		t.Fatalf("a cell of an enum field speaks the owner's language: %s", raw)
	}
	if !strings.Contains(string(raw), `"labels":["в работе","todo"]`) || !strings.Contains(string(raw), `"options":["x","y"]`) || !strings.Contains(string(raw), `"labels":["икс","y"]`) {
		t.Fatalf("a filter offers the values in the owner's language, and each value of a list field: %s", raw)
	}
	var id string
	for k, b := range e.ScreenData("work", "table", nil) {
		if _, ok := b["rows"]; ok {
			id = k
		}
	}
	picked, _ := json.Marshal(e.ScreenData("work", "table", map[string]any{"filter": fmt.Sprintf(`{%q: {"tags": "x"}}`, id)}))
	if !strings.Contains(string(picked), `"a"`) || strings.Contains(string(picked), `"cells":["b"`) {
		t.Fatalf("a filter of a list field keeps the items that have the value: %s", picked)
	}
}

// TestTheFormatAModelReadsHasOnlyWhatAModuleUses: manifest_format leaves out the blocks and button fields only deck's
// screens use, so a model learns one way to do each thing; a manifest that has them still loads.
func TestTheFormatAModelReadsHasOnlyWhatAModuleUses(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	var format map[string]any
	e.Must("deck.manifest_format", nil, &format)
	raw, _ := json.Marshal(format)
	for _, gone := range []string{`"appearance"`, `"verb"`, `"until"`, `"keep_open"`} {
		if strings.Contains(string(raw), gone) {
			t.Fatalf("the format a model reads has no %s", gone)
		}
	}
	for _, kept := range []string{`"board"`, `"custom"`, `"chat"`, `"options"`} {
		if !strings.Contains(string(raw), kept) {
			t.Fatalf("the format a model reads keeps %s", kept)
		}
	}
	e.Project("work", strings.Replace(boardManifest, `{"label": "Move"`, `{"verb": "open", "screen": "board"}, {"label": "Move"`, 1))
}

// TestARefusalOfAScreenSaysWhereByPointerAndWhatFits: a refusal names the block by the JSON Pointer a patch edits,
// and an unknown field lists the fields there are.
func TestARefusalOfAScreenSaysWhereByPointerAndWhatFits(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	bad := strings.Replace(boardManifest, `"item": {"title": "title"},`, `"item": {"title": "title"}, "filter": ["colour"],`, 1)
	code, out := e.Call("deck.project_create", map[string]any{"name": "work", "manifest": bad})
	if code != 422 || !strings.Contains(string(out), `/screens/0/blocks/0`) || !strings.Contains(string(out), "status") || !strings.Contains(string(out), "title") {
		t.Fatalf("a refusal says where by pointer and lists the fields: %d %s", code, out)
	}
}

// TestAStatOpensAScreenAndNoCommand: a stat runs only a button that opens a screen; a command on it is refused on save,
// and a module saved before still loads.
func TestAStatOpensAScreenAndNoCommand(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	m := strings.Replace(boardManifest, `"screens": [`, `"screens": [{"name": "count", "title": "Count", "blocks": [{"type": "stat", "title": "Tasks", "source": {"collection": "tasks"},
	  "actions": [{"label": "Move", "command": "move", "params": {"id": "1", "status": "done"}}]}]}, `, 1)
	code, out := e.Call("deck.project_create", map[string]any{"name": "work", "manifest": m})
	if code != 422 || !strings.Contains(string(out), "a stat opens a screen") {
		t.Fatalf("a command on a stat is refused on save: %d %s", code, out)
	}
}

// TestATranslationOfAnEnumValueIsUsed: a translation of a value of an enum of a collection is no unused word, since
// the cells, lines, lanes and filters of the module show it.
func TestATranslationOfAnEnumValueIsUsed(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("work", boardManifest)
	var saved struct {
		Unused []string `json:"unused_i18n"`
	}
	e.Must("deck.manifest_save", map[string]any{"project": "work", "manifest": boardManifest}, &saved)
	if len(saved.Unused) != 0 {
		t.Fatalf("the translation of an enum value is used: %v", saved.Unused)
	}
}

// TestACellPicksTheInputOfAButtonOfItsRow: a table column with pick names a button of the row with an input and
// options with their labels; the row says which button the cell is, and a pick presses it with the value.
func TestACellPicksTheInputOfAButtonOfItsRow(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	table := strings.Replace(boardManifest, `{"type": "list", "title": "Tasks", "board": "status", "source": {"collection": "tasks"}, "item": {"title": "title"},
     "actions": [{"label": "Move", "command": "move", "params": {"id": "=_id"}, "input": "status"}]}`,
		`{"type": "table", "title": "Tasks", "source": {"collection": "tasks"},
     "columns": [{"label": "Task", "bind": "title"}, {"label": "Status", "bind": "status", "pick": "Move"}, {"label": "Next", "bind": "status == 'todo' ? 'doing' : status"}],
     "actions": [{"label": "Move", "command": "move", "params": {"id": "=_id"}, "input": "status", "options": "=['todo', 'doing', 'done']"}]}`, 1)
	if table == boardManifest {
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
