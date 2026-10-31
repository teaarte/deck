package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"deck2/internal/provider"
)

const trialScript = pythonLib + `
st, out = deck("deck.records_upsert", {"collection": "items", "records": json.dumps({"name": "a"})})
internal = st == 200
st, out = deck("tried.publish", {})
print(json.dumps({"internal": internal, "external": [st, out]}))
`

func TestTrialScriptWritesInsideDeckAndIsRefusedOutside(t *testing.T) {
	t.Parallel()
	e := New(t)
	e.Project("tried", scriptManifest("run", "", ""))
	e.saveScript("tried", "scripts/main.py", trialScript)
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
	e := New(t, WithAgent(func(ctx context.Context, c *Call) provider.Result {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))

	e.Project("keys", testManifest([]string{agentCommand("work", "")}, nil))
	var real, again struct{ Process int64 }
	e.Must("keys.work", map[string]any{"retry_key": "k"}, &real)
	e.Refused("deck.command_try", map[string]any{"command": "keys.work"}, 422, "draft copy")
	e.Refused("deck.command_try", map[string]any{"live": true, "command": "keys.work", "input": `{"retry_key": "k"}`}, 409,
		fmt.Sprintf("running now (process %d)", real.Process))
	e.Must("keys.work", map[string]any{"retry_key": "k"}, &again)
	if again.Process != real.Process || e.trial(real.Process) {
		t.Fatalf("a real run with the same key is the one going on: real %d, again %d", real.Process, again.Process)
	}
	close(release)
	e.WaitState(real.Process, "done")
	var try struct{ Process int64 }
	e.Must("deck.command_try", map[string]any{"live": true, "command": "keys.work"}, &try)
	if e.WaitState(try.Process, "done"); !e.trial(try.Process) {
		t.Fatalf("once the real run is over the trial runs: %d", try.Process)
	}
}

func TestScriptRunsOnlyFromAScriptDocument(t *testing.T) {
	t.Parallel()
	e := New(t)
	e.Project("kinds", scriptManifest("run", "", ""))
	e.Must("deck.document_save", map[string]any{"project": "kinds", "name": "scripts/main.py", "kind": "doc", "text": "print('{}')\n"}, nil)
	if code, out := e.Call("kinds.run", map[string]any{}); code != 422 || !strings.Contains(string(out), "not a script document") {
		t.Fatalf("a script file stored as another kind does not run: %d %s", code, out)
	}
}

// TestTrialOfAToolIsRefusedAndNothingLeavesDeck: a tool is the call itself, so it has no trial run; a token
// that may try runs any command of a project as a trial, whose agent calls a tool only on the owner's yes.
func TestTrialOfAToolIsRefusedAndNothingLeavesDeck(t *testing.T) {
	t.Parallel()
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{"ok": true}`) })
	var posted string
	e := New(t, WithAgent(func(ctx context.Context, c *Call) provider.Result {
		if c.N == 1 {
			posted, _ = c.Tool(ctx, "notes__post", map[string]any{"id": "a", "text": "hi"})
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}))

	e.Must("deck.secret_set", map[string]any{"name": "svc", "value": "s3cret"}, nil)
	e.Project("notes", postManifest(svc.URL, ""))
	_, tok := e.token("terminal", "deck.command_try")
	for _, call := range []func() (int, []byte){
		func() (int, []byte) {
			return e.Call("deck.command_try", map[string]any{"live": true, "command": "notes.post", "input": `{"id": "a", "text": "hi"}`})
		},
		func() (int, []byte) {
			return e.As(tok).Call("deck.command_try", map[string]any{"live": true, "command": "notes.post", "input": `{"id": "a", "text": "hi"}`})
		},
	} {
		if code, out := call(); code < 400 || len(svc.requests()) != 0 {
			t.Fatalf("a trial of a tool is refused: %d %s, %d calls", code, out, len(svc.requests()))
		}
	}
	var try struct{ Process int64 }
	code, out := e.As(tok).Call("deck.command_try", map[string]any{"live": true, "command": "notes.work"})
	if code != 200 || json.Unmarshal(out, &try) != nil {
		t.Fatalf("a token that may try runs a command of any project as a trial: %d %s", code, out)
	}
	e.WaitState(try.Process, "waiting_answer")
	var held struct{ Waiting int64 }
	if json.Unmarshal([]byte(posted), &held) != nil || held.Waiting == 0 || len(svc.requests()) != 0 {
		t.Fatalf("a tool call of a trial waits for the owner's yes on that call: %q, %d calls", posted, len(svc.requests()))
	}
	e.Must("deck.ask_answer", map[string]any{"ask": held.Waiting, "answer": "allow"}, nil)
	if seen := svc.requests(); len(seen) != 1 || seen[0].Header.Get("X-Key") != "s3cret" {
		t.Fatalf("the owner's yes makes that one call: %+v", seen)
	}
	e.WaitState(try.Process, "done")
	e.Must("deck.manifest_save", map[string]any{"project": "notes",
		"manifest": strings.Replace(postManifest(svc.URL, ""), `"class": "internal",`, `"class": "internal", "callers": ["owner"],`, 1)}, nil)
	if code, out := e.As(tok).Call("deck.command_try", map[string]any{"live": true, "command": "notes.work"}); code != 403 || !strings.Contains(string(out), "cannot be called by master") {
		t.Fatalf("a command closed to the master is not tried by a token: %d %s", code, out)
	}
}

// TestATrialOfOneCommandStartsNoChain: a trial of only this command does not start the commands that run after
// it; a trial of the chain, as before, does.
func TestATrialOfOneCommandStartsNoChain(t *testing.T) {
	t.Parallel()
	e := New(t, WithAgent(ok))
	e.Project("chain", testManifest([]string{agentCommand("c1", ""), agentCommand("c2", processField)},
		[]string{`{"command": "c2", "after": "c1"}`}))
	var try struct{ Process int64 }
	e.Must("deck.command_try", map[string]any{"live": true, "command": "chain.c1", "only": true}, &try)
	e.WaitState(try.Process, "done")
	e.Settle()
	if n := e.countOf("c2"); n != 0 {
		t.Fatalf("a trial of only c1 starts no c2: %d", n)
	}
	e.Must("deck.command_try", map[string]any{"live": true, "command": "chain.c1"}, &try)
	e.WaitState(try.Process, "done")
	e.Until("the trial of the chain", func() bool { return e.countOf("c2") == 1 })
}

// TestATrialOfAToolCommandWaitsForTheOwner: in a trial or a draft the tool of a tool command is not cleared with
// the command, so its call waits for the owner's yes before anything leaves deck.
func TestATrialOfAToolCommandWaitsForTheOwner(t *testing.T) {
	t.Parallel()
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{"ok": true}`) })
	e := New(t)
	e.Must("deck.secret_set", map[string]any{"name": "svc", "value": "s3cret"}, nil)
	send := strings.Replace(postManifest(svc.URL, ""), `"commands": [`, `"commands": [{"name": "send", "title": "Send",
    "description": "Sends the note.", "class": "external", "executor": {"type": "tool", "tool": "post"},
    "input": {"type": "object", "properties": {"id": {"type": "string"}, "text": {"type": "string"}}, "required": ["id", "text"]}},`, 1)
	e.Project("notes", send)
	_, tok := e.token("terminal", "deck.project_create", "deck.command_try")
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
		if p := e.WaitState(try.Process, "waiting_answer", "done", "error"); p.State != "waiting_answer" || len(svc.requests()) != 0 {
			t.Fatalf("the tool call of a trial waits for the owner: %s, %d calls", p.State, len(svc.requests()))
		}
	}
}
