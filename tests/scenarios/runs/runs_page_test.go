package runs

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"deck2/tests/harness"
)

// TestTheProcessPageSaysHowFastAndOffersAnotherModel: the page of a process says how fast its model answered; a
// failed one offers to change the model of its command, among those that could take it, and to run it again.
func TestTheProcessPageSaysHowFastAndOffersAnotherModel(t *testing.T) {
	t.Parallel()
	down := false
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			if down {
				return 500, "application/json", `{"error": {"message": "the model is down"}}`
			}
			time.Sleep(30 * time.Millisecond)
			return 200, "application/json", harness.ChatText(`{"verdict": "true", "confidence": 0.9}`)
		},
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "groq", "url": api.URL + "/v1", "key": "k"}, nil)
	for _, m := range []string{"fact-model", "spare"} {
		e.Must("deck.model_set", map[string]any{"connection": "groq", "model": m, "kind": "text"}, nil)
	}
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"groq/fact-model"}}, nil)
	e.Project("films", harness.ModelManifest)
	card := func(id int64) map[string]any {
		t.Helper()
		var v map[string]any
		_ = json.Unmarshal(e.Screen("", "process", map[string]any{"record": id})["b0"]["value"], &v)
		return v
	}
	ok := e.Start("films", "factcheck", map[string]any{"claim": "one"})
	e.WaitState(ok, "done")
	var got struct {
		Speed float64
		Calls int
	}
	e.Must("deck.process_get", map[string]any{"process": ok}, &got)
	if got.Speed <= 0 || got.Calls != 1 {
		t.Fatalf("a process says how fast its model answered: %+v", got)
	}
	if acts := card(ok)["actions"]; harness.HasVerb(toActs(acts), "model") || harness.HasVerb(toActs(acts), "retry") {
		t.Fatalf("a process that went well offers no other model: %+v", acts)
	}

	down = true
	failed := e.Start("films", "factcheck", map[string]any{"claim": "two"})
	e.WaitState(failed, "error")
	acts := toActs(card(failed)["actions"])
	model := harness.Verb(t, acts, "model")
	if model["command"] != "deck.assignment_set" || fmt.Sprint(model["options"]) != "[groq/fact-model groq/spare]" || !harness.HasVerb(acts, "retry") {
		t.Fatalf("a failed process offers another model of its command and to run it again: %+v", acts)
	}
	params := model["params"].(map[string]any)
	params["model"] = "groq/spare"
	e.Must("deck.assignment_set", params, nil)
	var m harness.ModelsOut
	e.Must("deck.models_list", map[string]any{"project": "films"}, &m)
	for _, a := range m.Assignments {
		if a.Project == "films" && a.Command == "factcheck" && (len(a.Models) == 0 || a.Models[0] != "groq/spare") {
			t.Fatalf("the change of model is the command's from then on: %+v", a)
		}
	}
}

func toActs(v any) []map[string]any {
	raw, _ := json.Marshal(v)
	var out []map[string]any
	_ = json.Unmarshal(raw, &out)
	return out
}
