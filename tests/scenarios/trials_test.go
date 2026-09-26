package scenarios

import (
	"context"
	"encoding/json"
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
	e.createProject("tried", scriptManifest("run", "", "", ""))
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
	e.createProject("kinds", scriptManifest("run", "", "", ""))
	e.must("deck.document_save", map[string]any{"project": "kinds", "name": "scripts/main.py", "kind": "doc", "text": "print('{}')\n"}, nil)
	if code, out := e.call("kinds.run", map[string]any{}); code != 422 || !strings.Contains(string(out), "not a script document") {
		t.Fatalf("a script file stored as another kind does not run: %d %s", code, out)
	}
}
