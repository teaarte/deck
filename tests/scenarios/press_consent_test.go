package scenarios

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// applyScript applies the proposal of its id, or with none every proposal, by deck.assignment_set; with relay it
// starts apply itself, as another caller, with the allows it was given.
const applyScript = `import json, os, sys, urllib.request, urllib.error
inp = json.load(sys.stdin)
def call(m, f):
    req = urllib.request.Request(os.environ["DECK_URL"] + "/commands/" + m, data=json.dumps(f).encode(),
        headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
    try:
        body = urllib.request.urlopen(req, timeout=30).read().decode()
        return json.loads(body) if body.strip() else None
    except urllib.error.HTTPError as e:
        return {"status": e.code, "body": e.read().decode()}
if inp.get("relay"):
    print(json.dumps(call("picker.apply", {"id": inp["id"], "allows": inp["relay"]})))
    sys.exit(0)
ids = [inp["id"]] if inp.get("id") else [r["id"] for r in call("deck.records_list", {"collection": "proposals"})]
out = []
for i in ids:
    d = call("deck.record_get", {"record": i})["data"]
    out.append(call("deck.assignment_set", {"role": "text", "project": d["project"], "models": d["models"].split(","), "command": d.get("command", "")}))
print(json.dumps({"calls": out}))
`

// pickerManifest has proposals of models with an Apply button whose press allows the assignment its row shows, and
// a daily run of apply over all of them.
const pickerManifest = `{"version": 13,
  "collections": [{"name": "proposals", "description": "A proposal of models.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}, "project": {"type": "string"}, "models": {"type": "string"},
      "command": {"type": "string"}}, "required": ["name"]}}],
  "commands": [
    {"name": "apply", "title": "Apply", "description": "Applies a proposal.", "class": "internal",
     "input": {"type": "object", "properties": {"id": {"type": "integer"}, "relay": {"type": "string"}}},
     "result": {"type": "object"}, "executor": {"type": "script", "script": "apply"}}],
  "scripts": [{"name": "apply", "file": "scripts/apply.py", "runtime": "python"}],
  "triggers": [{"command": "apply", "cron": "0 9 * * *"}],
  "screens": [{"name": "main", "title": "Proposals", "blocks": [{"type": "table", "title": "Proposals", "source": {"collection": "proposals"},
    "columns": [{"label": "Models", "bind": "models"}],
    "actions": [{"label": "Apply", "command": "apply", "params": {"id": "=_id"},
      "allows": [{"command": "deck.assignment_set", "params": {"role": "text", "project": "=project", "command": "=command ?? ''", "models": "=split(models, ',')"}}]}]}]}],
  "menu": ["main"]}`

type feedChange struct {
	ID      int64  `json:"id"`
	Kind    string `json:"kind"`
	Author  string `json:"author"`
	Text    string `json:"text"`
	CanUndo bool   `json:"can_undo"`
}

