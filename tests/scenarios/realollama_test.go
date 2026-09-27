package scenarios

import (
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

const ollamaManifest = `{"version": 5,
  "collections": [{"name": "notes", "description": "Notes", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}}, "required": ["title"]}}],
  "commands": [{"name": "collect", "title": "Collect", "description": "Collects notes.", "class": "internal", "requires": ["no_cloud"],
    "executor": {"type": "agent", "prompt": "Write exactly one note with the title deck-ollama into the notes collection with the records_upsert tool. Then hand in the result: written is the number of notes you wrote."},
    "result": {"type": "object", "properties": {"written": {"type": "integer"}}, "required": ["written"]}}]}`

// TestRealOllamaAgentTurn runs one agent turn on a local model: a deck tool call and result_submit by the schema.
func TestRealOllamaAgentTurn(t *testing.T) {
	if os.Getenv("DECK_REAL_OLLAMA") == "" {
		t.Skip("set DECK_REAL_OLLAMA=1 to run against a local Ollama")
	}
	base := os.Getenv("DECK_OLLAMA_URL")
	if base == "" {
		base = "http://localhost:11434"
	}
	if resp, err := (&http.Client{Timeout: 3 * time.Second}).Get(base + "/api/tags"); err != nil {
		t.Skipf("Ollama is not running at %s: %v", base, err)
	} else {
		resp.Body.Close()
	}
	model := os.Getenv("DECK_OLLAMA_MODEL")
	if model == "" {
		model = "laguna-xs-2.1:nvfp4"
	}
	e := newEnv(t, nil)
	e.must("deck.provider_add", map[string]any{"name": "ollama", "url": base + "/v1", "model": model, "no_cloud": true}, nil)
	p := e.createProject("notes", ollamaManifest)
	id := e.run(p, "collect")
	deadline := time.Now().Add(8 * time.Minute)
	got := e.process(id)
	for ; time.Now().Before(deadline) && got.State != "done" && got.State != "error"; got = e.process(id) {
		time.Sleep(time.Second)
	}
	t.Logf("%s: state %s, result %s, error %s", model, got.State, got.Result, got.Error)
	var recs []map[string]any
	e.must("deck.records_list", map[string]any{"project": "notes", "collection": "notes"}, &recs)
	if got.State != "done" || !strings.Contains(string(got.Result), `"written"`) || len(recs) == 0 {
		t.Fatalf("the local model writes the note through the gateway and hands in the result: %+v, records %v", got, recs)
	}
}
