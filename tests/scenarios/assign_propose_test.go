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

// TestAModuleProposesAnAssignmentOfAnotherProject: a script's deck.assignment_set for another project waits for the
// owner, who sees it before and after; yes applies it, no leaves it, a yes after a change is refused.
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
	ask, who := e.callAsk("deck.assignment_set")
	if !strings.HasPrefix(who, "token #") {
		t.Fatalf("the owner sees who asks: %s", who)
	}
	e.answer(ask, "allow")
	if got := e.assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/qwen"}) {
		t.Fatalf("the owner's yes applies the token's proposal: %v", got)
	}
}

// TestAToolCommandOfAModuleDoesNotClearAnAssignment: a command of a module whose one call is deck.assignment_set,
// allowed to run without asking, still leaves the assignment to the owner's button.
func TestAToolCommandOfAModuleDoesNotClearAnAssignment(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "kind": "text", "call_usd": 0.01}, nil)
	e.createProject("shorts", plainManifest)
	e.createProject("picker", `{"version": 13,
  "commands": [{"name": "propose", "title": "Propose", "description": "Proposes an assignment.", "class": "external",
    "input": {"type": "object", "properties": {"role": {"type": "string"}, "project": {"type": "string"}, "models": {"type": "array", "items": {"type": "string"}}}, "required": ["role"]},
    "executor": {"type": "tool", "tool": "deck.assignment_set"}}],
  "allowed_external": ["propose"]}`)
	_, tok := e.token("terminal", "picker.*")
	var r struct{ Process int64 }
	if code, out := e.callWith(tok, "picker.propose", map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/qwen"}}); code != 200 || json.Unmarshal(out, &r) != nil {
		t.Fatalf("the command runs: %d %s", code, out)
	}
	e.waitAsk()
	ask, where := e.callAsk("deck.assignment_set")
	if got := e.assigned("shorts", "text"); got != nil || !strings.Contains(where, "text of shorts: deck's default → openrouter/qwen") {
		t.Fatalf("its call of deck.assignment_set waits for the owner: %v %s", got, where)
	}
	e.answer(ask, "allow")
	if got := e.assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/qwen"}) {
		t.Fatalf("the owner's yes applies it: %v", got)
	}
}

// TestAModelsReadOfAProjectOpensNoDataOfIt: reads {"project": "shorts", "models": true} reads the models of shorts and
// not its records; dropping models for a read of the project asks the owner again.
func TestAModelsReadOfAProjectOpensNoDataOfIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("shorts", scriptManifest("main", "", ""))
	module := scriptManifest("main", "", "")
	e.createProject("watcher", strings.Replace(module, `"scripts":`, `"reads": [{"project": "shorts", "models": true}], "scripts":`, 1))
	e.saveScript("watcher", "scripts/main.py", deckCallScript)
	if c := e.scriptCall("watcher", "deck.models_list", map[string]any{"project": "shorts"}); c.Status != 200 || !strings.Contains(c.Body, `"command":"publish"`) {
		t.Fatalf("the module reads the models of shorts: %+v", c)
	}
	if c := e.scriptCall("watcher", "deck.records_list", map[string]any{"project": "shorts", "collection": "items"}); c.Status != 403 {
		t.Fatalf("it reads no records of shorts: %+v", c)
	}
	_, tok := e.token("terminal", "deck.*")
	_, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "watcher",
		"manifest": strings.Replace(module, `"scripts":`, `"reads": [{"project": "shorts"}], "scripts":`, 1)})
	var saved struct {
		Waiting int64
		Widens  []string
	}
	_ = json.Unmarshal(out, &saved)
	if saved.Waiting == 0 || !slices.Equal(saved.Widens, []string{"reads project shorts"}) {
		t.Fatalf("a read of the project in place of its models waits for the owner: %s", out)
	}
}