// TestTheOwnersPressIsTheYesToTheCallItsButtonNames: Apply pressed makes the assignment its row shows at once, listed
// with its undo; the same apply by the schedule, a call the button did not name and a run of another caller still ask.
func TestTheOwnersPressIsTheYesToTheCallItsButtonNames(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	for _, m := range []string{"qwen", "gemma", "llama"} {
		e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": m, "kind": "text", "call_usd": 0.01}, nil)
	}
	e.createProject("shorts", plainManifest)
	e.createProject("picker", pickerManifest)
	e.saveScript("picker", "scripts/apply.py", applyScript)
	e.must("deck.assignment_set", map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/gemma"}}, nil)
	e.must("deck.records_upsert", map[string]any{"project": "picker", "collection": "proposals",
		"records": jsonl(map[string]any{"name": "a", "project": "shorts", "models": "openrouter/qwen,openrouter/gemma"})}, nil)

	apply := e.rowAction("picker", "main", "picker.apply", nil)
	code, out := e.press(apply)
	if code != 200 {
		t.Fatalf("the press starts apply: %d %s", code, out)
	}
	var run struct{ Process int64 }
	_ = json.Unmarshal(out, &run)
	if p := e.waitScript(run.Process, "done", "error"); p.State != "done" || strings.Contains(string(p.Result), "waiting") {
		t.Fatalf("the call the button names is made at once: %+v", p)
	}
	if got := e.assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/qwen", "openrouter/gemma"}) {
		t.Fatalf("the press applies the models its row shows: %v", got)
	}
	var asks []askView
	e.must("deck.asks_list", nil, &asks)
	if len(asks) != 0 {
		t.Fatalf("nothing waits for a second yes: %+v", asks)
	}
	var feed []feedChange
	e.must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 1 || !feed[0].CanUndo || !strings.Contains(feed[0].Text, "deck.assignment_set by your press: text of shorts: openrouter/gemma → openrouter/qwen, openrouter/gemma") {
		t.Fatalf("the change is listed with its undo: %+v", feed)
	}
	var ru []feedChange
	e.must("deck.feed_list", map[string]any{"kind": "change", "lang": "ru"}, &ru)
	if len(ru) != 1 || !strings.Contains(ru[0].Text, "по вашему нажатию") {
		t.Fatalf("the change is told in the owner's words: %+v", ru)
	}
	e.must("deck.feed_undo", map[string]any{"entry": feed[0].ID}, nil)
	if got := e.assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/gemma"}) {
		t.Fatalf("the undo sets the models back: %v", got)
	}

	// The same apply by the schedule asks, as before.
	e.Clock.Add(time.Hour + time.Minute)
	e.tick()
	e.waitFor("the scheduled apply to ask", func() bool {
		var as []askView
		e.must("deck.asks_list", nil, &as)
		return len(as) == 1
	})
	if got := e.assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/gemma"}) {
		t.Fatalf("the schedule changes nothing without the owner: %v", got)
	}
	e.must("deck.asks_list", nil, &asks)
	e.answer(asks[0].ID, "deny")

	// A row whose run calls with other params than the button named still asks.
	e.must("deck.records_upsert", map[string]any{"project": "picker", "collection": "proposals",
		"records": jsonl(map[string]any{"name": "a", "project": "shorts", "models": "openrouter/llama"})}, nil)
	other := e.rowAction("picker", "main", "picker.apply", nil)
	params := other["params"].(map[string]any)
	params["allows"] = apply["params"].(map[string]any)["allows"]
	_, out = e.press(other)
	_ = json.Unmarshal(out, &run)
	if p := e.waitScript(run.Process, "done", "error"); !strings.Contains(string(p.Result), "waiting") {
		t.Fatalf("a call the press did not name waits for the owner: %+v", p)
	}
	e.must("deck.asks_list", nil, &asks)
	e.answer(asks[0].ID, "deny")

	// The yes is the owner's alone and the run's alone: a run another caller starts with the same allows asks.
	_, out = e.call("picker.apply", map[string]any{"id": apply["params"].(map[string]any)["id"], "relay": other["params"].(map[string]any)["allows"]})
	_ = json.Unmarshal(out, &run)
	relay := e.waitScript(run.Process, "done", "error")
	var child struct{ Process int64 }
	_ = json.Unmarshal(relay.Result, &child)
	if p := e.waitScript(child.Process, "done", "error"); child.Process == 0 || !strings.Contains(string(p.Result), "waiting") {
		t.Fatalf("a run another caller started is no press of the owner: %+v %+v", relay, p)
	}
	if got := e.assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/gemma"}) {
		t.Fatalf("nothing but the press changed the models: %v", got)
	}
}

// TestAPressStandsForNoMoneyAndNoFrame: a button may not name a call that sets prices, a custom block's frame presses
// without the owner so its buttons name none, and allows comes from the property alone, not from a param.
func TestAPressStandsForNoMoneyAndNoFrame(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	form := `{"connection": "openrouter", "model": "llama", "kind": "text", "free": true}`
	params, _ := json.Marshal(map[string]string{"method": "deck.model_set", "form": form})
	e.createProject("picker", scriptManifest("main", "", `, "screens": [{"name": "main", "title": "Models", "blocks": [
    {"type": "actions", "title": "Model", "actions": [{"label": "Add", "command": "main", "params": `+string(params)+`,
      "allows": [{"command": "deck.model_set", "params": {"connection": "openrouter", "model": "llama", "kind": "text", "free": "true"}}]}]}]}], "menu": ["main"]`))
	e.saveScript("picker", "scripts/main.py", pressScript)
	add := e.rowAction("picker", "main", "picker.main", nil)
	if _, ok := add["params"].(map[string]any)["allows"]; !ok {
		t.Fatalf("the button names its call: %+v", add)
	}
	_, out := e.press(add)
	var run struct{ Process int64 }
	_ = json.Unmarshal(out, &run)
	if p := e.waitScript(run.Process, "done", "error"); !strings.Contains(string(p.Result), "waiting") {
		t.Fatalf("a model with its price waits for the owner's own yes, press or not: %+v", p)
	}

	frame := strings.Replace(scriptManifest("main", "", `, "screens": [{"name": "main", "title": "Models", "blocks": [
    {"type": "custom", "document": "custom/x.html", "actions": [{"label": "Add", "command": "main", "params": {"method": "x"},
      "allows": [{"command": "deck.model_set", "params": {"model": "llama"}}]}]}]}]`), `"version": 13`, `"version": 13, "title": "Frame"`, 1)
	if code, out := e.call("deck.manifest_save", map[string]any{"project": "picker", "manifest": frame}); code < 400 || !strings.Contains(string(out), "custom block") {
		t.Fatalf("a custom block names no call its press allows: %d %s", code, out)
	}
	literal := scriptManifest("main", "", `, "screens": [{"name": "main", "title": "Models", "blocks": [
    {"type": "actions", "title": "Model", "actions": [{"label": "Add", "command": "main", "params": {"allows": "[]"}}]}]}]`)
	if code, out := e.call("deck.manifest_save", map[string]any{"project": "picker", "manifest": literal}); code < 400 || !strings.Contains(string(out), `"allows"`) {
		t.Fatalf("allows is no param a button sets itself: %d %s", code, out)
	}
}
