package providers

import (
	"strings"
	"testing"

	"deck2/tests/harness"
)

// modelsAnswering is a model API whose chat answers text with each call.
func modelsAnswering(t *testing.T, text string) *harness.FakeModels {
	return harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", harness.ChatText(text)
		},
	})
}

// chats is how many chat calls a model API got.
func chats(m *harness.FakeModels) int {
	n := 0
	for _, c := range m.Calls() {
		if c.Path == "/v1/chat/completions" {
			n++
		}
	}
	return n
}

// TestABadAnswerOfAModelHandsTheCallToTheNext: a model that answers what is not JSON by the schema fails as the
// model's, and the next model assigned takes the call; with no next model the call fails as the model's, not deck's.
func TestABadAnswerOfAModelHandsTheCallToTheNext(t *testing.T) {
	t.Parallel()
	bad, good := modelsAnswering(t, "not json at all"), modelsAnswering(t, `{"verdict": "true", "confidence": 1}`)
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "bad", "api": "openai", "url": bad.URL + "/v1", "key": "k1"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "good", "api": "openai", "url": good.URL + "/v1", "key": "k2"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "bad", "model": "m1", "kind": "text"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "good", "model": "m2", "kind": "text"}, nil)

	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"bad/m1"}}, nil)
	code, out := e.Call("deck.model_call", map[string]any{"kind": "text", "prompt": "x", "schema": `{"type":"object"}`})
	if code != 502 || !strings.Contains(string(out), "not JSON") {
		t.Errorf("a model's bad answer fails the call as the model's (502), not deck's: %d %s", code, out)
	}

	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"bad/m1", "good/m2"}}, nil)
	code, out = e.Call("deck.model_call", map[string]any{"kind": "text", "prompt": "x", "schema": `{"type":"object"}`})
	if code != 200 || !strings.Contains(string(out), `"verdict":"true"`) || chats(good) != 1 {
		t.Errorf("the next model answers after a bad answer: %d %s, next asked %d", code, out, chats(good))
	}

	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"bad/m1"}}, nil)
	e.Project("films", harness.ModelManifest)
	p := e.WaitState(e.Start("films", "factcheck", map[string]any{"claim": "x"}), "done", "error")
	var typ string
	// The type of the failure the process keeps; deck shows it only as words.
	if err := e.SQLRow(`SELECT error_type FROM process WHERE id = ?`, p.ID).Scan(&typ); err != nil {
		t.Fatal(err)
	}
	if p.State != "error" || typ != "model.unavailable" {
		t.Fatalf("a command of type model on a bad answer fails as the model's: %s %s %q", p.State, typ, p.Error)
	}
}

// TestAPictureModelWithoutAPictureHandsOnAndAVideoWithoutAJobDoesNot: no picture hands the call on; a video request
// taken without a job may still be made and billed, so it fails as the model's and goes to no other model.
func TestAPictureModelWithoutAPictureHandsOnAndAVideoWithoutAJobDoesNot(t *testing.T) {
	t.Parallel()
	blank := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/images/generations": func(int, map[string]any) (int, string, string) { return 200, "application/json", `{"data": []}` },
		"/v1/videos": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", `{"status": "pending"}`
		},
	})
	painter := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/images/generations": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", `{"data": [{"b64_json": "iVBORw0KGgo="}]}`
		},
	})
	spare := videoJob(t, `{"id": "job-1", "status": "completed"}`)
	e := harness.New(t, harness.WithConfig(quickVideo))
	e.Must("deck.connection_set", map[string]any{"name": "blank", "api": "openai", "url": blank.URL + "/v1", "key": "k1"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "painter", "api": "openai", "url": painter.URL + "/v1", "key": "k2"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "spare", "api": "openai", "url": spare.URL + "/api/v1", "key": "k3"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "blank", "model": "i1", "name": "blank-image", "kind": "image"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "painter", "model": "i2", "name": "painter-image", "kind": "image"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "blank", "model": "v1", "name": "blank-video", "kind": "video"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "spare", "model": "v2", "name": "spare-video", "kind": "video"}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "image", "models": []string{"blank-image", "painter-image"}}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "video", "models": []string{"blank-video", "spare-video"}}, nil)

	// The checks of a model when it is set call it too: only the calls after them count.
	painted, spared := len(painter.Calls()), len(spare.Calls())
	if code, out := e.Call("deck.model_call", map[string]any{"kind": "image", "prompt": "a knight"}); code != 200 || len(painter.Calls()) != painted+1 {
		t.Errorf("an image model that answers no picture hands the call on: %d %s, next asked %d", code, out, len(painter.Calls())-painted)
	}
	code, out := e.Call("deck.model_call", map[string]any{"kind": "video", "prompt": "a knight rides"})
	if code != 502 || !strings.Contains(string(out), "no job id") || len(spare.Calls()) != spared {
		t.Errorf("a video request taken without a job fails as the model's and goes to no other: %d %s, next asked %d", code, out, len(spare.Calls())-spared)
	}
}
