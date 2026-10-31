package modules

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

var leftManifest = harness.Manifest(`{"collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}],
  "commands": [{"name": "collect", "title": "Collect", "description": "Collects an item.", "class": "internal",
    "result": {"type": "object"}, "executor": {"type": "script", "script": "collect"}}],
  "writes": ["notes.md"],
  "scripts": [{"name": "collect", "file": "scripts/collect.py", "runtime": "python"}]}`)

const leftScript = harness.PythonLib + `
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
func leftBlock(e *harness.Env, id int64) (harness.ScreenTable, bool) {
	e.T.Helper()
	in := map[string]any{"record": id}
	for _, m := range e.ScreenMessages("", "process", in) {
		var uc struct{ Components []map[string]any }
		if json.Unmarshal(m["updateComponents"], &uc) != nil {
			continue
		}
		for _, c := range uc.Components {
			if c["title"] == "What it left" {
				return harness.BlockData[harness.ScreenTable](e.T, harness.MustJSON(e.Screen("", "process", in)[c["id"].(string)])), true
			}
		}
	}
	return harness.ScreenTable{}, false
}

// TestATrialListsWhatItLeftWithItsUndo: a trial in an enabled project tells the master what it left — the record,
// the file of deck and the change of a document —, says what is not listed, and its page undoes each.
func TestATrialListsWhatItLeftWithItsUndo(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("finds", leftManifest)
	e.SaveScript("finds", "scripts/collect.py", leftScript)
	e.Must("deck.document_save", map[string]any{"project": "finds", "name": "notes.md", "kind": "doc", "text": "the owner wrote this"}, nil)
	var tried struct{ Process int64 }
	e.Must("deck.command_try", map[string]any{"live": true, "command": "finds.collect"}, &tried)
	if p := e.WaitState(tried.Process, "done", "error"); p.State != "done" {
		t.Fatalf("the trial runs: %+v", p)
	}
	var got struct {
		Left     []leftRow
		LeftNote string `json:"left_note"`
	}
	e.Must("deck.process_get", map[string]any{"process": tried.Process}, &got)
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

	left, ok := leftBlock(e, tried.Process)
	rows := left.Rows
	if !ok || len(rows) != 3 {
		t.Fatalf("the page of the trial lists what it left: %+v", rows)
	}
	for _, r := range rows {
		acts := r.Actions
		var a map[string]any
		switch {
		case harness.HasVerb(acts, "undo"):
			a = harness.Verb(t, acts, "undo")
		case harness.HasVerb(acts, "delete"):
			a = harness.Verb(t, acts, "delete")
		default:
			t.Fatalf("each thing it left has its button: %+v", r)
		}
		if code, out := e.Press(a); code != 200 {
			t.Fatalf("%v: %d %s", a, code, out)
		}
	}
	var doc struct{ Text string }
	e.Must("deck.document_get", map[string]any{"project": "finds", "name": "notes.md"}, &doc)
	var recs []map[string]any
	e.Must("deck.records_list", map[string]any{"project": "finds", "collection": "items"}, &recs)
	if doc.Text != "the owner wrote this" || len(recs) != 0 {
		t.Fatalf("the buttons undo the change and delete the record: %q %v", doc.Text, recs)
	}
	if left, ok := leftBlock(e, tried.Process); ok {
		t.Fatalf("a trial that left nothing has no table of it: %+v", left.Rows)
	}
	var plain struct{ Left []leftRow }
	run := e.WaitState(e.Start("finds", "collect", nil), "done").ID
	e.Must("deck.process_get", map[string]any{"process": run}, &plain)
	if plain.Left != nil {
		t.Fatalf("only a trial lists in process_get what it left: %+v", plain.Left)
	}
	if left, ok := leftBlock(e, run); ok {
		t.Fatalf("only the page of a trial has the table of what it left: %+v", left.Rows)
	}
}

const trialScript = harness.PythonLib + `
st, out = deck("deck.records_upsert", {"collection": "items", "records": json.dumps({"name": "a"})})
internal = st == 200
st, out = deck("tried.publish", {})
print(json.dumps({"internal": internal, "external": [st, out]}))
`

func TestTrialScriptWritesInsideDeckAndIsRefusedOutside(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("tried", harness.ScriptManifest("run", "", ""))
	e.SaveScript("tried", "scripts/main.py", trialScript)
	var try struct{ Process int64 }
	e.Must("deck.command_try", map[string]any{"live": true, "command": "tried.run", "input": "{}"}, &try)
	p := e.WaitState(try.Process, "done")
	var res struct {
		Internal bool
		External []any
	}
	_ = json.Unmarshal(p.Result, &res)
	if !res.Internal || len(res.External) != 2 || res.External[0] != 403.0 ||
		res.External[1] != "deck: trial run: tried.publish is external, so it was declined\n" {
		t.Fatalf("a trial script writes records and is refused anything external with the trial reason: %s", p.Result)
	}
	var recs []any
	e.Must("deck.records_list", map[string]any{"project": "tried", "collection": "items"}, &recs)
	if len(recs) != 1 {
		t.Fatalf("internal writes of a trial stay: %v", recs)
	}
	if code, out := e.Call("deck.command_try", map[string]any{"command": "deck.projects_list"}); code != 422 {
		t.Fatalf("only project commands have trials: %d %s", code, out)
	}
}

// TestATrialInAnEnabledProjectIsAskedForAndNeverRunsBesideItsRealRun: a trial writes for real, so in an
// enabled project it takes live, and it waits until a real run of the command is over.
func TestATrialInAnEnabledProjectIsAskedForAndNeverRunsBesideItsRealRun(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))

	e.Project("keys", harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil))
	var real, again struct{ Process int64 }
	e.Must("keys.work", map[string]any{"retry_key": "k"}, &real)
	e.Refused("deck.command_try", map[string]any{"command": "keys.work"}, 422, "draft copy")
	e.Refused("deck.command_try", map[string]any{"live": true, "command": "keys.work", "input": `{"retry_key": "k"}`}, 409,
		fmt.Sprintf("running now (process %d)", real.Process))
	e.Must("keys.work", map[string]any{"retry_key": "k"}, &again)
	if again.Process != real.Process || trial(e, real.Process) {
		t.Fatalf("a real run with the same key is the one going on: real %d, again %d", real.Process, again.Process)
	}
	close(release)
	e.WaitState(real.Process, "done")
	var try struct{ Process int64 }
	e.Must("deck.command_try", map[string]any{"live": true, "command": "keys.work"}, &try)
	if e.WaitState(try.Process, "done"); !trial(e, try.Process) {
		t.Fatalf("once the real run is over the trial runs: %d", try.Process)
	}
}

func TestScriptRunsOnlyFromAScriptDocument(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("kinds", harness.ScriptManifest("run", "", ""))
	e.Must("deck.document_save", map[string]any{"project": "kinds", "name": "scripts/main.py", "kind": "doc", "text": "print('{}')\n"}, nil)
	if code, out := e.Call("kinds.run", map[string]any{}); code != 422 || !strings.Contains(string(out), "not a script document") {
		t.Fatalf("a script file stored as another kind does not run: %d %s", code, out)
	}
}

// TestTrialOfAToolIsRefusedAndNothingLeavesDeck: a tool is the call itself, so it has no trial run; a token
// that may try runs any command of a project as a trial, whose agent calls a tool only on the owner's yes.
func TestTrialOfAToolIsRefusedAndNothingLeavesDeck(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"ok": true}`) })
	var posted string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.N == 1 {
			posted, _ = c.Tool(ctx, "notes__post", map[string]any{"id": "a", "text": "hi"})
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}))

	e.Secret("notes", "svc", "s3cret")
	e.Project("notes", harness.PostManifest(svc.URL, ""))
	_, tok := e.Token("terminal", "deck.command_try")
	for _, call := range []func() (int, []byte){
		func() (int, []byte) {
			return e.Call("deck.command_try", map[string]any{"live": true, "command": "notes.post", "input": `{"id": "a", "text": "hi"}`})
		},
		func() (int, []byte) {
			return e.As(tok).Call("deck.command_try", map[string]any{"live": true, "command": "notes.post", "input": `{"id": "a", "text": "hi"}`})
		},
	} {
		if code, out := call(); code < 400 || len(svc.Requests()) != 0 {
			t.Fatalf("a trial of a tool is refused: %d %s, %d calls", code, out, len(svc.Requests()))
		}
	}
	var try struct{ Process int64 }
	code, out := e.As(tok).Call("deck.command_try", map[string]any{"live": true, "command": "notes.work"})
	if code != 200 || json.Unmarshal(out, &try) != nil {
		t.Fatalf("a token that may try runs a command of any project as a trial: %d %s", code, out)
	}
	e.WaitState(try.Process, "waiting_answer")
	var held struct{ Waiting int64 }
	if json.Unmarshal([]byte(posted), &held) != nil || held.Waiting == 0 || len(svc.Requests()) != 0 {
		t.Fatalf("a tool call of a trial waits for the owner's yes on that call: %q, %d calls", posted, len(svc.Requests()))
	}
	e.Must("deck.ask_answer", map[string]any{"ask": held.Waiting, "answer": "allow"}, nil)
	if seen := svc.Requests(); len(seen) != 1 || seen[0].Header.Get("X-Key") != "s3cret" {
		t.Fatalf("the owner's yes makes that one call: %+v", seen)
	}
	e.WaitState(try.Process, "done")
	e.Must("deck.manifest_save", map[string]any{"project": "notes",
		"manifest": strings.Replace(harness.PostManifest(svc.URL, ""), `"class": "internal",`, `"class": "internal", "callers": ["owner"],`, 1)}, nil)
	if code, out := e.As(tok).Call("deck.command_try", map[string]any{"live": true, "command": "notes.work"}); code != 403 || !strings.Contains(string(out), "cannot be called by master") {
		t.Fatalf("a command closed to the master is not tried by a token: %d %s", code, out)
	}
}

