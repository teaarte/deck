package owner

import (
	"deck2/tests/harness"
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// pressAsks are the calls a run the owner started waits to make, as the button reads them.
func pressAsks(e *harness.Env, process int64) []harness.PressAsk {
	e.T.Helper()
	var out struct{ Asks []harness.PressAsk }
	e.Must("deck.process_asks", map[string]any{"process": process}, &out)
	return out.Asks
}

// pressRun starts the script of project as a button of the owner does, with the deck call of method and form.
func pressRun(e *harness.Env, project, method string, form map[string]any) int64 {
	e.T.Helper()
	raw, _ := json.Marshal(form)
	return e.Start(project, "main", map[string]any{"method": method, "form": string(raw)})
}

// TestTheOwnersPressAnswersTheCallItsRunWaitsFor: a call a run of the owner's button waits to make comes back to
// the button with what it was and what it becomes; yes there applies it and nothing waits in the cockpit.
func TestTheOwnersPressAnswersTheCallItsRunWaitsFor(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "kind": "text", "call_usd": 0.01}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "gemma", "kind": "text", "call_usd": 0.02}, nil)
	e.Project("shorts", harness.PlainManifest)
	e.Project("picker", harness.ScriptManifest("main", "", ""))
	e.SaveScript("picker", "scripts/main.py", harness.PressScript)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/gemma"}}, nil)

	run := pressRun(e, "picker", "deck.assignment_set", map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/qwen"}})
	asks := pressAsks(e, run)
	if len(asks) != 1 || asks[0].Kind != "call" || asks[0].What != "Assign models" || asks[0].Where != "text of shorts: openrouter/gemma → openrouter/qwen" {
		t.Fatalf("the button gets the call its run waits for, before and after: %+v", asks)
	}
	if got := pressAsks(e, e.Start("picker", "main", map[string]any{"n": asks[0].ID})); len(got) != 1 || got[0].ID != asks[0].ID {
		t.Fatalf("a run that names the ask it waits for gives the same ask to answer, not a copy: %+v", got)
	}
	if code, out := e.Press(asks[0].Allow); code != 200 {
		t.Fatalf("the button that allows comes with the ask: %d %s", code, out)
	}
	if got := e.Assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/qwen"}) {
		t.Fatalf("yes at the button applies the call: %v", got)
	}
	var left []struct{ Kind string }
	e.Must("deck.asks_list", nil, &left)
	if len(left) != 0 {
		t.Fatalf("nothing waits in the cockpit after the yes at the button: %+v", left)
	}

	if got := pressAsks(e, pressRun(e, "picker", "deck.models_list", map[string]any{})); len(got) != 0 {
		t.Fatalf("a run that waits for nothing gives the button nothing once it ends: %+v", got)
	}
	add := pressRun(e, "picker", "deck.model_set", map[string]any{"connection": "openrouter", "model": "llama", "kind": "text"})
	if got := pressAsks(e, add); len(got) != 1 || got[0].Where != "openrouter/llama → kind text" {
		t.Fatalf("a model to add says what it sets: %+v", got)
	}
	if code, out := e.Press(pressAsks(e, add)[0].Deny); code != 200 {
		t.Fatalf("the button that denies comes with the ask: %d %s", code, out)
	}
	two := e.Start("picker", "main", map[string]any{"method": "many", "form": string(harness.MustJSON([]map[string]any{
		{"method": "deck.model_set", "form": map[string]any{"connection": "openrouter", "model": "mistral", "kind": "text", "free": true}},
		{"method": "deck.assignment_set", "form": map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/gemma"}}}}))})
	twoAsks := pressAsks(e, two)
	if len(twoAsks) != 2 || twoAsks[0].Where != "openrouter/mistral → free true, kind text" {
		t.Fatalf("a run that waits for two calls gives the button both: %+v", twoAsks)
	}

	var try struct{ Process int64 }
	e.Must("deck.command_try", map[string]any{"command": "picker.main", "live": true, "input": string(harness.MustJSON(map[string]any{
		"method": "deck.model_set", "form": string(harness.MustJSON(map[string]any{"connection": "openrouter", "model": "phi", "kind": "text"}))}))}, &try)
	if p := e.WaitState(try.Process, "done", "error"); !strings.Contains(string(p.Result), "trial") || len(pressAsks(e, try.Process)) != 0 {
		t.Fatalf("a trial does not propose a model: %s", p.Result)
	}

	e.Project("other", harness.ScriptManifest("main", "", ""))
	e.SaveScript("other", "scripts/main.py", harness.PressScript)
	theirs := twoAsks[0].ID
	if got := pressAsks(e, e.Start("other", "main", map[string]any{"n": theirs})); len(got) != 0 {
		t.Fatalf("a run names no ask of another project to the button: %+v", got)
	}
	if code, out := e.Call("deck.process_asks", map[string]any{"process": 99999}); code != 404 || !strings.Contains(string(out), "no process") {
		t.Fatalf("no asks of a process that is not there: %d %s", code, out)
	}
}
