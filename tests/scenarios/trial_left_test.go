package scenarios

import (
	"encoding/json"
	"strings"
	"testing"
)

const leftManifest = `{"version": 12,
  "collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}],
  "commands": [{"name": "collect", "title": "Collect", "description": "Collects an item.", "class": "internal",
    "result": {"type": "object"}, "executor": {"type": "script", "script": "collect"}}],
  "writes": ["notes.md"],
  "scripts": [{"name": "collect", "file": "scripts/collect.py", "runtime": "python"}]}`

const leftScript = pythonLib + `
st, out = deck("deck.records_upsert", {"collection": "items", "records": json.dumps({"name": "a"})})
assert st == 200, out
st, out = deck("deck.document_save", {"name": "notes.md", "kind": "doc", "text": "the trial wrote this"})
assert st == 200, out
with open(os.path.join(os.environ["DECK_RESULTS"], "found.txt"), "w") as f:
    f.write("found")
with open(os.path.join(os.environ["DECK_DATA"], "log.jsonl"), "a") as f:
    f.write("{}\n")
print(json.dumps({"files": [{"path": "found.txt"}]}))
`

type leftRow struct {
	Kind string
	ID   int64
	Text string
	Undo bool
}

// leftBlock is the table of what a process left on its page, its rows and the buttons of each; false when the page
// leaves it out.
func (e *Env) leftBlock(id int64) (screenTable, bool) {
	e.T.Helper()
	in := map[string]any{"record": id}
	for _, m := range e.screenMessages("", "process", in) {
		var uc struct{ Components []map[string]any }
		if json.Unmarshal(m["updateComponents"], &uc) != nil {
			continue
		}
		for _, c := range uc.Components {
			if c["title"] == "What it left" {
				return blockData[screenTable](e.T, mustJSON(e.screenData("", "process", in)[c["id"].(string)])), true
			}
		}
	}
	return screenTable{}, false
}

type screenTable struct {
	Rows []screenRow `json:"rows"`
}

// TestATrialListsWhatItLeftWithItsUndo: a trial in an enabled project tells the master what it left — the record,
// the file of deck and the change of a document —, says what is not listed, and its page undoes each.
func TestATrialListsWhatItLeftWithItsUndo(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("finds", leftManifest)
	e.saveScript("finds", "scripts/collect.py", leftScript)
	e.must("deck.document_save", map[string]any{"project": "finds", "name": "notes.md", "kind": "doc", "text": "the owner wrote this"}, nil)
	var tried struct{ Process int64 }
	e.must("deck.command_try", map[string]any{"live": true, "command": "finds.collect"}, &tried)
	if p := e.waitScript(tried.Process, "done", "error"); p.State != "done" {
		t.Fatalf("the trial runs: %+v", p)
	}
	var got struct {
		Left     []leftRow
		LeftNote string `json:"left_note"`
	}
	e.must("deck.process_get", map[string]any{"process": tried.Process}, &got)
	kinds := map[string]leftRow{}
	for _, l := range got.Left {
		kinds[l.Kind] = l
	}
	if len(got.Left) != 3 || !kinds["change"].Undo || !strings.Contains(kinds["change"].Text, "notes.md") ||
		!strings.Contains(kinds["record"].Text, `record "a" of finds.items`) || !strings.Contains(kinds["file"].Text, `file "found.txt" of finds`) {
		t.Fatalf("the trial lists the change, the record and the file it left: %+v", got.Left)
	}
	if !strings.Contains(got.LeftNote, "data folder of the project is not listed and is not undone") {
		t.Fatalf("it says what it does not list: %q", got.LeftNote)
	}

	left, ok := e.leftBlock(tried.Process)
	rows := left.Rows
	if !ok || len(rows) != 3 {
		t.Fatalf("the page of the trial lists what it left: %+v", rows)
	}
	for _, r := range rows {
		acts := r.Actions
		var a map[string]any
		switch {
		case hasVerb(acts, "undo"):
			a = verb(t, acts, "undo")
		case hasVerb(acts, "delete"):
			a = verb(t, acts, "delete")
		default:
			t.Fatalf("each thing it left has its button: %+v", r)
		}
		if code, out := e.press(a); code != 200 {
			t.Fatalf("%v: %d %s", a, code, out)
		}
	}
	var doc struct{ Text string }
	e.must("deck.document_get", map[string]any{"project": "finds", "name": "notes.md"}, &doc)
	var recs []map[string]any
	e.must("deck.records_list", map[string]any{"project": "finds", "collection": "items"}, &recs)
	if doc.Text != "the owner wrote this" || len(recs) != 0 {
		t.Fatalf("the buttons undo the change and delete the record: %q %v", doc.Text, recs)
	}
	if left, ok := e.leftBlock(tried.Process); ok {
		t.Fatalf("a trial that left nothing has no table of it: %+v", left.Rows)
	}
	var plain struct{ Left []leftRow }
	run := e.waitScript(e.start("finds", "collect", nil), "done").ID
	e.must("deck.process_get", map[string]any{"process": run}, &plain)
	if plain.Left != nil {
		t.Fatalf("only a trial lists in process_get what it left: %+v", plain.Left)
	}
	if left, ok := e.leftBlock(run); ok {
		t.Fatalf("only the page of a trial has the table of what it left: %+v", left.Rows)
	}
}
