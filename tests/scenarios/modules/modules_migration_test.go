package modules

import (
	"slices"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"deck2/tests/harness"
)

var peopleV1 = harness.Manifest(`{"collections": [{"name": "people", "description": "A person.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}]}`)

func peopleV2(migration string) string {
	m := harness.Manifest(`{"collections": [{"name": "people", "description": "A person.", "key": "full_name", MIGRATION
    "schema": {"type": "object", "properties": {"full_name": {"type": "string"}}, "required": ["full_name"], "additionalProperties": false}}],
  "scripts": [{"name": "rename", "file": "scripts/rename.py", "runtime": "python"}]}`)
	if migration == "" {
		return strings.Replace(m, "MIGRATION", "", 1)
	}
	return strings.Replace(m, "MIGRATION", `"migration": "`+migration+`",`, 1)
}

const renameScript = `import json, sys
for line in sys.stdin:
    r = json.loads(line)
    print(json.dumps({"id": r["id"], "data": {"full_name": r["data"]["name"].title()}}))
print(json.dumps({"data": {"full_name": "Eve"}}))
`

func people(e *harness.Env) []harness.RecordRow {
	e.T.Helper()
	var rs []harness.RecordRow
	e.Must("deck.records_list", map[string]any{"project": "people", "collection": "people"}, &rs)
	return rs
}

func TestSchemaMigrationAppliesRecordsWithTheManifestAndRevertsBoth(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("people", peopleV1)
	e.Must("deck.records_upsert", map[string]any{"project": "people", "collection": "people",
		"records": "{\"name\": \"ada\"}\n{\"name\": \"bob\"}"}, nil)
	before := people(e)

	code, out := e.Call("deck.manifest_save", map[string]any{"project": "people", "manifest": peopleV2("")})
	if code != 422 || !strings.Contains(string(out), "2 of 2 records") || !strings.Contains(string(out), "add a migration script") {
		t.Fatalf("a breaking change without a migration script is refused with the records: %d %s", code, out)
	}
	e.SaveScript("people", "scripts/rename.py", `import json, sys
for line in sys.stdin:
    r = json.loads(line)
    print(json.dumps({"id": r["id"], "data": {"full_name": "same"}}))
`)
	code, out = e.Call("deck.manifest_save", map[string]any{"project": "people", "manifest": peopleV2("rename")})
	if code != 422 || !strings.Contains(string(out), "collides") {
		t.Fatalf("migrated records whose keys collide are refused: %d %s", code, out)
	}
	if rs := people(e); rs[0].Data["name"] != "ada" {
		t.Fatalf("a refused migration leaves the records: %+v", rs)
	}

	e.SaveScript("people", "scripts/rename.py", renameScript)
	e.Must("deck.manifest_save", map[string]any{"project": "people", "manifest": peopleV2("rename"), "reason": "rename"}, nil)
	after := people(e)
	if len(after) != 3 || after[0].ID != before[0].ID || after[0].Data["full_name"] != "Ada" || after[1].Data["full_name"] != "Bob" ||
		after[2].Data["full_name"] != "Eve" {
		t.Fatalf("migrated records keep their ids and get the new shape: %+v", after)
	}
	var man struct {
		Document int64 `json:"document"`
	}
	e.Must("deck.manifest_get", map[string]any{"project": "people"}, &man)
	e.Must("deck.document_revert", map[string]any{"document": man.Document, "revision": 1}, nil)
	reverted := people(e)
	if len(reverted) != 2 || reverted[0].ID != before[0].ID || reverted[0].Data["name"] != "ada" || reverted[0].Data["full_name"] != nil {
		t.Fatalf("revert brings the records back with the manifest: %+v", reverted)
	}
	var cur struct {
		Manifest string `json:"manifest"`
	}
	e.Must("deck.manifest_get", map[string]any{"project": "people"}, &cur)
	if !strings.Contains(cur.Manifest, `"key":"name"`) && !strings.Contains(cur.Manifest, `"key": "name"`) {
		t.Fatalf("revert brings the manifest back: %s", cur.Manifest)
	}
}

