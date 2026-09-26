package scenarios

import (
	"strings"
	"testing"
)

const peopleV1 = `{"version": 4,
  "collections": [{"name": "people", "description": "A person.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}],
  "board": {"lanes": [{"name": "inbox", "title": "Inbox", "role": "inbox"}]}}`

func peopleV2(migration string) string {
	m := `{"version": 4,
  "collections": [{"name": "people", "description": "A person.", "key": "full_name", MIGRATION
    "schema": {"type": "object", "properties": {"full_name": {"type": "string"}}, "required": ["full_name"], "additionalProperties": false}}],
  "scripts": [{"name": "rename", "file": "scripts/rename.py", "runtime": "python"}],
  "board": {"lanes": [{"name": "inbox", "title": "Inbox", "role": "inbox"}]}}`
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
	var card struct{ ID int64 }
	e.must("deck.card_create", map[string]any{"project": "people", "title": "Call Ada", "record": before[0].ID}, &card)

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
	var got struct {
		Record int64 `json:"record"`
	}
	e.must("deck.card_get", map[string]any{"card": card.ID}, &got)
	if got.Record != before[0].ID {
		t.Fatalf("the card still points at its record: %+v", got)
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
}
