package scenarios

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"deck2/internal/provider"
)

// deckCallScript calls the deck command of its input's method with the JSON of form and prints the status and body.
const deckCallScript = `import json, os, sys, urllib.request, urllib.error
inp = json.load(sys.stdin)
req = urllib.request.Request(os.environ["DECK_URL"] + "/commands/" + inp["method"], data=inp["form"].encode(),
    headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
try:
    r = urllib.request.urlopen(req, timeout=30)
    status, body = r.status, r.read().decode()
except urllib.error.HTTPError as e:
    status, body = e.code, e.read().decode()
print(json.dumps({"status": status, "body": body}))
`

type called struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// scriptCall runs the script command main of project with a call of method and returns what deck answered it.
func (e *Env) scriptCall(project, method string, form map[string]any) called {
	e.T.Helper()
	raw, _ := json.Marshal(form)
	p := e.waitScript(e.start(project, "main", map[string]any{"method": method, "form": string(raw)}), "done", "error")
	var c called
	if err := json.Unmarshal(p.Result, &c); err != nil {
		e.T.Fatalf("the script answers what deck said: %+v %v", p, err)
	}
	return c
}

// assigned are the models of a role of a project as the owner reads them.
func (e *Env) assigned(project, role string) []string {
	e.T.Helper()
	var m modelsOut
	e.must("deck.models_list", map[string]any{"project": project}, &m)
	for _, a := range m.Assignments {
		if a.Project == project && a.Command == "" && a.Role == role {
			return a.Models
		}
	}
	return nil
}

// callAsk is the open ask of a call of command, with where it goes as the owner reads it.
func (e *Env) callAsk(command string) (int64, string) {
	e.T.Helper()
	var as []struct {
		ID      int64  `json:"id"`
		Kind    string `json:"kind"`
		Command string `json:"command"`
		Where   string `json:"where"`
		Who     string `json:"who"`
	}
	e.must("deck.asks_list", nil, &as)
	for _, a := range as {
		if a.Kind == "call" && a.Command == command {
			return a.ID, a.Who + ": " + a.Where
		}
	}
	e.T.Fatalf("no call of %s waits for the owner: %+v", command, as)
	return 0, ""
}

// TestAModuleProposesAnAssignmentOfAnotherProject: a script of a module calls deck.assignment_set for another
// project; the call waits for the owner, who sees what it was and what it becomes; yes applies exactly it, no
// leaves it, and a yes after the assignment changed is refused. The owner's own call applies at once.
func TestAModuleProposesAnAssignmentOfAnotherProject(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "kind": "text", "call_usd": 0.01}, nil)
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "gemma", "kind": "text", "call_usd": 0.02}, nil)
	e.createProject("shorts", plainManifest)
	e.createProject("picker", scriptManifest("main", "", ""))
	e.saveScript("picker", "scripts/main.py", deckCallScript)
	e.must("deck.assignment_set", map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/gemma"}}, nil)
	if got := e.assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/gemma"}) {
		t.Fatalf("the owner's call applies at once: %v", got)
	}

	propose := map[string]any{"role": "text", "project": "shorts", "model": "openrouter/qwen"}
	if c := e.scriptCall("picker", "deck.assignment_set", propose); c.Status != 202 || !strings.Contains(c.Body, "waits for the owner") {
		t.Fatalf("the proposal waits for the owner: %+v", c)
	}
	if got := e.assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/gemma"}) {
		t.Fatalf("nothing changes before the owner's yes: %v", got)
	}
	ask, where := e.callAsk("deck.assignment_set")
	if !strings.Contains(where, "text of shorts: openrouter/gemma → openrouter/qwen, openrouter/gemma") {
		t.Fatalf("the owner sees what the assignment was and what it becomes: %s", where)
	}
	e.answer(ask, "allow")
	if got := e.assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/qwen", "openrouter/gemma"}) {
		t.Fatalf("the owner's yes applies the assignment as asked: %v", got)
	}

	only := map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/gemma"}}
	if c := e.scriptCall("picker", "deck.assignment_set", only); c.Status != 202 {
		t.Fatalf("a second proposal waits too: %+v", c)
	}
	ask, _ = e.callAsk("deck.assignment_set")
	e.answer(ask, "deny")
	if got := e.assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/qwen", "openrouter/gemma"}) {
		t.Fatalf("the owner's no leaves the assignment: %v", got)
	}

	if c := e.scriptCall("picker", "deck.assignment_set", only); c.Status != 202 {
		t.Fatalf("a third proposal waits too: %+v", c)
	}
	ask, _ = e.callAsk("deck.assignment_set")
	e.must("deck.assignment_set", map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/qwen"}}, nil)
	if code, out := e.call("deck.ask_answer", map[string]any{"ask": ask, "answer": "allow"}); code == 200 || !strings.Contains(string(out), "changed after it was asked for") {
		t.Fatalf("a yes to an assignment that changed meanwhile is refused: %d %s", code, out)
	}
	if got := e.assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/qwen"}) {
		t.Fatalf("the refused yes changes nothing: %v", got)
	}
	e.answer(ask, "deny")

	if c := e.scriptCall("picker", "deck.assignment_set", map[string]any{"role": "text", "project": "shorts", "models": []string{"nope"}}); c.Status != 404 {
		t.Fatalf("a proposal deck cannot apply is refused at once, not held: %+v", c)
	}
}