func TestRevertBringsBackRecordsAMigrationDropped(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("people", peopleV1)
	e.Must("deck.records_upsert", map[string]any{"project": "people", "collection": "people", "records": `{"name": "ada"}`}, nil)
	before := people(e)
	e.Must("deck.document_save", map[string]any{"project": "people", "record": before[0].ID, "name": "notes.md", "kind": "doc", "text": "met her"}, nil)
	e.SaveScript("people", "scripts/rename.py", "import sys\nsys.stdin.read()\n")
	e.Must("deck.manifest_save", map[string]any{"project": "people", "manifest": peopleV2("rename")}, nil)
	if rs := people(e); len(rs) != 0 {
		t.Fatalf("the migration dropped every record: %+v", rs)
	}
	var man struct {
		Document int64 `json:"document"`
	}
	e.Must("deck.manifest_get", map[string]any{"project": "people"}, &man)
	e.Must("deck.document_revert", map[string]any{"document": man.Document, "revision": 1}, nil)
	if rs := people(e); len(rs) != 1 || rs[0].ID != before[0].ID || rs[0].Data["name"] != "ada" {
		t.Fatalf("revert brings back records of a collection the migration emptied: %+v", rs)
	}
	var docs []struct {
		Name    string
		OwnerID int64 `json:"owner_id"`
	}
	e.Must("deck.documents_list", map[string]any{"project": "people"}, &docs)
	if !slices.ContainsFunc(docs, func(d struct {
		Name    string
		OwnerID int64 `json:"owner_id"`
	}) bool {
		return d.Name == "notes.md" && d.OwnerID == before[0].ID
	}) {
		t.Fatalf("and the documents of those records: %+v", docs)
	}
}

// TestDeckKeepsNoBoards: deck has no commands of boards or cards, and a manifest with a board, a board block or a
// lane trigger is refused.
func TestDeckKeepsNoBoards(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	for _, name := range []string{"deck.cards_list", "deck.card_create", "deck.card_move", "deck.card_transfer", "deck.chat_interview"} {
		e.Refused(name, map[string]any{"project": "work", "card": 1, "title": "x"}, 404, "unknown command")
	}
	plain := harness.Manifest(`{"commands": [{"name": "work", "title": "Work", "description": "Works.", "class": "internal",
    "executor": {"type": "agent", "prompt": "Work."}}],
  "triggers": [{"command": "work", "cron": "0 9 * * *"}],
  "screens": [{"name": "main", "title": "Main", "blocks": [{"type": "actions", "title": "Work", "actions": [{"label": "Work", "command": "work"}]}]}], "menu": ["main"]}`)
	e.Project("work", plain)
	for _, c := range []struct{ old, new, says string }{
		{`"triggers": [`, `"board": {"lanes": [{"name": "doing", "title": "Doing", "role": "inbox"}]}, "triggers": [`, "board"},
		{`{"type": "actions", "title": "Work", "actions": [{"label": "Work", "command": "work"}]}`, `{"type": "board", "title": "Work"}`, "/screens/0/blocks/0/type"},
		{`{"command": "work", "cron": "0 9 * * *"}`, `{"command": "work", "lane": "doing"}`, "lane"},
	} {
		m := strings.Replace(plain, c.old, c.new, 1)
		if m == plain {
			t.Fatalf("fixture replacement did not apply: %s", c.old)
		}
		e.Refused("deck.manifest_save", map[string]any{"project": "work", "manifest": m}, 422, c.says)
	}
}

// TestADeckThatRefusesASavedManifestSaysSoOnce: a manifest saved by an older deck that the checks of this one refuse
// works on, and its project gets one note of what to fix; a clean project gets none, nor does a second start.
func TestADeckThatRefusesASavedManifestSaysSoOnce(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	e.Project("clean", harness.PlainManifest)
	e.Project("old", harness.PlainManifest)
	older := strings.Replace(harness.PlainManifest, `"screens": []`, `"screens": [], "domains": {"read": ["https://example.com/feed"]}`, 1)
	// A manifest an older deck saved before the check of domains.read.
	e.PlantRevision("old", "manifest.json", older)
	restart := func() {
		_ = e.App.Close()
		e = harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	}
	restart()
	n := e.WaitNote("The manifest of old")
	if !strings.Contains(n.Text, "domains.read") || !strings.Contains(n.Text, "not a URL") {
		t.Fatalf("the note says what to fix: %s", n.Text)
	}
	for _, f := range e.Notes() {
		if strings.Contains(f.Text, "clean") {
			t.Fatalf("a manifest the checks pass gets no note: %s", f.Text)
		}
	}
	if code, out := e.Call("deck.screen_get", map[string]any{"project": "old", "screen": "documents"}); code != 200 {
		t.Fatalf("the project works on: %d %s", code, out)
	}
	restart()
	count := 0
	for _, f := range e.Notes() {
		if strings.Contains(f.Text, "The manifest of old") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the same problems are told once: %d notes", count)
	}
}
