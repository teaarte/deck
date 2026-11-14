package owner

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// itemsManifest is a module of a collection of items and a command of an agent.
var itemsManifest = harness.Manifest(`{"collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}],
  "commands": [{"name": "work", "title": "Work", "description": "Does work", "executor": {"type": "agent", "prompt": "Do the work."}}]}`)

// TestATokenWithARightOnOneModuleDoesNotReadAnother: data on one module lets a token read that one and change none;
// modules on a module lets it change that one, on a command of a module call that command alone.
func TestATokenWithARightOnOneModuleDoesNotReadAnother(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	for _, p := range []string{"notes", "diary"} {
		e.Project(p, itemsManifest)
		e.Must("deck.record_upsert", map[string]any{"project": p, "collection": "items", "records": `{"name": "a"}`}, nil)
	}
	var mine []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "diary", "collection": "items"}, &mine)
	deck := []string{"deck.records_list", "deck.record_get", "deck.record_upsert", "deck.document_get", "deck.processes_list"}
	_, reader := e.TokenWith(map[string]any{"name": "reader", "commands": deck, "data": []string{"notes"}})
	refused := func(tok, command string, in map[string]any, want string) {
		t.Helper()
		if code, out := e.As(tok).Call(command, in); code != 403 && code != 422 && code != 404 || !strings.Contains(string(out), want) {
			t.Fatalf("%s %v is refused with %q: %d %s", command, in, want, code, out)
		}
	}
	var rows []map[string]any
	if e.As(reader).Must("deck.records_list", map[string]any{"project": "notes", "collection": "items"}, &rows); len(rows) != 1 {
		t.Fatalf("a token reads the module its data names: %+v", rows)
	}
	refused(reader, "deck.records_list", map[string]any{"project": "diary", "collection": "items"}, "no right on module diary")
	refused(reader, "deck.records_list", map[string]any{"collection": "items"}, "project is required")
	refused(reader, "deck.record_get", map[string]any{"record": mine[0].ID}, "no record")
	refused(reader, "deck.processes_list", map[string]any{}, "project is required")
	refused(reader, "deck.record_upsert", map[string]any{"project": "notes", "collection": "items", "records": `{"name": "b"}`}, "changes only")

	_, writer := e.TokenWith(map[string]any{"name": "writer", "commands": deck, "modules": []string{"notes"}})
	e.As(writer).Must("deck.record_upsert", map[string]any{"project": "notes", "collection": "items", "records": `{"name": "b"}`}, nil)
	refused(writer, "deck.record_upsert", map[string]any{"project": "diary", "collection": "items", "records": `{"name": "b"}`}, "changes only")
	refused(writer, "deck.records_list", map[string]any{"project": "diary", "collection": "items"}, "no right on module diary")

	_, caller := e.TokenWith(map[string]any{"name": "caller", "modules": []string{"notes.work"}})
	s, err := e.Terminal(caller)
	if err != nil {
		t.Fatal(err)
	}
	if names := harness.ToolNames(s); !slices.Equal(names, []string{"notes__work"}) {
		t.Fatalf("a token of one command of a module is offered that command alone: %v", names)
	}
	var started struct{ Process int64 }
	if code, out := e.As(caller).Call("notes.work", map[string]any{}); code != 200 || json.Unmarshal(out, &started) != nil || started.Process == 0 {
		t.Fatalf("it calls that command: %d %s", code, out)
	}
	refused(caller, "diary.work", map[string]any{}, "may not call diary.work")
	refused(caller, "deck.records_list", map[string]any{"project": "notes", "collection": "items"}, "may not call")

	var tokens []struct {
		Name          string
		Commands      []string
		Data, Modules []string
	}
	e.Must("deck.tokens_list", map[string]any{"project": "diary"}, &tokens)
	if len(tokens) != 0 {
		t.Fatalf("no token reaches diary: %+v", tokens)
	}
	e.Must("deck.tokens_list", map[string]any{"project": "notes"}, &tokens)
	if len(tokens) != 3 || !slices.Equal(tokens[0].Data, []string{"notes"}) || !slices.Equal(tokens[2].Modules, []string{"notes.work"}) {
		t.Fatalf("the tokens that reach notes, with their rights: %+v", tokens)
	}
	e.Refused("deck.token_create", map[string]any{"name": "x", "commands": []string{"notes.work"}}, 422, "not a command of deck")
	e.Refused("deck.token_create", map[string]any{"name": "x", "data": []string{"notes.items"}}, 422, "not a module")
}

// TestATokenOfCommandsReachesTheModulesItNamedAfterTheMigration: a token of before keeps the module commands it named;
// one of deck's commands reaches the modules it named whole, or every one; deck tells the owner once.
func TestATokenOfCommandsReachesTheModulesItNamedAfterTheMigration(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithHome(t.TempDir()))
	e.Project("notes", itemsManifest)
	e.Project("diary", itemsManifest)
	// A base an older deck left: its tokens listed names of commands and held no rights.
	e.SQL(`DELETE FROM goose_db_version WHERE version_id >= 86`)
	// Nor had it the column of the rights of a token.
	e.SQL(`ALTER TABLE automation_token DROP COLUMN rights; DROP INDEX tool_call_project; ALTER TABLE tool_call DROP COLUMN subject; ALTER TABLE tool_call DROP COLUMN count; ALTER TABLE process DROP COLUMN opens`)
	for _, row := range [][2]string{{"board", `["dec.next","dec.status"]`}, {"orchestrator", `["dec.status","dec.add","deck.record_update"]`},
		{"everything", `["*"]`}, {"menu", `["deck.menu_get"]`}, {"wide", `["notes.*","dec.add"]`}} {
		// A token as an older deck made it, by the names of its commands.
		e.SQL(`INSERT INTO automation_token (workspace_id, name, hash, commands) VALUES (1, ?, ?, ?)`, row[0], "h-"+row[0], row[1])
	}
	e = e.Restart()
	var tokens []struct {
		Name                    string
		Commands, Data, Modules []string
	}
	e.Must("deck.tokens_list", map[string]any{}, &tokens)
	want := map[string][2]string{
		"board":        {"", "dec.next,dec.status"},
		"orchestrator": {"deck.record_update", "dec"},
		"everything":   {"deck.*", "*"},
		"menu":         {"deck.menu_get", "*"},
		"wide":         {"", "notes,dec.add"},
	}
	for _, tok := range tokens {
		w := want[tok.Name]
		if strings.Join(tok.Commands, ",") != w[0] || strings.Join(tok.Modules, ",") != w[1] || len(tok.Data) != 0 {
			t.Fatalf("%s after the migration: %+v, want commands %q and modules %q", tok.Name, tok, w[0], w[1])
		}
		delete(want, tok.Name)
	}
	if len(want) != 0 {
		t.Fatalf("tokens lost: %v", want)
	}
	e.WaitNote("orchestrator — dec")
}