// TestAModuleReadsTheModelsOfEveryProjectByARight: reads {"project": "*", "models": true}, granted by the owner, lets
// scripts and agents read the models of another project, never a key; an assignment still waits for the owner.
func TestAModuleReadsTheModelsOfEveryProjectByARight(t *testing.T) {
	t.Parallel()
	var heard string
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		heard, _ = c.Tool(ctx, "models_list", map[string]any{"project": "shorts"})
		return provider.Result{Text: "read"}
	})
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1", "key": "sk-or-hidden"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "kind": "text", "call_usd": 0.01}, nil)
	e.createProject("shorts", plainManifest)
	e.must("deck.assignment_set", map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/qwen"}}, nil)
	module := scriptManifest("main", "", "")
	watcher := e.createProject("watcher", module)
	e.saveScript("watcher", "scripts/main.py", deckCallScript)

	read := map[string]any{"project": "shorts"}
	if c := e.scriptCall("watcher", "deck.models_list", read); c.Status != 403 || !strings.Contains(c.Body, "does not read which models do the work of shorts") {
		t.Fatalf("without the right a module does not read the models of another project: %+v", c)
	}

	_, tok := e.token("terminal", "deck.*")
	wider := strings.Replace(module, `"scripts":`, `"reads": [{"project": "*", "models": true}], "scripts":`, 1)
	_, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "watcher", "manifest": wider})
	var saved struct {
		Waiting int64
		Widens  []string
	}
	_ = json.Unmarshal(out, &saved)
	if saved.Waiting == 0 || !slices.Equal(saved.Widens, []string{"reads which models do the work of every project"}) {
		t.Fatalf("the right waits for the owner's button, named plainly: %s", out)
	}
	if c := e.scriptCall("watcher", "deck.models_list", read); c.Status != 403 {
		t.Fatalf("the right is not in force before the owner's yes: %+v", c)
	}
	e.answer(saved.Waiting, "allow")

	c := e.scriptCall("watcher", "deck.models_list", read)
	var m struct {
		modelsOut
		Work []struct {
			Project string `json:"project"`
			Command string `json:"command"`
		} `json:"work"`
	}
	if err := json.Unmarshal([]byte(c.Body), &m); c.Status != 200 || err != nil {
		t.Fatalf("with the right a script reads the models of another project: %+v", c)
	}
	if len(m.Assignments) != 1 || m.Assignments[0].Project != "shorts" || len(m.Work) == 0 || m.Work[0].Project != "shorts" || m.Work[0].Command != "work" {
		t.Fatalf("it reads the assignments and the work of that project: %s", c.Body)
	}
	if strings.Contains(c.Body, "sk-or-hidden") || strings.Contains(c.Body, "key_secret") {
		t.Fatalf("the right gives no key: %s", c.Body)
	}

	e.waitState(e.run(watcher, "publish"), "done")
	if !strings.Contains(heard, `"project":"shorts"`) || !strings.Contains(heard, `"command":"work"`) {
		t.Fatalf("an agent of the module reads it too: %s", heard)
	}

	if c := e.scriptCall("watcher", "deck.assignment_set", map[string]any{"role": "text", "project": "shorts", "models": []string{}}); c.Status != 202 {
		t.Fatalf("the right reads only; an assignment still waits for the owner: %+v", c)
	}
	if got := e.assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/qwen"}) {
		t.Fatalf("nothing changes by the right: %v", got)
	}
	data := strings.Replace(module, `"scripts":`, `"reads": [{"project": "*", "collections": ["items"]}], "scripts":`, 1)
	e.refused("deck.manifest_save", map[string]any{"project": "watcher", "manifest": data}, 422, "names every project only for which models do their work")
}

// TestAModuleReadsTheRunsOfAnotherProjectInBriefByARight: reads {"project": "*", "runs": true}, granted by the owner,
// lets a script list the runs of another project with what ran, how it ended and on which model, never their input,
// result or log.
func TestAModuleReadsTheRunsOfAnotherProjectInBriefByARight(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`), Text: "secret result"}
	})
	plain := e.createProject("plain", testManifest([]string{agentCommand("work", `{"type": "object", "properties": {"q": {"type": "string"}}}`)}, nil))
	id := e.run(plain, "work")
	e.waitState(id, "done")
	module := scriptManifest("main", "", "")
	e.createProject("watcher", module)
	e.saveScript("watcher", "scripts/main.py", deckCallScript)
	list := map[string]any{"project": "plain"}
	if c := e.scriptCall("watcher", "deck.processes_list", list); c.Status != 403 || !strings.Contains(c.Body, `"runs": true`) {
		t.Fatalf("without the right a module does not list the runs of another project, and is told what gives it: %+v", c)
	}
	_, tok := e.token("terminal", "deck.*")
	_, out := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "watcher",
		"manifest": strings.Replace(module, `"scripts":`, `"reads": [{"project": "*", "runs": true}], "scripts":`, 1)})
	var saved struct {
		Waiting int64
		Widens  []string
	}
	_ = json.Unmarshal(out, &saved)
	if saved.Waiting == 0 || !slices.Equal(saved.Widens, []string{"reads the runs of every project"}) {
		t.Fatalf("the right waits for the owner's button, named plainly: %s", out)
	}
	e.answer(saved.Waiting, "allow")
	c := e.scriptCall("watcher", "deck.processes_list", list)
	var runs []map[string]any
	if c.Status != 200 || json.Unmarshal([]byte(c.Body), &runs) != nil || len(runs) != 1 {
		t.Fatalf("with the right the module lists the runs of plain: %+v", c)
	}
	if r := runs[0]; r["command"] != "work" || r["state"] != "done" || r["provider"] != "fake" || r["result"] != nil || r["input"] != nil ||
		r["session_id"] != nil || r["started_at"] == nil {
		t.Fatalf("a run of another project is brief: %v", r)
	}
	if c := e.scriptCall("watcher", "deck.records_list", map[string]any{"project": "plain", "collection": "items"}); c.Status != 403 {
		t.Fatalf("the runs open no data of the project: %+v", c)
	}
}
