package scenarios

import (
	"slices"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const peopleV1 = `{"version": 9,
  "collections": [{"name": "people", "description": "A person.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}]}`

func peopleV2(migration string) string {
	m := `{"version": 9,
  "collections": [{"name": "people", "description": "A person.", "key": "full_name", MIGRATION
    "schema": {"type": "object", "properties": {"full_name": {"type": "string"}}, "required": ["full_name"], "additionalProperties": false}}],
  "scripts": [{"name": "rename", "file": "scripts/rename.py", "runtime": "python"}]}`
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

type recordRow struct {
	ID   int64          `json:"id"`
	Data map[string]any `json:"data"`
}

func (e *Env) people() []recordRow {
	e.T.Helper()
	var rs []recordRow
	e.must("deck.records_list", map[string]any{"project": "people", "collection": "people"}, &rs)
	return rs
}

func TestSchemaMigrationAppliesRecordsWithTheManifestAndRevertsBoth(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("people", peopleV1)
	e.must("deck.records_upsert", map[string]any{"project": "people", "collection": "people",
		"records": "{\"name\": \"ada\"}\n{\"name\": \"bob\"}"}, nil)
	before := e.people()

	code, out := e.call("deck.manifest_save", map[string]any{"project": "people", "manifest": peopleV2("")})
	if code != 422 || !strings.Contains(string(out), "2 of 2 records") || !strings.Contains(string(out), "add a migration script") {
		t.Fatalf("a breaking change without a migration script is refused with the records: %d %s", code, out)
	}
	e.saveScript("people", "scripts/rename.py", `import json, sys
for line in sys.stdin:
    r = json.loads(line)
    print(json.dumps({"id": r["id"], "data": {"full_name": "same"}}))
`)
	code, out = e.call("deck.manifest_save", map[string]any{"project": "people", "manifest": peopleV2("rename")})
	if code != 422 || !strings.Contains(string(out), "collides") {
		t.Fatalf("migrated records whose keys collide are refused: %d %s", code, out)
	}
	if rs := e.people(); rs[0].Data["name"] != "ada" {
		t.Fatalf("a refused migration leaves the records: %+v", rs)
	}

	e.saveScript("people", "scripts/rename.py", renameScript)
	e.must("deck.manifest_save", map[string]any{"project": "people", "manifest": peopleV2("rename"), "reason": "rename"}, nil)
	after := e.people()
	if len(after) != 3 || after[0].ID != before[0].ID || after[0].Data["full_name"] != "Ada" || after[1].Data["full_name"] != "Bob" ||
		after[2].Data["full_name"] != "Eve" {
		t.Fatalf("migrated records keep their ids and get the new shape: %+v", after)
	}
	var man struct {
		Document int64 `json:"document"`
	}
	e.must("deck.manifest_get", map[string]any{"project": "people"}, &man)
	e.must("deck.document_revert", map[string]any{"document": man.Document, "revision": 1}, nil)
	reverted := e.people()
	if len(reverted) != 2 || reverted[0].ID != before[0].ID || reverted[0].Data["name"] != "ada" || reverted[0].Data["full_name"] != nil {
		t.Fatalf("revert brings the records back with the manifest: %+v", reverted)
	}
	var cur struct {
		Manifest string `json:"manifest"`
	}
	e.must("deck.manifest_get", map[string]any{"project": "people"}, &cur)
	if !strings.Contains(cur.Manifest, `"key":"name"`) && !strings.Contains(cur.Manifest, `"key": "name"`) {
		t.Fatalf("revert brings the manifest back: %s", cur.Manifest)
	}
}

func TestRevertBringsBackRecordsAMigrationDropped(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("people", peopleV1)
	e.must("deck.records_upsert", map[string]any{"project": "people", "collection": "people", "records": `{"name": "ada"}`}, nil)
	before := e.people()
	e.must("deck.document_save", map[string]any{"project": "people", "record": before[0].ID, "name": "notes.md", "kind": "doc", "text": "met her"}, nil)
	e.saveScript("people", "scripts/rename.py", "import sys\nsys.stdin.read()\n")
	e.must("deck.manifest_save", map[string]any{"project": "people", "manifest": peopleV2("rename")}, nil)
	if rs := e.people(); len(rs) != 0 {
		t.Fatalf("the migration dropped every record: %+v", rs)
	}
	var man struct {
		Document int64 `json:"document"`
	}
	e.must("deck.manifest_get", map[string]any{"project": "people"}, &man)
	e.must("deck.document_revert", map[string]any{"document": man.Document, "revision": 1}, nil)
	if rs := e.people(); len(rs) != 1 || rs[0].ID != before[0].ID || rs[0].Data["name"] != "ada" {
		t.Fatalf("revert brings back records of a collection the migration emptied: %+v", rs)
	}
	var docs []struct {
		Name    string
		OwnerID int64 `json:"owner_id"`
	}
	e.must("deck.documents_list", map[string]any{"project": "people"}, &docs)
	if !slices.ContainsFunc(docs, func(d struct {
		Name    string
		OwnerID int64 `json:"owner_id"`
	}) bool {
		return d.Name == "notes.md" && d.OwnerID == before[0].ID
	}) {
		t.Fatalf("and the documents of those records: %+v", docs)
	}
}

// TestABoardOfABaseFromBeforeGoes: deck keeps no boards; the cards of an older base go with their lane triggers,
// documents and files, a topic of a card stays a topic, and a manifest of format 8 with a board still loads.
func TestABoardOfABaseFromBeforeGoes(t *testing.T) {
	home := t.TempDir()
	e := newEnvWith(t, home, &Fake{})
	e.createProject("work", plainManifest)
	e.olderBase(18)
	for _, q := range []string{
		`INSERT INTO card (project_id, title, lane, rank) SELECT id, 'Ship it', 'inbox', 1 FROM project WHERE name = 'work'`,
		`INSERT INTO card (project_id, title, lane, rank) SELECT id, 'Then this', 'inbox', 2 FROM project WHERE name = 'work'`,
		`INSERT INTO card_edge (card_id, waits_for) VALUES (2, 1)`,
		`INSERT INTO "trigger" (project_id, command, kind, spec) SELECT id, 'work', 'lane', 'inbox' FROM project WHERE name = 'work'`,
		`INSERT INTO document (workspace_id, project_id, card_id, kind, name, current) SELECT 1, id, 1, 'doc', 'spec.md', 1 FROM project WHERE name = 'work'`,
		`INSERT INTO document_revision (document_id, n, text, author) SELECT id, 1, 'the spec', 'owner' FROM document WHERE card_id = 1`,
		`INSERT INTO document (workspace_id, project_id, kind, name, current) SELECT 1, id, 'doc', 'notes.md', 0 FROM project WHERE name = 'work'`,
		`INSERT INTO chat_topic (workspace_id, title, card_id) VALUES (1, 'About the card', 1)`,
		`INSERT INTO file (project_id, owner_kind, owner_id, name) SELECT id, 'card', 1, 'scan.txt' FROM project WHERE name = 'work'`,
	} {
		if _, err := e.App.Store.DB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = e.App.Close()
	e = newEnvWith(t, home, &Fake{})
	for q, want := range map[string]int{
		`SELECT count(*) FROM sqlite_master WHERE name IN ('card', 'card_edge')`: 0,
		`SELECT count(*) FROM "trigger" WHERE kind = 'lane'`:                     0,
		`SELECT count(*) FROM document WHERE name = 'spec.md'`:                   0,
		`SELECT count(*) FROM document_revision WHERE text = 'the spec'`:         0,
		`SELECT count(*) FROM document WHERE name = 'notes.md'`:                  1,
		`SELECT count(*) FROM chat_topic WHERE title = 'About the card'`:         1,
	} {
		var n int
		if err := e.App.Store.DB.QueryRow(q).Scan(&n); err != nil || n != want {
			t.Fatalf("%s: %d, want %d (%v)", q, n, want, err)
		}
	}
	e.App.Sweep(t.Context(), e.App.Cfg.Now().Add(48*time.Hour))
	var files int
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM file WHERE name = 'scan.txt'`).Scan(&files)
	if files != 0 {
		t.Fatal("a file of a card goes with the sweep of files without an owner")
	}
	for _, name := range []string{"deck.cards_list", "deck.card_create", "deck.card_move", "deck.card_transfer", "deck.chat_interview"} {
		e.refused(name, map[string]any{"project": "work", "card": 1, "title": "x"}, 404, "unknown command")
	}
	eight := `{"version": 8, "commands": [{"name": "work", "title": "Work", "description": "Works.", "class": "internal",
    "input": {"type": "object", "properties": {"card": {"type": "integer"}}}, "executor": {"type": "agent", "prompt": "Work."}}],
  "triggers": [{"command": "work", "lane": "doing"}, {"command": "work", "cron": "0 9 * * *"}],
  "board": {"lanes": [{"name": "doing", "title": "Doing", "role": "inbox"}]},
  "screens": [{"name": "main", "title": "Main", "blocks": [{"type": "board", "title": "Work"}]}], "menu": ["main"]}`
	e.createProject("old", eight)
	if ts := e.triggers(); !slices.ContainsFunc(ts, func(tr triggerV) bool { return tr.Project == "old" && tr.Kind == "cron" }) ||
		slices.ContainsFunc(ts, func(tr triggerV) bool { return tr.Kind == "lane" }) {
		t.Fatalf("a manifest of format 8 loads without its lane triggers: %+v", ts)
	}
	if code, out := e.call("deck.screen", map[string]any{"project": "old", "screen": "main"}); code != 200 || strings.Contains(string(out), "board") {
		t.Fatalf("its screen shows without the board: %d %s", code, out)
	}
	nine := strings.Replace(strings.Replace(eight, `"version": 8`, `"version": 9`, 1), `{"command": "work", "lane": "doing"}, `, "", 1)
	e.refused("deck.manifest_save", map[string]any{"project": "old", "manifest": nine}, 422, "board")
}
