package owner

import (
	"deck2/tests/harness"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

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
	e := harness.New(t, harness.WithClock())
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	for _, m := range []string{"qwen", "gemma", "llama"} {
		e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": m, "kind": "text", "call_usd": 0.01}, nil)
	}
	e.Project("shorts", harness.PlainManifest)
	e.Project("picker", harness.PickerManifest)
	e.SaveScript("picker", "scripts/apply.py", harness.ApplyScript)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/gemma"}}, nil)
	e.Must("deck.records_upsert", map[string]any{"project": "picker", "collection": "proposals",
		"records": harness.Jsonl(map[string]any{"name": "a", "project": "shorts", "models": "openrouter/qwen,openrouter/gemma"})}, nil)

	apply := e.RowAction("picker", "main", "picker.apply", nil)
	code, out := e.Press(apply)
	if code != 200 {
		t.Fatalf("the press starts apply: %d %s", code, out)
	}
	var run struct{ Process int64 }
	_ = json.Unmarshal(out, &run)
	if p := e.WaitState(run.Process, "done", "error"); p.State != "done" || strings.Contains(string(p.Result), "waiting") {
		t.Fatalf("the call the button names is made at once: %+v", p)
	}
	if got := e.Assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/qwen", "openrouter/gemma"}) {
		t.Fatalf("the press applies the models its row shows: %v", got)
	}
	var asks []harness.Ask
	e.Must("deck.asks_list", nil, &asks)
	if len(asks) != 0 {
		t.Fatalf("nothing waits for a second yes: %+v", asks)
	}
	var feed []feedChange
	e.Must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 1 || !feed[0].CanUndo || !strings.Contains(feed[0].Text, "deck.assignment_set by your press: text of shorts: openrouter/gemma → openrouter/qwen, openrouter/gemma") {
		t.Fatalf("the change is listed with its undo: %+v", feed)
	}
	var ru []feedChange
	e.Must("deck.feed_list", map[string]any{"kind": "change", "lang": "ru"}, &ru)
	if len(ru) != 1 || !strings.Contains(ru[0].Text, "по вашему нажатию") {
		t.Fatalf("the change is told in the owner's words: %+v", ru)
	}
	e.Must("deck.feed_undo", map[string]any{"entry": feed[0].ID}, nil)
	if got := e.Assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/gemma"}) {
		t.Fatalf("the undo sets the models back: %v", got)
	}

	// The same apply by the schedule asks, as before.
	e.Clock.Add(time.Hour + time.Minute)
	e.Tick()
	e.Until("the scheduled apply to ask", func() bool {
		var as []harness.Ask
		e.Must("deck.asks_list", nil, &as)
		return len(as) == 1
	})
	if got := e.Assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/gemma"}) {
		t.Fatalf("the schedule changes nothing without the owner: %v", got)
	}
	e.Must("deck.asks_list", nil, &asks)
	e.Answer(asks[0].ID, "deny")

	// A row whose run calls with other params than the button named still asks.
	e.Must("deck.records_upsert", map[string]any{"project": "picker", "collection": "proposals",
		"records": harness.Jsonl(map[string]any{"name": "a", "project": "shorts", "models": "openrouter/llama"})}, nil)
	other := e.RowAction("picker", "main", "picker.apply", nil)
	params := other["params"].(map[string]any)
	params["allows"] = apply["params"].(map[string]any)["allows"]
	_, out = e.Press(other)
	_ = json.Unmarshal(out, &run)
	if p := e.WaitState(run.Process, "done", "error"); !strings.Contains(string(p.Result), "waiting") {
		t.Fatalf("a call the press did not name waits for the owner: %+v", p)
	}
	e.Must("deck.asks_list", nil, &asks)
	e.Answer(asks[0].ID, "deny")

	// The yes is the owner's alone: a run that gives allows to a command it starts is refused.
	_, out = e.Call("picker.apply", map[string]any{"id": apply["params"].(map[string]any)["id"], "relay": other["params"].(map[string]any)["allows"]})
	_ = json.Unmarshal(out, &run)
	if relay := e.WaitState(run.Process, "done", "error"); !strings.Contains(string(relay.Result), "allows comes only with a press of the owner") {
		t.Fatalf("a run another caller started is no press of the owner: %+v", relay)
	}
	if got := e.Assigned("shorts", "text"); !slices.Equal(got, []string{"openrouter/gemma"}) {
		t.Fatalf("nothing but the press changed the models: %v", got)
	}
}

// TestAPressStandsForNoMoneyAndNoFrame: a button may not name a call that sets prices, a custom block's frame presses
// without the owner so its buttons name none, and allows comes from the property alone, not from a param.
func TestAPressStandsForNoMoneyAndNoFrame(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	form := `{"connection": "openrouter", "model": "llama", "kind": "text", "free": true}`
	params, _ := json.Marshal(map[string]string{"method": "deck.model_set", "form": form})
	e.Project("picker", harness.ScriptManifest("main", "", `, "screens": [{"name": "main", "title": "Models", "blocks": [
    {"type": "actions", "title": "Model", "actions": [{"label": "Add", "command": "main", "params": `+string(params)+`,
      "allows": [{"command": "deck.model_set", "params": {"connection": "openrouter", "model": "llama", "kind": "text", "free": "true"}}]}]}]}], "menu": ["main"]`))
	e.SaveScript("picker", "scripts/main.py", harness.PressScript)
	add := e.RowAction("picker", "main", "picker.main", nil)
	if _, ok := add["params"].(map[string]any)["allows"]; !ok {
		t.Fatalf("the button names its call: %+v", add)
	}
	_, out := e.Press(add)
	var run struct{ Process int64 }
	_ = json.Unmarshal(out, &run)
	if p := e.WaitState(run.Process, "done", "error"); !strings.Contains(string(p.Result), "waiting") {
		t.Fatalf("a model with its price waits for the owner's own yes, press or not: %+v", p)
	}

	frame := strings.Replace(harness.ScriptManifest("main", "", `, "screens": [{"name": "main", "title": "Models", "blocks": [
    {"type": "custom", "document": "custom/x.html", "actions": [{"label": "Add", "command": "main", "params": {"method": "x"},
      "allows": [{"command": "deck.model_set", "params": {"model": "llama"}}]}]}]}]`), `"version": 13`, `"version": 13, "title": "Frame"`, 1)
	if code, out := e.Call("deck.manifest_save", map[string]any{"project": "picker", "manifest": frame}); code < 400 || !strings.Contains(string(out), "custom block") {
		t.Fatalf("a custom block names no call its press allows: %d %s", code, out)
	}
	literal := harness.ScriptManifest("main", "", `, "screens": [{"name": "main", "title": "Models", "blocks": [
    {"type": "actions", "title": "Model", "actions": [{"label": "Add", "command": "main", "params": {"allows": "[]"}}]}]}]`)
	if code, out := e.Call("deck.manifest_save", map[string]any{"project": "picker", "manifest": literal}); code < 400 || !strings.Contains(string(out), `"allows"`) {
		t.Fatalf("allows is no param a button sets itself: %d %s", code, out)
	}
}
