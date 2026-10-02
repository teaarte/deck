package scenarios

import (
	"context"
	"encoding/json"
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
	e := newEnv(t, nil)
	e.createProject("tried", scriptManifest("run", "", ""))
	e.saveScript("tried", "scripts/main.py", trialScript)
	var try struct{ Process int64 }
	e.must("deck.command_try", map[string]any{"command": "tried.run", "input": "{}"}, &try)
	p := e.waitScript(try.Process, "done")
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
	e.must("deck.records_list", map[string]any{"project": "tried", "collection": "items"}, &recs)
	if len(recs) != 1 {
		t.Fatalf("internal writes of a trial stay: %v", recs)
	}
	if code, out := e.call("deck.command_try", map[string]any{"command": "deck.projects_list"}); code != 422 {
		t.Fatalf("only project commands have trials: %d %s", code, out)
	}
}

func TestTrialAndRealRunsDoNotShareRetryKeys(t *testing.T) {
	release := make(chan struct{})
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	defer close(release)
	e.createProject("keys", testManifest([]string{agentCommand("work", "")}, nil))
	var real, again, try struct{ Process int64 }
	e.must("keys.work", map[string]any{"retry_key": "k"}, &real)
	e.must("deck.command_try", map[string]any{"command": "keys.work", "input": `{"retry_key": "k"}`}, &try)
	e.must("keys.work", map[string]any{"retry_key": "k"}, &again)
	if try.Process == real.Process || again.Process != real.Process || !e.trial(try.Process) || e.trial(real.Process) {
		t.Fatalf("a trial never stands in for a real run with the same key, nor the other way: real %d, again %d, trial %d", real.Process, again.Process, try.Process)
	}
}

func TestScriptRunsOnlyFromAScriptDocument(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("kinds", scriptManifest("run", "", ""))
	e.must("deck.document_save", map[string]any{"project": "kinds", "name": "scripts/main.py", "kind": "doc", "text": "print('{}')\n"}, nil)
	if code, out := e.call("kinds.run", map[string]any{}); code != 422 || !strings.Contains(string(out), "not a script document") {
		t.Fatalf("a script file stored as another kind does not run: %d %s", code, out)
	}
}

// TestTrialOfAToolIsRefusedAndNothingLeavesDeck: a tool is the call itself, so it has no trial run; a token
// that may try runs any command of a project as a trial, whose agent has no external tool.
func TestTrialOfAToolIsRefusedAndNothingLeavesDeck(t *testing.T) {
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{"ok": true}`) })
	var posted string
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		posted, _ = c.Tool(ctx, "notes__post", map[string]any{"id": "a", "text": "hi"})
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	e.must("deck.secret_set", map[string]any{"name": "svc", "value": "s3cret"}, nil)
	e.createProject("notes", postManifest(svc.URL, ""))
	_, tok := e.token("terminal", "deck.command_try")
	for _, call := range []func() (int, []byte){
		func() (int, []byte) {
			return e.call("deck.command_try", map[string]any{"command": "notes.post", "input": `{"id": "a", "text": "hi"}`})
		},
		func() (int, []byte) {
			return e.callWith(tok, "deck.command_try", map[string]any{"command": "notes.post", "input": `{"id": "a", "text": "hi"}`})
		},
	} {
		if code, out := call(); code < 400 || len(svc.requests()) != 0 {
			t.Fatalf("a trial of a tool is refused: %d %s, %d calls", code, out, len(svc.requests()))
		}
	}
	var try struct{ Process int64 }
	code, out := e.callWith(tok, "deck.command_try", map[string]any{"command": "notes.work"})
	if code != 200 || json.Unmarshal(out, &try) != nil {
		t.Fatalf("a token that may try runs a command of any project as a trial: %d %s", code, out)
	}
	e.waitState(try.Process, "done")
	if !strings.Contains(posted, "unknown tool") || len(svc.requests()) != 0 {
		t.Fatalf("an agent of a trial has no external tool to call: %q, %d calls", posted, len(svc.requests()))
	}
	e.must("deck.manifest_save", map[string]any{"project": "notes",
		"manifest": strings.Replace(postManifest(svc.URL, ""), `"class": "internal",`, `"class": "internal", "callers": ["owner"],`, 1)}, nil)
	if code, out := e.callWith(tok, "deck.command_try", map[string]any{"command": "notes.work"}); code != 403 || !strings.Contains(string(out), "cannot be called by master") {
		t.Fatalf("a command closed to the master is not tried by a token: %d %s", code, out)
	}
}
