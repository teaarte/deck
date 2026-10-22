package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"deck2/internal/provider"
)

type faultView struct {
	ID           int64
	State        string
	Error        string
	ErrorType    string  `json:"error_type"`
	Whose        string  `json:"whose"`
	ErrorDetails string  `json:"error_details"`
	Follows      int64   `json:"follows"`
	FollowedBy   []int64 `json:"followed_by"`
	Repeats      int64   `json:"repeats"`
	Retry        bool
}

func (e *Env) fault(id int64) faultView {
	e.T.Helper()
	var v faultView
	e.must("deck.process_get", map[string]any{"process": id}, &v)
	return v
}

func (e *Env) failedRuns() []faultView {
	e.T.Helper()
	var out []faultView
	e.must("deck.processes_list", map[string]any{"failed": true}, &out)
	return out
}

// TestAScriptReportsItsFailureInTheOneForm: {"error", "type", "details"} a script prints as it fails gives its failure,
// a type a script may not give is script.failed; with stderr alone its last line is the text, the rest the details.
func TestAScriptReportsItsFailureInTheOneForm(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("work", scriptManifest("run", "", ""))
	e.saveScript("work", "scripts/main.py", `import json, sys
inp = json.load(sys.stdin)
if inp.get("type") == "stderr":
    print("Traceback (most recent call last):", file=sys.stderr)
    print("ValueError: the feed is empty", file=sys.stderr)
    sys.exit(1)
print(json.dumps({"error": "the date is not a date", "type": inp.get("type"), "details": {"field": "date"}}))
sys.exit(2)
`)
	got := e.fault(e.waitScript(e.start("work", "run", map[string]any{"type": "input.invalid"}), "error").ID)
	if got.ErrorType != "input.invalid" || got.Whose != "module" || got.Error != "script main: the date is not a date" ||
		!strings.Contains(got.ErrorDetails, `"date"`) {
		t.Fatalf("the failure the script reported, with its details apart: %+v", got)
	}
	got = e.fault(e.waitScript(e.start("work", "run", map[string]any{"type": "deck.internal"}), "error").ID)
	if got.ErrorType != "script.failed" {
		t.Fatalf("a script cannot say the failure is deck's: %+v", got)
	}
	got = e.fault(e.waitScript(e.start("work", "run", map[string]any{"type": "stderr"}), "error").ID)
	if got.ErrorType != "script.failed" || !strings.HasSuffix(got.Error, "ValueError: the feed is empty") ||
		!strings.Contains(got.ErrorDetails, "Traceback") {
		t.Fatalf("stderr: the last line is the text, the rest the details: %+v", got)
	}
	e.saveScript("work", "scripts/main.py", "print('loading the feed')\n")
	got = e.fault(e.waitScript(e.start("work", "run", nil), "error").ID)
	if got.ErrorType != "result.invalid" || got.Whose != "module" {
		t.Fatalf("a script that prints a line, not JSON, fails as the module's: %+v", got)
	}
}

// TestTheSameFailuresAreOneRowOfTheCockpit: failures of a command with the same type and text are one row with how
// many there were; closing it closes them all; a retry follows the failure and takes it off while it runs.
func TestTheSameFailuresAreOneRowOfTheCockpit(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("work", scriptManifest("run", "", ""))
	e.saveScript("work", "scripts/main.py", `import json, sys
inp = json.load(sys.stdin)
if inp.get("n") == 0:
    print(json.dumps({}))
    sys.exit(0)
print(json.dumps({"error": "the site is down", "type": "network"}))
sys.exit(1)
`)
	var ids []int64
	for i := 1; i <= 3; i++ {
		ids = append(ids, e.waitScript(e.start("work", "run", map[string]any{"n": i}), "error").ID)
	}
	rows := e.failedRuns()
	if len(rows) != 1 || rows[0].ID != ids[2] || rows[0].Repeats != 3 || rows[0].ErrorType != "network" || rows[0].Whose != "environment" {
		t.Fatalf("three of the same failure are one row, the newest, counted: %+v", rows)
	}
	var items []listItem
	_ = json.Unmarshal(e.screenData("", "cockpit", nil)["b1"]["items"], &items)
	if len(items) != 1 || !strings.Contains(items[0].Meta, "×3") || !strings.Contains(items[0].Details, "network") {
		t.Fatalf("the cockpit line says how many, its details the type: %+v", items)
	}
	page, _ := json.Marshal(e.screenData("", "process", map[string]any{"record": ids[2]}))
	if !strings.Contains(string(page), "network · environment") || !strings.Contains(string(page), "Failure type") {
		t.Fatalf("the page of the process says the type and whose it is: %s", page)
	}
	e.must("deck.process_dismiss", map[string]any{"process": ids[2]}, nil)
	if rows := e.failedRuns(); len(rows) != 0 {
		t.Fatalf("closing the row closes the failures it stands for: %+v", rows)
	}
	failed := e.waitScript(e.start("work", "run", map[string]any{"n": 7}), "error").ID
	var again struct{ Process int64 }
	e.must("deck.process_retry", map[string]any{"process": failed}, &again)
	e.waitScript(again.Process, "error")
	if got := e.fault(again.Process); got.Follows != failed {
		t.Fatalf("a retry follows the failure it repeats: %+v", got)
	}
	if got := e.fault(failed); len(got.FollowedBy) != 1 || got.FollowedBy[0] != again.Process {
		t.Fatalf("the failure names the run that repeated it: %+v", got)
	}
	if rows := e.failedRuns(); len(rows) != 1 || rows[0].ID != again.Process {
		t.Fatalf("only the end of the chain is in the cockpit: %+v", rows)
	}
}

