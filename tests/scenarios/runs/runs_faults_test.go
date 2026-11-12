package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/fault"
	"deck2/internal/provider"
	"deck2/internal/sandbox"
	"deck2/tests/harness"
)

// TestAScriptReportsItsFailureInTheOneForm: {"error", "type", "details"} a script prints as it fails gives its failure,
// a type a script may not give is script.failed; with stderr alone its last line is the text, the rest the details.
func TestAScriptReportsItsFailureInTheOneForm(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("work", harness.ScriptManifest("run", "", ""))
	e.SaveScript("work", "scripts/main.py", `import json, sys
inp = json.load(sys.stdin)
if inp.get("type") == "stderr":
    print("Traceback (most recent call last):", file=sys.stderr)
    print("ValueError: the feed is empty", file=sys.stderr)
    sys.exit(1)
print(json.dumps({"error": "the date is not a date", "type": inp.get("type"), "details": {"field": "date"}}))
sys.exit(2)
`)
	got := e.Fault(e.WaitState(e.Start("work", "run", map[string]any{"type": "input.invalid"}), "error").ID)
	if got.ErrorType != "input.invalid" || got.Whose != "module" || got.Error != "script main: the date is not a date" ||
		!strings.Contains(got.ErrorDetails, `"date"`) {
		t.Fatalf("the failure the script reported, with its details apart: %+v", got)
	}
	got = e.Fault(e.WaitState(e.Start("work", "run", map[string]any{"type": "deck.internal"}), "error").ID)
	if got.ErrorType != "script.failed" {
		t.Fatalf("a script cannot say the failure is deck's: %+v", got)
	}
	got = e.Fault(e.WaitState(e.Start("work", "run", map[string]any{"type": "stderr"}), "error").ID)
	if got.ErrorType != "script.failed" || !strings.HasSuffix(got.Error, "ValueError: the feed is empty") ||
		!strings.Contains(got.ErrorDetails, "Traceback") {
		t.Fatalf("stderr: the last line is the text, the rest the details: %+v", got)
	}
	e.SaveScript("work", "scripts/main.py", "print('loading the feed')\n")
	got = e.Fault(e.WaitState(e.Start("work", "run", nil), "error").ID)
	if got.ErrorType != "result.invalid" || got.Whose != "module" {
		t.Fatalf("a script that prints a line, not JSON, fails as the module's: %+v", got)
	}
}

// TestTheSameFailuresAreOneRowOfTheCockpit: failures of a command with the same type and text are one row with how
// many there were; closing it closes them all; a retry follows the failure and takes it off while it runs.
func TestTheSameFailuresAreOneRowOfTheCockpit(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("work", harness.ScriptManifest("run", "", ""))
	e.SaveScript("work", "scripts/main.py", `import json, sys
inp = json.load(sys.stdin)
if inp.get("n") == 0:
    print(json.dumps({}))
    sys.exit(0)
print(json.dumps({"error": "the site is down", "type": "network"}))
sys.exit(1)
`)
	var ids []int64
	for i := 1; i <= 3; i++ {
		ids = append(ids, e.WaitState(e.Start("work", "run", map[string]any{"n": i}), "error").ID)
	}
	rows := e.FailedRuns()
	if len(rows) != 1 || rows[0].ID != ids[2] || rows[0].Repeats != 3 || rows[0].ErrorType != "network" || rows[0].Whose != "environment" {
		t.Fatalf("three of the same failure are one row, the newest, counted: %+v", rows)
	}
	var items []harness.ListItem
	_ = json.Unmarshal(e.Screen("", "cockpit", nil)["b2"]["items"], &items)
	items = slices.DeleteFunc(items, func(it harness.ListItem) bool { return !strings.Contains(it.Meta, "site is down") })
	if len(items) != 1 || !strings.Contains(items[0].Meta, "×3") || !strings.Contains(items[0].Details, "network") {
		t.Fatalf("the cockpit line says how many, its details the type: %+v", items)
	}
	page, _ := json.Marshal(e.Screen("", "process", map[string]any{"record": ids[2]}))
	if !strings.Contains(string(page), "network · environment") || !strings.Contains(string(page), "Failure type") {
		t.Fatalf("the page of the process says the type and whose it is: %s", page)
	}
	e.Must("deck.process_dismiss", map[string]any{"process": ids[2]}, nil)
	if rows := e.FailedRuns(); len(rows) != 0 {
		t.Fatalf("closing the row closes the failures it stands for: %+v", rows)
	}
	failed := e.WaitState(e.Start("work", "run", map[string]any{"n": 7}), "error").ID
	var again struct{ Process int64 }
	e.Must("deck.process_retry", map[string]any{"process": failed}, &again)
	e.WaitState(again.Process, "error")
	if got := e.Fault(again.Process); got.Follows != failed {
		t.Fatalf("a retry follows the failure it repeats: %+v", got)
	}
	if got := e.Fault(failed); len(got.FollowedBy) != 1 || got.FollowedBy[0] != again.Process {
		t.Fatalf("the failure names the run that repeated it: %+v", got)
	}
	if rows := e.FailedRuns(); len(rows) != 1 || rows[0].ID != again.Process {
		t.Fatalf("only the end of the chain is in the cockpit: %+v", rows)
	}
}

