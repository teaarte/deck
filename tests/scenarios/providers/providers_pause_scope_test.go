package providers

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/internal/provider/remote"
	"deck2/tests/harness"
)

// callsOf are the calls of the fake API that named model.
func callsOf(f *harness.FakeModels, model string) int {
	n := 0
	for _, c := range made(f) {
		if c.Model == model {
			n++
		}
	}
	return n
}

// statuses are the statuses of the models in Settings → Models, by name.
func statuses(e *harness.Env) map[string]string {
	var out struct {
		Models []struct{ Name, Status string }
	}
	e.Must("deck.models_list", map[string]any{}, &out)
	m := map[string]string{}
	for _, x := range out.Models {
		m[x.Name] = x.Status
	}
	return m
}

// TestAFreeLimitOfAnAccountPausesItsFreeModelsOnly: free-models-per-min pauses every free model of that connection; a
// paid model of the same connection and a free one of another connection are still called.
func TestAFreeLimitOfAnAccountPausesItsFreeModelsOnly(t *testing.T) {
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(_ int, body map[string]any) (int, string, string) {
			if body["model"] == "a:free" {
				return 429, "application/json", `{"error": {"message": "Rate limit exceeded: free-models-per-min. ", "code": 429}}`
			}
			return 200, "application/json", harness.ChatText(`{"verdict": "true", "confidence": 0.9}`)
		},
	})
	e := harness.New(t, harness.WithClock())
	for _, c := range []string{"openrouter", "nvidia"} {
		e.Must("deck.connection_set", map[string]any{"name": c, "api": "openai", "url": api.URL + "/v1", "key": "k-" + c}, nil)
	}
	for _, m := range []struct {
		connection, model, name string
		free                    bool
	}{{"openrouter", "a:free", "or-a", true}, {"openrouter", "b:free", "or-b", true}, {"openrouter", "paid", "or-paid", false}, {"nvidia", "n-free", "nv-free", true}} {
		e.Must("deck.model_set", map[string]any{"connection": m.connection, "model": m.model, "name": m.name, "kind": "text", "free": m.free}, nil)
	}
	e.Project("films", harness.Declared(harness.ModelManifest))
	for _, tc := range []struct{ last, model string }{{"nv-free", "n-free"}, {"or-paid", "paid"}} {
		e.Clock.Add(time.Hour)
		e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"or-a", "or-b", tc.last}}, nil)
		before := callsOf(api, "a:free")
		if got := e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": tc.last}), "done", "error"); got.State != "done" {
			t.Fatalf("the call goes on to %s: %+v %+v", tc.last, got, e.Fault(got.ID))
		}
		if callsOf(api, "a:free") != before+1 || callsOf(api, "b:free") != 0 || callsOf(api, tc.model) == 0 {
			t.Fatalf("the other free model of the account is not called, %s is: %+v", tc.last, made(api))
		}
		if h := e.HealthRowOf("limit:openrouter:free"); h == nil || h["status"] != "paused" {
			t.Fatalf("the free models of the connection pause together: %v", h)
		}
	}
	status := statuses(e)
	if status["or-b"] != "paused" || status["or-paid"] == "paused" || status["nv-free"] == "paused" {
		t.Fatalf("only the free models of openrouter are paused: %v", status)
	}
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"or-b", "or-a"}}, nil)
	before := callsOf(api, "b:free")
	e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "all paused"}), "done", "error")
	if callsOf(api, "b:free") != before+1 {
		t.Fatalf("with every model paused, each goes in its order: %+v", made(api))
	}
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"or-a", "or-b", "or-paid"}}, nil)
	e.Clock.Add(2 * time.Minute)
	before = callsOf(api, "a:free")
	e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "after"}), "done", "error")
	if callsOf(api, "a:free") != before+1 {
		t.Fatalf("at the end of the pause the first free model is called again: %+v", made(api))
	}
}

// TestARedModelStandsLastForTenMinutes: a model of a role that did not answer is called after the others for ten
// minutes, then in its place again; a call by its own name goes to it all the same.
func TestARedModelStandsLastForTenMinutes(t *testing.T) {
	t.Parallel()
	down := true
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(_ int, body map[string]any) (int, string, string) {
			if body["model"] == "first" && down {
				return 503, "application/json", `{"error": {"message": "overloaded"}}`
			}
			return 200, "application/json", harness.ChatText(`{"verdict": "true", "confidence": 0.9}`)
		},
	})
	e := harness.New(t, harness.WithClock())
	e.Must("deck.connection_set", map[string]any{"name": "groq", "api": "openai", "url": api.URL + "/v1", "key": "gsk-1"}, nil)
	for _, m := range []string{"first", "second"} {
		e.Must("deck.model_set", map[string]any{"connection": "groq", "model": m, "name": m, "kind": "text"}, nil)
	}
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"first", "second"}}, nil)
	e.Project("films", harness.Declared(harness.ModelManifest))
	run := func(claim string) {
		t.Helper()
		if got := e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": claim}), "done", "error"); got.State != "done" {
			t.Fatalf("the call is done: %+v %+v", got, e.Fault(got.ID))
		}
	}
	run("one")
	if callsOf(api, "first") != 1 || callsOf(api, "second") != 1 {
		t.Fatalf("the red first hands the call to the second: %+v", made(api))
	}
	down = false
	e.Clock.Add(9 * time.Minute)
	run("two")
	if callsOf(api, "first") != 1 || callsOf(api, "second") != 2 {
		t.Fatalf("within ten minutes the red model stands last: %+v", made(api))
	}
	e.Must("deck.model_call", map[string]any{"model": "first", "kind": "text", "prompt": "hi"}, nil)
	if callsOf(api, "first") != 2 {
		t.Fatalf("a call by name goes to the red model: %+v", made(api))
	}
	e.Clock.Add(2 * time.Minute)
	run("three")
	if callsOf(api, "first") != 3 || callsOf(api, "second") != 2 {
		t.Fatalf("after ten minutes the model is first again: %+v", made(api))
	}
}

// TestALimitOfASubscriptionPausesItsCLIOnANodeToo: a usage limit of the subscription of a CLI pauses every way to it,
// the same CLI on a node too, so no new start picks it there.
func TestALimitOfASubscriptionPausesItsCLIOnANodeToo(t *testing.T) {
	own, onNode := harness.NewCodex(t), harness.NewCodex(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	own.Stream(1, "ratelimit.jsonl")
	e := harness.New(t, harness.WithCodex(own))
	n := e.PairNode("Mac")
	n.RunAgents(func(spec remote.Spec, home string, _ func(provider.ModelHooks) http.Handler) provider.Provider {
		return onNode.Adapter(home)
	})
	n.Allow("codex")
	n.Start()
	e.Until("codex@Mac among the agents", func() bool { return hasModel(e, "codex@Mac") })
	p := e.Project("paint", harness.OnLogin(harness.ColorManifest))
	e.UseModels("paint.work", "codex", "codex@Mac")
	id := e.Run(p, "work")
	if got := e.WaitState(id, "waiting:limit", "error", "done"); got.State == "done" {
		t.Fatalf("the limit stops the turn: %+v", got)
	}
	if h := e.WaitHealth("limit:codex", "paused"); !strings.Contains(fmt.Sprint(h.Error), "next try at") {
		t.Fatalf("the limit of the subscription is a pause of its connection: %+v", h)
	}
	if status := statuses(e); status["codex@Mac"] != "paused" {
		t.Fatalf("the CLI on the node pauses with the subscription: %v", status)
	}
}