// TestTheSameNoteOfDeckIsToldOnceWithACount: deck's same unread note in a project is one note with how many times it
// came; once read, the next one is a new note.
func TestTheSameNoteOfDeckIsToldOnceWithACount(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("work", scriptManifest("run", `{"type": "object", "required": ["ok"]}`, ""))
	e.saveScript("work", "scripts/main.py", "print('{}')\n")
	for range 2 {
		e.waitScript(e.start("work", "run", nil), "done")
	}
	var notes []struct {
		ID      int64
		Text    string
		Repeats int64
	}
	told := func(n int, repeats int64) bool {
		e.must("deck.feed_list", map[string]any{"kind": "note"}, &notes)
		return len(notes) == n && notes[0].Repeats == repeats
	}
	e.waitFor("one note counted twice", func() bool { return told(1, 2) })
	if !strings.Contains(notes[0].Text, "does not match its schema") {
		t.Fatalf("the same note twice is one note counted twice: %+v", notes)
	}
	e.must("deck.feed_read", map[string]any{"entry": notes[0].ID}, nil)
	e.waitScript(e.start("work", "run", nil), "done")
	e.waitFor("a new note after the read one", func() bool { return told(2, 1) })
}

// TestTheMasterGetsTheTypeOfAFailure: on_error gets the type of the failure and whose it is; a failure of a model
// does not go to the master.
func TestTheMasterGetsTheTypeOfAFailure(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "work" {
			return provider.Result{Unavailable: true, IsError: true, Text: "API Error: 529 overloaded"}
		}
		return masterScript(ctx, c)
	})
	e.masterAwake()
	p := e.createProject("work", plainManifest)
	failed := e.waitState(e.run(p, "work"), "error")
	if got := e.fault(failed.ID); got.ErrorType != "model.unavailable" || got.Whose != "model" {
		t.Fatalf("a model that does not answer is the model's failure: %+v", got)
	}
	e.App.Runner.Wait()
	if n := len(e.runsOf("master", "on_error")); n != 0 {
		t.Fatalf("a failure of a model does not go to the master: %d", n)
	}
	e.createProject("other", scriptManifest("run", "", ""))
	e.saveScript("other", "scripts/main.py", "import sys\nprint('no', file=sys.stderr)\nsys.exit(3)\n")
	for range 3 {
		e.waitScript(e.start("other", "run", nil), "error")
	}
	e.waitFor("on_error", func() bool { return len(e.runsOf("master", "on_error")) == 1 })
	got, _ := e.App.Store.Q.GetProcess(context.Background(), e.runsOf("master", "on_error")[0].ID)
	if !strings.Contains(got.Input, `"type":"script.failed"`) || !strings.Contains(got.Input, `"whose":"module"`) {
		t.Fatalf("the master gets the type and whose it is: %s", got.Input)
	}
}

// callScript calls deck at key with the body other and, refused, fails with the type it is given or with a bare
// traceback when none; with use it handles the refusal, calls deck again and fails on a bug of its own.
const callScript = `import json, os, sys, urllib.request, urllib.error
inp = json.load(sys.stdin)
req = urllib.request.Request(os.environ["DECK_URL"] + "/" + inp["key"], data=inp["other"].encode(),
    headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
try:
    urllib.request.urlopen(req, timeout=60)
except urllib.error.HTTPError as e:
    if inp.get("use"):
        ok = urllib.request.Request(os.environ["DECK_URL"] + "/commands/deck.records_list", data=b'{"collection": "items"}',
            headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
        urllib.request.urlopen(ok, timeout=60)
        raise KeyError("a bug after the refusal was handled")
    if inp.get("type"):
        print(json.dumps({"error": "deck said no", "type": inp["type"]}))
        sys.exit(1)
    raise
print("{}")
`

