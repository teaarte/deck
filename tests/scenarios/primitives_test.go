package scenarios

import (
	"encoding/json"
	"fmt"
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

// boardManifest is a module whose tasks move across the lanes of their status.
const boardManifest = `{"version": 10, "i18n": {"ru": {"doing": "в работе"}},
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
	e := newEnv(t, nil)
	for _, bad := range []struct{ from, to, want string }{
		{`"board": "status"`, `"board": "title"`, "board: title is not an enum field"},
		{`"source": {"collection": "tasks"}`, `"source": {"from": "deck.projects_list"}`, "board: lays out a collection of the module"},
	} {
		code, out := e.call("deck.project_create", map[string]any{"name": "bad", "manifest": strings.Replace(boardManifest, bad.from, bad.to, 1)})
		if code != 422 || !strings.Contains(string(out), bad.want) {
			t.Fatalf("%s is refused: %d %s", bad.to, code, out)
		}
	}
	e.createProject("work", boardManifest)
	e.must("deck.records_upsert", map[string]any{"project": "work", "collection": "tasks", "records": `{"title": "a", "status": "doing"}` + "\n" + `{"title": "b", "status": "todo"}`}, nil)
	block := e.screenData("work", "board", map[string]any{"lang": "ru"})
	var data struct {
		Board struct {
			Field string `json:"field"`
			Lanes []struct{ Value, Label string }
		} `json:"board"`
		Items []struct {
			Title, Lane string
		} `json:"items"`
	}
	for _, b := range block {
		if _, ok := b["board"]; ok {
			raw, _ := json.Marshal(b)
			_ = json.Unmarshal(raw, &data)
		}
	}
	if data.Board.Field != "status" || fmt.Sprint(data.Board.Lanes) != "[{todo todo} {doing в работе} {done done}]" {
		t.Fatalf("the lanes are the enum in order and in the owner's language: %+v", data.Board)
	}
	lanes := map[string]string{}
	for _, it := range data.Items {
		lanes[it.Title] = it.Lane
	}
	if lanes["a"] != "doing" || lanes["b"] != "todo" {
		t.Fatalf("each item says its lane: %+v", data.Items)
	}
}

// TestAButtonOpensTheChatOfTheMaster: a button with chat and nothing else opens the chat panel in a new topic; it calls
// no command.
func TestAButtonOpensTheChatOfTheMaster(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	m := strings.Replace(boardManifest, `"actions": [{"label": "Move"`, `"actions": [{"label": "New task", "chat": true}, {"label": "Move"`, 1)
	e.createProject("work", m)
	e.must("deck.records_upsert", map[string]any{"project": "work", "collection": "tasks", "records": `{"title": "a", "status": "doing"}`}, nil)
	raw, _ := json.Marshal(e.screenData("work", "board", nil))
	if !strings.Contains(string(raw), `{"chat":true,"label":"New task"}`) {
		t.Fatalf("the chat button is drawn with chat alone: %s", raw)
	}
	if code, out := e.call("deck.manifest_save", map[string]any{"project": "work", "manifest": strings.Replace(m, `"chat": true}`, `"chat": true, "screen": "board"}`, 1)}); code != 422 || !strings.Contains(string(out), "chat") {
		t.Fatalf("a chat button opens the chat and nothing else: %d %s", code, out)
	}
}

// TestABoardShowsItsLanesEmptyAndOnlyThoseItsButtonOffers: a board with no items still shows its lanes, its move
// button names the lanes it offers by options, and a board with no move button is refused.
func TestABoardShowsItsLanesEmptyAndOnlyThoseItsButtonOffers(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	if code, out := e.call("deck.project_create", map[string]any{"name": "still", "manifest": strings.Replace(boardManifest,
		`, "input": "status"}`, `}`, 1)}); code != 422 || !strings.Contains(string(out), `input "status"`) {
		t.Fatalf("a board without a button that moves an item is refused with what to add: %d %s", code, out)
	}
	e.createProject("work", strings.Replace(boardManifest, `"input": "status"}`, `"input": "status", "options": "=['todo', 'doing']"}`, 1))
	var lanes []string
	for _, b := range e.screenData("work", "board", nil) {
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
// without errors.
func TestAScreenThatDrawsAgainIsHealthy(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	m := strings.Replace(boardManifest, `"item": {"title": "title"}`, `"item": {"title": "title + note"}`, 1)
	m = strings.Replace(m, `"status": {"type": "string", "enum": ["todo", "doing", "done"]}}}}]`, `"status": {"type": "string", "enum": ["todo", "doing", "done"]}, "note": {"type": "string"}}}}]`, 1)
	e.createProject("work", m)
	e.must("deck.records_upsert", map[string]any{"project": "work", "collection": "tasks", "records": `{"title": "a", "status": "doing"}`}, nil)
	e.screenData("work", "board", nil)
	e.waitHealth("screen:work.board", "error")
	e.must("deck.records_upsert", map[string]any{"project": "work", "collection": "tasks", "records": `{"title": "a", "status": "doing", "note": "!"}`}, nil)
	e.screenData("work", "board", nil)
	var hs []healthRow
	e.must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if h.Subject == "screen:work.board" {
			t.Fatalf("a screen that draws again is healthy: %+v", h)
		}
	}
}