// TestNoCallerAppliesAnAssignmentPastTheOwner: a token and an agent of a module wait for the owner too, and a trial
// is refused.
func TestNoCallerAppliesAnAssignmentPastTheOwner(t *testing.T) {
	t.Parallel()
	var said string
	var bad bool
	turns := 0
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if turns++; turns > 1 {
			return provider.Result{Text: "done"}
		}
		said, bad = c.Tool(ctx, "assignment_set", map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/qwen"}})
		return provider.Result{Text: "proposed"}
	})
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "kind": "text", "call_usd": 0.01}, nil)
	shorts := e.createProject("shorts", plainManifest)
	e.createProject("picker", scriptManifest("main", "", ""))
	e.saveScript("picker", "scripts/main.py", deckCallScript)

	id := e.run(shorts, "work")
	e.waitState(id, "waiting_answer")
	if got := e.assigned("shorts", "text"); got != nil {
		t.Fatalf("an agent's call does not apply before the owner's yes: %v", got)
	}
	if bad || !strings.Contains(said, "waits for the owner") {
		t.Fatalf("the agent is told its call waits for the owner: %v %s", bad, said)
	}
	ask, _ := e.callAsk("deck.assignment_set")
	e.answer(ask, "deny")
	e.waitState(id, "done")

	_, tok := e.token("terminal", "deck.assignment_set")
	s, err := e.terminal(tok)
	if err != nil {
		t.Fatal(err)
	}
	if out, failed := use(s, "assignment_set", map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/qwen"}}); failed || !strings.Contains(out, "waits for the owner") {
		t.Fatalf("a token's call waits for the owner: %v %s", failed, out)
	}
	if got := e.assigned("shorts", "text"); got != nil {
		t.Fatalf("a token's call does not apply before the owner's yes: %v", got)
	}

	raw, _ := json.Marshal(map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/qwen"}})
	var try struct{ Process int64 }
	e.must("deck.command_try", map[string]any{"command": "picker.main", "live": true,
		"input": string(mustJSON(map[string]any{"method": "deck.assignment_set", "form": string(raw)}))}, &try)
	p := e.waitScript(try.Process, "done", "error")
	var c called
	_ = json.Unmarshal(p.Result, &c)
	if c.Status != 403 || !strings.Contains(c.Body, "trial or a draft") {
		t.Fatalf("a trial does not propose an assignment: %+v", p)
	}
	if got := e.assigned("shorts", "text"); got != nil {
		t.Fatalf("nothing applied past the owner: %v", got)
	}
}