// TestATrialOfOneCommandStartsNoChain: a trial of only this command does not start the commands that run after
// it; a trial of the chain, as before, does.
func TestATrialOfOneCommandStartsNoChain(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("chain", harness.TestManifest([]string{harness.AgentCommand("c1", ""), harness.AgentCommand("c2", harness.ProcessField)},
		[]string{`{"command": "c2", "after": "c1"}`}))
	var try struct{ Process int64 }
	e.Must("deck.command_try", map[string]any{"live": true, "command": "chain.c1", "only": true}, &try)
	e.WaitState(try.Process, "done")
	e.Settle()
	if n := e.CountOf("c2"); n != 0 {
		t.Fatalf("a trial of only c1 starts no c2: %d", n)
	}
	e.Must("deck.command_try", map[string]any{"live": true, "command": "chain.c1"}, &try)
	e.WaitState(try.Process, "done")
	e.Until("the trial of the chain", func() bool { return e.CountOf("c2") == 1 })
}

// TestATrialOfAToolCommandWaitsForTheOwner: in a trial or a draft the tool of a tool command is not cleared with
// the command, so its call waits for the owner's yes before anything leaves deck.
func TestATrialOfAToolCommandWaitsForTheOwner(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"ok": true}`) })
	e := harness.New(t)
	e.Secret("notes", "svc", "s3cret")
	send := strings.Replace(harness.PostManifest(svc.URL, ""), `"commands": [`, `"commands": [{"name": "send", "title": "Send",
    "description": "Sends the note.", "class": "external", "executor": {"type": "tool", "tool": "post"},
    "input": {"type": "object", "properties": {"id": {"type": "string"}, "text": {"type": "string"}}, "required": ["id", "text"]}},`, 1)
	e.Project("notes", send)
	_, tok := e.Token("terminal", "deck.project_create", "deck.command_try")
	if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "drafted", "manifest": send}); code != 200 {
		t.Fatalf("create: %d %s", code, out)
	}
	for _, call := range []func() (int, []byte){
		func() (int, []byte) {
			return e.Call("deck.command_try", map[string]any{"live": true, "command": "notes.send", "input": `{"id": "a", "text": "hi"}`})
		},
		func() (int, []byte) {
			return e.As(tok).Call("deck.command_try", map[string]any{"command": "drafted.send", "input": `{"id": "a", "text": "hi"}`})
		},
	} {
		var try struct{ Process int64 }
		code, out := call()
		if code != 200 || json.Unmarshal(out, &try) != nil || try.Process == 0 {
			t.Fatalf("try: %d %s", code, out)
		}
		if p := e.WaitState(try.Process, "waiting_answer", "done", "error"); p.State != "waiting_answer" || len(svc.Requests()) != 0 {
			t.Fatalf("the tool call of a trial waits for the owner: %s, %d calls", p.State, len(svc.Requests()))
		}
	}
}
