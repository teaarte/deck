package providers

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// TestTheModelsTabSaysFirstWhatWaitsForTheOwner: on top of Settings → Models what waits, a line a thing with one
// button (a model down, replaced in all its work at once; a key missing); the lists seldom needed read once opened.
func TestTheModelsTabSaysFirstWhatWaitsForTheOwner(t *testing.T) {
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(_ int, body map[string]any) (int, string, string) {
			if body["model"] == "busy" {
				return 429, "application/json", `{"error": {"message": "rate limited"}}`
			}
			return 200, "application/json", harness.ChatText(`{"verdict": "true", "confidence": 0.9}`)
		},
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "groq", "url": api.URL + "/v1", "key": "gsk-1"}, nil)
	for _, m := range []string{"busy", "fact-model", "spare"} {
		e.Must("deck.model_set", map[string]any{"connection": "groq", "model": m, "kind": "text"}, nil)
	}
	e.Must("deck.connection_set", map[string]any{"name": "claude", "url": "https://api.anthropic.com/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "claude", "model": "claude-haiku", "kind": "text"}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/busy", "groq/fact-model"}}, nil)
	e.Project("films", harness.ModelManifest)
	e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "busy"}), "done", "error")

	var conns struct {
		Connections []struct{ Name, API, Where string }
	}
	e.Must("deck.models_list", nil, &conns)
	for _, c := range conns.Connections {
		if want := map[string]string{"groq": "openai", "claude": "anthropic"}[c.Name]; want != "" && c.API != want {
			t.Fatalf("a connection without its API takes it by its address: %+v", c)
		}
	}
	waits := harness.BlockData[[]harness.ListItem](t, e.SettingsBlock("Waiting for you", nil)["items"])
	down := slices.IndexFunc(waits, func(w harness.ListItem) bool { return strings.HasPrefix(w.Title, "groq/busy") })
	key := slices.IndexFunc(waits, func(w harness.ListItem) bool { return strings.Contains(w.Title, "key of claude") })
	if down != 0 || key < 0 || waits[down].Tone != "error" || waits[down].Logo != "busy groq" || !strings.Contains(waits[down].Text, "films") {
		t.Fatalf("what waits says first the model that is down in its own mark, with the work it leaves, then the connection without a key: %+v", waits)
	}
	replace := harness.Verb(t, waits[down].Actions, "replace")
	if replace["command"] != "deck.model_replace" || replace["primary"] != true ||
		strings.Contains(fmt.Sprint(replace["options"]), "groq/busy") || !strings.Contains(fmt.Sprint(replace["options"]), "groq/spare") {
		t.Fatalf("the model that is down has one button that replaces it by another of its kind: %+v", replace)
	}
	if add := waits[key].Actions[0]; add["screen"] != "settings" || add["params"].(map[string]any)["dialog"] != "connections" ||
		add["params"].(map[string]any)["name"] != "claude" {
		t.Fatalf("the connection without a key opens its form: %+v", add)
	}

	params := replace["params"].(map[string]any)
	e.Must("deck.model_replace", map[string]any{"model": params["model"], "role": params["role"], "to": "groq/spare"}, nil)
	var m harness.ModelsOut
	e.Must("deck.models_list", nil, &m)
	if i := slices.IndexFunc(m.Assignments, func(a harness.AssignmentOut) bool { return a.Role == "text" && a.Project == "" }); i < 0 ||
		fmt.Sprint(m.Assignments[i].Models) != "[groq/spare groq/fact-model]" {
		t.Fatalf("a replace puts the model first and takes the one down out: %+v", m.Assignments)
	}
	if slices.ContainsFunc(m.Assignments, func(a harness.AssignmentOut) bool { return a.Project == "films" }) {
		t.Fatalf("a replace writes where the chain comes from, and a command that follows the default keeps following it: %+v", m.Assignments)
	}
	waits = harness.BlockData[[]harness.ListItem](t, e.SettingsBlock("Waiting for you", nil)["items"])
	if slices.ContainsFunc(waits, func(w harness.ListItem) bool { return strings.HasPrefix(w.Title, "groq/busy") }) {
		t.Fatalf("a model replaced waits no more: %+v", waits)
	}
	if code, out := e.Call("deck.model_replace", map[string]any{"model": "groq/busy", "role": "text", "to": "groq/spare"}); code != 404 {
		t.Fatalf("a model no work goes to first is not replaced: %d %s", code, out)
	}

	var used struct {
		Models []struct {
			Name     string
			UsedIn   string `json:"used_in"`
			Projects int
			Places   []string
		}
	}
	e.Must("deck.models_list", nil, &used)
	for _, x := range used.Models {
		if x.Name == "groq/spare" && (x.Projects < 1 || x.UsedIn == "" || len(x.Places) == 0) {
			t.Fatalf("a model says in how many modules it works and where: %+v", x)
		}
	}

	tab := harness.DataOf(t, e.ScreenMessages("", "settings", map[string]any{"tab": "Models", "block": "b0", "open": "[]"}))
	for _, title := range []string{"Models in use", "Models not in use", "Connections"} {
		id, _ := e.FindBlock("", "settings", title)
		if tab[id] != nil || tab["b0-0-5"] == nil {
			t.Fatalf("a tab shown reads its blocks but %s, collapsed: %v", title, id)
		}
		folded := harness.DataOf(t, e.ScreenMessages("", "settings", map[string]any{"tab": "Models", "open": "[]"}))[id]
		if folded != nil {
			t.Fatalf("%s stands collapsed, its data read once it is opened: %s", title, folded)
		}
		if opened := harness.DataOf(t, e.ScreenMessages("", "settings", map[string]any{"tab": "Models", "open": `["` + id + `"]`}))[id]; opened == nil {
			t.Fatalf("%s opened is read: %v", title, id)
		}
	}
}