// TestTheSameNoteOfDeckIsToldOnceWithACount: deck's same unread note in a project is one note with how many times it
// came; once read, the next one is a new note.
func TestTheSameNoteOfDeckIsToldOnceWithACount(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("work", harness.ScriptManifest("run", `{"type": "object", "required": ["ok"]}`, ""))
	e.SaveScript("work", "scripts/main.py", "print('{}')\n")
	for range 2 {
		e.WaitState(e.Start("work", "run", nil), "done")
	}
	var notes []struct {
		ID      int64
		Text    string
		Repeats int64
	}
	told := func(n int, repeats int64) bool {
		e.Must("deck.feed_list", map[string]any{"kind": "note"}, &notes)
		return len(notes) == n && notes[0].Repeats == repeats
	}
	e.Until("one note counted twice", func() bool { return told(1, 2) })
	if !strings.Contains(notes[0].Text, "does not match its schema") {
		t.Fatalf("the same note twice is one note counted twice: %+v", notes)
	}
	e.Must("deck.feed_mark_read", map[string]any{"entry": notes[0].ID}, nil)
	e.WaitState(e.Start("work", "run", nil), "done")
	e.Until("a new note after the read one", func() bool { return told(2, 1) })
}

// TestTheMasterGetsTheTypeOfAFailure: on_error gets the type of the failure and whose it is; a failure of a model
// does not go to the master.
func TestTheMasterGetsTheTypeOfAFailure(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "work" {
			return provider.Fail(fault.ModelUnavailable, "API Error: 529 overloaded")
		}
		return harness.MasterScript(ctx, c)
	}), harness.WithClock())

	e.MasterAwake()
	p := e.Project("work", harness.PlainManifest)
	failed := e.WaitState(e.Run(p, "work"), "error")
	if got := e.Fault(failed.ID); got.ErrorType != "model.unavailable" || got.Whose != "model" {
		t.Fatalf("a model that does not answer is the model's failure: %+v", got)
	}
	e.App.Runner.Wait()
	if n := len(e.RunsOf("master", "on_error")); n != 0 {
		t.Fatalf("a failure of a model does not go to the master: %d", n)
	}
	e.Project("other", harness.ScriptManifest("run", "", ""))
	e.SaveScript("other", "scripts/main.py", "import sys\nprint('no', file=sys.stderr)\nsys.exit(3)\n")
	for range 3 {
		e.WaitState(e.Start("other", "run", nil), "error")
	}
	e.Until("on_error", func() bool { return len(e.RunsOf("master", "on_error")) == 1 })
	got, _ := e.App.Store.Q.GetProcess(context.Background(), e.RunsOf("master", "on_error")[0].ID)
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
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(_ int, body map[string]any) (int, string, string) {
			if body["model"] == "m2" {
				return 500, "application/json", `{"error": {"message": "overloaded"}}`
			}
			return 429, "application/json", `{"error": {"message": "rate limited"}}`
		},
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "cloud", "api": "openai", "url": api.URL + "/v1", "key": "sk-cloud"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "cloud", "model": "m1", "kind": "text", "name": "m1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "cloud", "model": "m2", "kind": "text", "name": "m2"}, nil)
	e.Project("calls", harness.ScriptManifest("call", "", `, "rights": [{"right": "money", "what": ["cloud"], "why": "calls models"}]`))
	e.SaveScript("calls", "scripts/main.py", callScript)
	for _, c := range []struct{ key, body, named, use, want, says string }{
		{"commands/deck.record_get", `{"record": 987654}`, "", "", "input.invalid", "deck refused its calls: deck.record_get: 404"},
		{"commands/deck.record_get", `{"record": 987654}`, "script.failed", "", "input.invalid", "deck.record_get: 404"},
		{"commands/deck.settings_list", `{}`, "", "", "rights.denied", "deck.settings_list: 403"},
		{"commands/deck.record_get", `{"record": 987654}`, "network", "", "network", "deck.record_get: 404"},
		{"commands/deck.record_get", `{"record": 987654}`, "", "handled", "script.failed", "deck.record_get: 404"},
		{"models/cloud/chat/completions", `{"model": "m1", "messages": [{"role": "user", "content": "hi"}]}`, "", "", "model.limit", "the model proxy cloud: 429"},
		{"models/cloud/chat/completions", `{"model": "m2", "messages": [{"role": "user", "content": "hi"}]}`, "", "", "model.unavailable", "the model proxy cloud: 502"},
	} {
		got := e.WaitState(e.Start("calls", "call", map[string]any{"key": c.key, "other": c.body, "type": c.named, "use": c.use}), "done", "error")
		f := e.Fault(got.ID)
		if f.ErrorType != c.want || !strings.Contains(f.Error, c.says) {
			t.Fatalf("%s %q: want %s with %q, got %s: %s", c.key, c.named, c.want, c.says, f.ErrorType, f.Error)
		}
	}
}

