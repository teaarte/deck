package providers

import (
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/health"
	"deck2/tests/harness"
)

// modelNames are the names of the models of deck.
func modelNames(e *harness.Env) []string {
	e.T.Helper()
	var m harness.ModelsOut
	e.Must("deck.models_list", nil, &m)
	var out []string
	for _, x := range m.Models {
		if !x.Builtin {
			out = append(out, x.Name)
		}
	}
	return out
}

// TestAModuleRemovesItsOwnFreeModelNoAssignmentNames: a module removes its own free unassigned model unasked, the owner
// undoes it; a paid, an assigned, the owner's and another module's model wait for the owner.
func TestAModuleRemovesItsOwnFreeModelNoAssignmentNames(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "mine", "kind": "text", "free": true}, nil)
	e.Project("films", harness.PlainManifest)
	module := harness.ScriptManifest("main", "", `, "rights": [{"right": "send", "what": ["deck.model_delete"], "why": "removes its own spare models"}]`)
	e.Project("picker", module)
	e.Project("other", module)
	e.Grant("picker", "send", "deck.model_delete")
	e.Grant("other", "send", "deck.model_delete")
	for _, p := range []string{"picker", "other"} {
		e.SaveScript(p, "scripts/main.py", harness.DeckCallScript)
	}
	add := func(project string, in map[string]any) {
		t.Helper()
		in["connection"], in["kind"] = "openrouter", "text"
		if c := e.ScriptCall(project, "deck.model_set", in); c.Status != 202 {
			t.Fatalf("a model a module adds waits for the owner: %+v", c)
		}
		ask, _ := callAsk(e, "deck.model_set")
		e.Answer(ask, "allow")
	}
	add("picker", map[string]any{"model": "spare", "free": true, "tpm": 8000, "options": `{"temperature": 0}`})
	add("picker", map[string]any{"model": "paid", "call_usd": 0.01})
	add("picker", map[string]any{"model": "used", "free": true})
	add("other", map[string]any{"model": "theirs", "free": true})
	e.Must("deck.assignment_set", map[string]any{"role": "text", "project": "films", "models": []string{"openrouter/used"}}, nil)

	if c := e.ScriptCall("picker", "deck.model_delete", map[string]any{"name": "openrouter/spare"}); c.Status != 200 {
		t.Fatalf("the module removes its own free model no assignment names: %+v", c)
	}
	if slices.Contains(modelNames(e), "openrouter/spare") {
		t.Fatalf("the model is gone: %v", modelNames(e))
	}
	var feed []struct {
		ID      int64  `json:"id"`
		Text    string `json:"text"`
		CanUndo bool   `json:"can_undo"`
	}
	e.Must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 1 || !feed[0].CanUndo || feed[0].Text != `deleted model "openrouter/spare"` {
		t.Fatalf("the owner sees the removal with its undo: %+v", feed)
	}
	e.Must("deck.feed_list", map[string]any{"kind": "change", "lang": "ru"}, &feed)
	if feed[0].Text != `удалена модель "openrouter/spare"` {
		t.Fatalf("in the owner's words: %+v", feed)
	}
	e.Must("deck.feed_undo", map[string]any{"entry": feed[0].ID}, nil)
	type shown struct {
		Name    string `json:"name"`
		Kind    string `json:"kind"`
		Free    bool   `json:"free"`
		TPM     int64  `json:"tpm"`
		Options string `json:"options"`
	}
	var list struct{ Models []shown }
	e.Must("deck.models_list", nil, &list)
	i := slices.IndexFunc(list.Models, func(m shown) bool { return m.Name == "openrouter/spare" })
	if i < 0 {
		t.Fatalf("the undo brings the model back: %+v", list.Models)
	}
	if m := list.Models[i]; !m.Free || m.Kind != "text" || m.TPM != 8000 || !strings.Contains(m.Options, "temperature") {
		t.Fatalf("with its former fields: %+v", m)
	}

	names := []string{"openrouter/paid", "openrouter/used", "openrouter/mine", "openrouter/theirs", "openrouter/spare"}
	for _, name := range names {
		if c := e.ScriptCall("picker", "deck.model_delete", map[string]any{"name": name}); c.Status != 202 || !strings.Contains(c.Body, "waits for the owner") {
			t.Fatalf("a removal of %s waits for the owner: %+v", name, c)
		}
		ask, _ := callAsk(e, "deck.model_delete")
		e.Answer(ask, "deny")
	}
	e.Must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if got := modelNames(e); len(feed) != 1 || slices.ContainsFunc(names, func(n string) bool { return !slices.Contains(got, n) }) {
		t.Fatalf("nothing else is removed without the owner: %v %+v", got, feed)
	}
}

// TestDeckDoesNotCheckAModelNoAssignmentNames: deck checks a model in an agent by itself only once an assignment names
// it; the owner's check runs either way.
func TestDeckDoesNotCheckAModelNoAssignmentNames(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	f.SelfCheck()
	for i := 4; i <= 9; i++ {
		f.Stream(i, "selfcheck_"+harness.Itoa(int64((i-1)%3+1))+".jsonl")
	}
	llm := harness.AcpLLM(t)
	e := harness.New(t, harness.WithACP(f, nil))
	if h := e.AddACP(f, llm); h.Status != "ok" {
		t.Fatalf("the model added is checked: %+v", h)
	}
	checked := func(e *harness.Env, calls int) {
		t.Helper()
		e.Until("the self-check", func() bool {
			h, err := health.Get(t.Context(), e.App.Store.Q, health.Of{}, "provider:acp")
			return f.Calls() >= calls && err == nil && h.Status == "ok"
		})
	}

	e = e.Restart()
	e.App.Providers.Checks(t.Context(), time.Now())
	if code, out := e.Call("deck.provider_check", map[string]any{"provider": "acp"}); code != 200 {
		t.Fatalf("deck started no check of a model no assignment names, the owner's runs: %d %s", code, out)
	}
	checked(e, 6)

	e.Project("paint", harness.ColorManifest)
	e.UseModels("paint.work", "acp")
	e = e.Restart()
	e.App.Providers.Checks(t.Context(), time.Now())
	if code, out := e.Call("deck.provider_check", map[string]any{"provider": "acp"}); code == 200 || !strings.Contains(string(out), "already running") {
		t.Fatalf("deck checks an assigned model by itself: %d %s", code, out)
	}
	checked(e, 9)
}