// TestAScriptFailedOnDecksAnswerTakesItsType: a script failing on deck's error answer with no type takes its type;
// a type the script names stays, and a refusal it handled before a later call is not its type.
func TestAScriptFailedOnDecksAnswerTakesItsType(t *testing.T) {
	t.Parallel()
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(_ int, body map[string]any) (int, string, string) {
			if body["model"] == "m2" {
				return 500, "application/json", `{"error": {"message": "overloaded"}}`
			}
			return 429, "application/json", `{"error": {"message": "rate limited"}}`
		},
	})
	e := newEnv(t, nil)
	e.must("deck.secret_set", map[string]any{"name": "cloud", "value": "sk-cloud"}, nil)
	e.must("deck.connection_set", map[string]any{"name": "cloud", "api": "openai", "url": api.URL + "/v1", "key_secret": "cloud"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "cloud", "model": "m1", "kind": "text", "name": "m1"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "cloud", "model": "m2", "kind": "text", "name": "m2"}, nil)
	e.createProject("calls", scriptManifest("call", "", `, "models": ["cloud"]`))
	e.saveScript("calls", "scripts/main.py", callScript)
	for _, c := range []struct{ key, body, named, use, want, says string }{
		{"commands/deck.record_get", `{"record": 987654}`, "", "", "input.invalid", "deck refused its calls: deck.record_get: 404"},
		{"commands/deck.record_get", `{"record": 987654}`, "script.failed", "", "input.invalid", "deck.record_get: 404"},
		{"commands/deck.settings_list", `{}`, "", "", "rights.denied", "deck.settings_list: 403"},
		{"commands/deck.record_get", `{"record": 987654}`, "network", "", "network", "deck.record_get: 404"},
		{"commands/deck.record_get", `{"record": 987654}`, "", "handled", "script.failed", "deck.record_get: 404"},
		{"models/cloud/chat/completions", `{"model": "m1", "messages": [{"role": "user", "content": "hi"}]}`, "", "", "model.limit", "the model proxy cloud: 429"},
		{"models/cloud/chat/completions", `{"model": "m2", "messages": [{"role": "user", "content": "hi"}]}`, "", "", "model.unavailable", "the model proxy cloud: 502"},
	} {
		got := e.waitScript(e.start("calls", "call", map[string]any{"key": c.key, "other": c.body, "type": c.named, "use": c.use}), "done", "error")
		f := e.fault(got.ID)
		if f.ErrorType != c.want || !strings.Contains(f.Error, c.says) {
			t.Fatalf("%s %q: want %s with %q, got %s: %s", c.key, c.named, c.want, c.says, f.ErrorType, f.Error)
		}
	}
}

// TestTheHelpersOfTheSkillRunAsScripts: module-builder's helpers run as scripts in the sandbox: deck's answer is the
// details of a failure deck types, a named type stays, runs side by side add to one file, no .tmp left.
func TestTheHelpersOfTheSkillRunAsScripts(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	for _, c := range []struct{ runtime, file string }{{"python", "deck_helper.py"}, {"node", "deck_helper.mjs"}} {
		t.Run(c.runtime, func(t *testing.T) {
			if _, err := exec.LookPath(map[string]string{"python": "python3", "node": "node"}[c.runtime]); err != nil {
				t.Skip(c.runtime + " is not installed")
			}
			var doc struct{ Text string }
			e.must("deck.document_get", map[string]any{"name": "skills/module-builder/helpers/" + c.file}, &doc)
			name := "h" + c.runtime
			script := "scripts/main" + filepath.Ext(c.file)
			e.createProject(name, fmt.Sprintf(`{"version": 13,
  "commands": [{"name": "step", "title": "Step", "description": "Adds a value.", "class": "internal",
    "input": {"type": "object", "properties": {"record": {"type": "integer"}, "value": {"type": "string"}}},
    "result": {"type": "object"}, "executor": {"type": "script", "script": "main"}}],
  "scripts": [{"name": "main", "file": %q, "runtime": %q}]}`, script, c.runtime))
			e.saveScript(name, script, doc.Text)

			f := e.fault(e.waitScript(e.start(name, "step", map[string]any{"record": 987654}), "error").ID)
			if f.ErrorType != "input.invalid" || !strings.Contains(f.Error, "deck.record_get: deck answered 404") ||
				!strings.Contains(f.ErrorDetails, "987654") {
				t.Fatalf("deck's answer is the details and deck types the failure: %+v", f)
			}
			f = e.fault(e.waitScript(e.start(name, "step", map[string]any{}), "error").ID)
			if f.ErrorType != "input.invalid" || !strings.Contains(f.Error, "no value to add") {
				t.Fatalf("the failure the script names: %+v", f)
			}
			var ids []int64
			for _, v := range []string{"a", "b", "c", "d"} {
				ids = append(ids, e.start(name, "step", map[string]any{"value": v}))
			}
			for _, id := range ids {
				if p := e.waitScript(id, "done", "error"); p.State != "done" {
					t.Fatalf("a run side by side: %+v", e.fault(id))
				}
			}
			last := e.waitScript(e.start(name, "step", map[string]any{"value": "e"}), "done", "error")
			var out struct {
				Values, Files []string
			}
			if last.State != "done" || json.Unmarshal(last.Result, &out) != nil {
				t.Fatalf("the last run: %+v", last)
			}
			got := slices.Sorted(slices.Values(out.Values))
			if strings.Join(got, ",") != "a,b,c,d,e" ||
				!slices.Contains(out.Files, "values.json") || strings.Contains(strings.Join(out.Files, ","), ".tmp") {
				t.Fatalf("the file is replaced whole: %+v %s", last, last.Result)
			}
		})
	}
}