// TestTheHelpersOfTheSkillRunAsScripts: module-builder's helpers run as scripts in the sandbox: deck's answer is the
// details of a failure deck types, a named type stays, runs side by side add to one file, no .tmp left.
func TestTheHelpersOfTheSkillRunAsScripts(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	for _, c := range []struct{ runtime, file string }{{"python", "deck_helper.py"}, {"node", "deck_helper.mjs"}} {
		t.Run(c.runtime, func(t *testing.T) {
			if _, err := sandbox.Find(map[string]string{"python": "python3", "node": "node"}[c.runtime]); err != nil {
				t.Skip(c.runtime + " is not installed")
			}
			var doc struct{ Text string }
			e.Must("deck.document_get", map[string]any{"name": "skills/module-builder/helpers/" + c.file}, &doc)
			name := "h" + c.runtime
			script := "scripts/main" + filepath.Ext(c.file)
			e.Project(name, harness.Manifest(fmt.Sprintf(`{"commands": [{"name": "step", "title": "Step", "description": "Adds a value.",
    "input": {"type": "object", "properties": {"record": {"type": "integer"}, "value": {"type": "string"}}},
    "result": {"type": "object"}, "executor": {"type": "script", "script": "main"}}],
  "scripts": [{"name": "main", "file": %q, "runtime": %q}]}`, script, c.runtime)))
			e.SaveScript(name, script, doc.Text)

			f := e.Fault(e.WaitState(e.Start(name, "step", map[string]any{"record": 987654}), "error").ID)
			if f.ErrorType != "input.invalid" || !strings.Contains(f.Error, "deck.record_get: deck answered 404") ||
				!strings.Contains(f.ErrorDetails, "987654") {
				t.Fatalf("deck's answer is the details and deck types the failure: %+v", f)
			}
			f = e.Fault(e.WaitState(e.Start(name, "step", map[string]any{}), "error").ID)
			if f.ErrorType != "input.invalid" || !strings.Contains(f.Error, "no value to add") {
				t.Fatalf("the failure the script names: %+v", f)
			}
			var ids []int64
			for _, v := range []string{"a", "b", "c", "d"} {
				ids = append(ids, e.Start(name, "step", map[string]any{"value": v}))
			}
			for _, id := range ids {
				if p := e.WaitState(id, "done", "error"); p.State != "done" {
					t.Fatalf("a run side by side: %+v", e.Fault(id))
				}
			}
			last := e.WaitState(e.Start(name, "step", map[string]any{"value": "e"}), "done", "error")
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

// TestNotesOfOneCauseOfADayAreOneNote: notes of modules naming one cause, and deck's own about the same model, are
// one unread note with each module's part; another cause or another day is a note of its own.
func TestNotesOfOneCauseOfADayAreOneNote(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	for _, name := range []string{"films", "coin"} {
		e.Project(name, harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil))
	}
	e.Must("deck.owner_notify", map[string]any{"project": "films", "text": "Groq does not answer; move films to nemotron", "cause": "provider:groq"}, nil)
	e.Must("deck.owner_notify", map[string]any{"project": "coin", "text": "Groq refuses with 413; move coin to local-qwen", "cause": "provider:groq"}, nil)
	e.Must("deck.owner_notify", map[string]any{"project": "coin", "text": "The estimate is ready"}, nil)
	var notes []struct {
		Text     string
		Cause    string
		Projects []string
		Parts    []struct{ Project, Text string }
	}
	e.Must("deck.feed_list", map[string]any{"kind": "note", "unread": true}, &notes)
	if len(notes) != 2 {
		t.Fatalf("two notes of one cause are one: %+v", notes)
	}
	i := slices.IndexFunc(notes, func(n struct {
		Text     string
		Cause    string
		Projects []string
		Parts    []struct{ Project, Text string }
	}) bool {
		return n.Cause == "provider:groq"
	})
	if i < 0 || !slices.Equal(notes[i].Projects, []string{"films", "coin"}) || len(notes[i].Parts) != 2 || !strings.Contains(notes[i].Parts[1].Text, "local-qwen") {
		t.Fatalf("the note of the cause holds each module's part: %+v", notes)
	}
	e.Must("deck.owner_notify", map[string]any{"project": "coin", "text": "Groq refuses with 413; move coin to local-qwen", "cause": "provider:groq"}, nil)
	e.Must("deck.feed_list", map[string]any{"kind": "note", "unread": true}, &notes)
	if i := slices.IndexFunc(notes, func(n struct {
		Text     string
		Cause    string
		Projects []string
		Parts    []struct{ Project, Text string }
	}) bool {
		return n.Cause == "provider:groq"
	}); len(notes) != 2 || len(notes[i].Parts) != 2 {
		t.Fatalf("the same note again adds no part: %+v", notes)
	}
	e.Clock.Add(24 * time.Hour)
	e.Must("deck.owner_notify", map[string]any{"project": "films", "text": "Groq does not answer again", "cause": "provider:groq"}, nil)
	e.Must("deck.feed_list", map[string]any{"kind": "note", "unread": true}, &notes)
	if len(notes) != 3 {
		t.Fatalf("a note of the cause on another day is its own: %+v", notes)
	}
}

// TestABaseWithTheTriggersOfTheFeedJoinsItsNotesInDeck: a base a deck before left, whose triggers joined notes, loses
// them and keeps its notes; a note of the same cause still joins its unread one, deck's same note still counts.
func TestABaseWithTheTriggersOfTheFeedJoinsItsNotesInDeck(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	for _, name := range []string{"films", "coin"} {
		e.Project(name, harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil))
	}
	e.Project("work", harness.ScriptManifest("run", `{"type": "object", "required": ["ok"]}`, ""))
	e.SaveScript("work", "scripts/main.py", "print('{}')\n")
	var first struct{ Notification int64 }
	e.Must("deck.owner_notify", map[string]any{"project": "films", "text": "Groq does not answer; move films to nemotron", "cause": "provider:groq"}, &first)
	// A base an older deck left, before the notes joined in deck.
	e.SQL(`DELETE FROM goose_db_version WHERE version_id >= 75`)
	// Nor had its topics a language.
	e.SQL(`ALTER TABLE chat_topic DROP COLUMN lang; ALTER TABLE process RENAME COLUMN wait_until_ms TO pause_until_ms; ALTER TABLE process DROP COLUMN wait_reason; ALTER TABLE process DROP COLUMN wait_on; ALTER TABLE process DROP COLUMN wait_silence; DROP TABLE right_grant; ALTER TABLE project ADD COLUMN granted_revision INTEGER; ALTER TABLE mcp_tool ADD COLUMN approved TEXT NOT NULL DEFAULT ''; ALTER TABLE tool_call RENAME COLUMN need TO class; ALTER TABLE ask DROP COLUMN shown_at`)
	// Its trigger joined deck's same unread note.
	e.SQL(`CREATE TRIGGER feed_repeat BEFORE INSERT ON feed
WHEN NEW.author = 'deck' AND NEW.kind = 'note' AND NEW.for_master = 0 AND EXISTS (SELECT 1 FROM feed f
    WHERE f.project_id = NEW.project_id AND f.author = 'deck' AND f.kind = 'note' AND f.for_master = 0
      AND f.read_at IS NULL AND f.text = NEW.text)
BEGIN
    UPDATE feed SET repeats = repeats + 1, created_at = NEW.created_at
    WHERE project_id = NEW.project_id AND author = 'deck' AND kind = 'note' AND for_master = 0 AND read_at IS NULL
      AND text = NEW.text;
    SELECT RAISE(IGNORE);
END`)
	// Its trigger joined a note of a cause to the unread one of the day.
	e.SQL(`CREATE TRIGGER feed_cause BEFORE INSERT ON feed
WHEN NEW.cause != '' AND NEW.kind = 'note' AND NEW.for_master = 0 AND NEW.screen = '' AND NEW.actions IN ('[]', 'null') AND EXISTS (SELECT 1 FROM feed f
    WHERE f.workspace_id = NEW.workspace_id AND f.cause = NEW.cause AND f.kind = 'note' AND f.for_master = 0
      AND f.read_at IS NULL AND substr(f.created_at, 1, 10) = substr(NEW.created_at, 1, 10))
BEGIN
    UPDATE feed SET repeats = repeats + 1, created_at = NEW.created_at,
        parts = CASE WHEN text = NEW.text AND project_id = NEW.project_id OR EXISTS (SELECT 1 FROM json_each(parts) j
            WHERE json_extract(j.value, '$.text') = NEW.text AND json_extract(j.value, '$.project_id') = NEW.project_id)
            THEN parts ELSE json_insert(parts, '$[#]', json_object('project_id', NEW.project_id, 'author', NEW.author, 'text', NEW.text)) END
    WHERE id = (SELECT f.id FROM feed f WHERE f.workspace_id = NEW.workspace_id AND f.cause = NEW.cause AND f.kind = 'note'
        AND f.for_master = 0 AND f.read_at IS NULL AND substr(f.created_at, 1, 10) = substr(NEW.created_at, 1, 10)
        ORDER BY f.id LIMIT 1);
    SELECT RAISE(IGNORE);
END`)
	e = e.Restart()
	var triggers int
	// The triggers of a base are nowhere in deck.
	if err := e.SQLRow(`SELECT count(*) FROM sqlite_master WHERE type = 'trigger' AND tbl_name = 'feed'`).Scan(&triggers); err != nil || triggers != 0 {
		t.Fatalf("the triggers of the feed are gone: %d %v", triggers, err)
	}
	var joined struct{ Notification int64 }
	e.Must("deck.owner_notify", map[string]any{"project": "coin", "text": "Groq refuses with 413; move coin to local-qwen", "cause": "provider:groq"}, &joined)
	if joined.Notification != first.Notification {
		t.Fatalf("a note of the cause is the unread note of its cause: %d, not %d", joined.Notification, first.Notification)
	}
	var notes []struct {
		ID       int64
		Text     string
		Cause    string
		Repeats  int64
		Projects []string
		Parts    []struct{ Project, Text string }
	}
	e.Must("deck.feed_list", map[string]any{"kind": "note", "unread": true}, &notes)
	if len(notes) != 1 || notes[0].ID != first.Notification || notes[0].Repeats != 2 || !slices.Equal(notes[0].Projects, []string{"films", "coin"}) {
		t.Fatalf("the note before the update holds the part after it: %+v", notes)
	}
	for range 2 {
		e.WaitState(e.Start("work", "run", nil), "done")
	}
	e.Until("deck's same note counted twice", func() bool {
		e.Must("deck.feed_list", map[string]any{"kind": "note", "project": "work"}, &notes)
		return len(notes) == 1 && notes[0].Repeats == 2 && strings.Contains(notes[0].Text, "does not match its schema")
	})
}
