package scenarios

import (
	"context"
	"slices"
	"strings"
	"testing"

	"deck2/internal/docs"
	"deck2/internal/store/db"

	_ "modernc.org/sqlite"
)

const peopleV1 = `{"version": 13,
  "collections": [{"name": "people", "description": "A person.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}]}`

func peopleV2(migration string) string {
	m := `{"version": 13,
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
	t.Parallel()
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
	t.Parallel()
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

// TestDeckKeepsNoBoards: deck has no commands of boards or cards, and a manifest with a board, a board block or a
// lane trigger is refused.
func TestDeckKeepsNoBoards(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	for _, name := range []string{"deck.cards_list", "deck.card_create", "deck.card_move", "deck.card_transfer", "deck.chat_interview"} {
		e.refused(name, map[string]any{"project": "work", "card": 1, "title": "x"}, 404, "unknown command")
	}
	plain := `{"version": 13, "commands": [{"name": "work", "title": "Work", "description": "Works.", "class": "internal",
    "executor": {"type": "agent", "prompt": "Work."}}],
  "triggers": [{"command": "work", "cron": "0 9 * * *"}],
  "screens": [{"name": "main", "title": "Main", "blocks": [{"type": "actions", "title": "Work", "actions": [{"label": "Work", "command": "work"}]}]}], "menu": ["main"]}`
	e.createProject("work", plain)
	for _, c := range []struct{ old, new, says string }{
		{`"triggers": [`, `"board": {"lanes": [{"name": "doing", "title": "Doing", "role": "inbox"}]}, "triggers": [`, "board"},
		{`{"type": "actions", "title": "Work", "actions": [{"label": "Work", "command": "work"}]}`, `{"type": "board", "title": "Work"}`, "/screens/0/blocks/0/type"},
		{`{"command": "work", "cron": "0 9 * * *"}`, `{"command": "work", "lane": "doing"}`, "lane"},
	} {
		m := strings.Replace(plain, c.old, c.new, 1)
		if m == plain {
			t.Fatalf("fixture replacement did not apply: %s", c.old)
		}
		e.refused("deck.manifest_save", map[string]any{"project": "work", "manifest": m}, 422, c.says)
	}
}

// TestADeckThatRefusesASavedManifestSaysSoOnce: a manifest saved by an older deck that the checks of this one refuse
// works on, and its project gets one note of what to fix; a clean project gets none, nor does a second start.
func TestADeckThatRefusesASavedManifestSaysSoOnce(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := newEnvWith(t, home, &Fake{})
	e.createProject("clean", plainManifest)
	p := e.createProject("old", plainManifest)
	older := strings.Replace(plainManifest, `"screens": []`, `"screens": [], "domains": {"read": ["https://example.com/feed"]}`, 1)
	err := e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		_, _, _, err := docs.Save(ctx, q, docs.Ref{Scope: docs.Scope{Workspace: 1, Project: p}, Name: docs.ManifestName}, docs.KindManifest, older, "deck", "an older deck")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	restart := func() {
		_ = e.App.Close()
		e = newEnvWith(t, home, &Fake{})
	}
	restart()
	n := e.waitNote("The manifest of old")
	if !strings.Contains(n.Text, "domains.read") || !strings.Contains(n.Text, "not a URL") {
		t.Fatalf("the note says what to fix: %s", n.Text)
	}
	for _, f := range e.notes() {
		if strings.Contains(f.Text, "clean") {
			t.Fatalf("a manifest the checks pass gets no note: %s", f.Text)
		}
	}
	if code, out := e.call("deck.screen", map[string]any{"project": "old", "screen": "documents"}); code != 200 {
		t.Fatalf("the project works on: %d %s", code, out)
	}
	restart()
	count := 0
	for _, f := range e.notes() {
		if strings.Contains(f.Text, "The manifest of old") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("the same problems are told once: %d notes", count)
	}
}
