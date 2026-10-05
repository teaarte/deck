package scenarios

import (
	"context"
	"encoding/json"
	"testing"

	"deck2/internal/provider"
)

// TestListsOfRecordsAndProcessesNarrowToWhatTheCallerAsks: records by the values of their fields with only the
// fields asked for, processes of one command without their results.
func TestListsOfRecordsAndProcessesNarrowToWhatTheCallerAsks(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"big": "result"}`)}
	})
	p := e.createProject("web", boardWebManifest)
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": jsonl(
		map[string]any{"title": "a", "score": 3, "source": "x", "note": "long"},
		map[string]any{"title": "b", "score": 1, "source": "x"},
		map[string]any{"title": "c", "score": 3, "source": "y"})}, nil)
	var recs []struct {
		Key  string
		Data map[string]any
	}
	e.must("deck.records_list", map[string]any{"project": "web", "collection": "notes", "where": `{"score": 3}`, "fields": []string{"title", "note"}}, &recs)
	if len(recs) != 2 || recs[0].Key != "a" || recs[1].Key != "c" || len(recs[0].Data) != 2 || recs[0].Data["note"] != "long" || len(recs[1].Data) != 1 {
		t.Fatalf("records with score 3, with only their title and note: %+v", recs)
	}
	e.must("deck.records_list", map[string]any{"project": "web", "collection": "notes", "where": "score > 2 && source == 'y'"}, &recs)
	if len(recs) != 1 || recs[0].Key != "c" {
		t.Fatalf("where is an expression over the fields, as in a source of a screen: %+v", recs)
	}
	e.must("deck.records_list", map[string]any{"project": "web", "collection": "notes", "where": `{"source": "x", "score": 1}`}, &recs)
	if len(recs) != 1 || recs[0].Key != "b" {
		t.Fatalf("a JSON object of field to value still narrows by equality: %+v", recs)
	}
	e.must("deck.records_list", map[string]any{"project": "web", "collection": "notes", "where": `{"source": "y", "title": "\/c"}`}, &recs)
	if len(recs) != 0 {
		t.Fatalf("an object of field to value narrows too, a JSON escape included: %+v", recs)
	}
	e.must("deck.records_list", map[string]any{"project": "web", "collection": "notes", "where": map[string]any{"source": "y"}}, &recs)
	if len(recs) != 1 || recs[0].Key != "c" {
		t.Fatalf("a where sent as an object, as saved calls do: %+v", recs)
	}
	e.refused("deck.records_list", map[string]any{"project": "web", "collection": "notes", "where": `[1]`}, 422, "where")
	e.refused("deck.records_list", map[string]any{"project": "web", "collection": "notes", "where": "score >"}, 422, "where")

	e.waitState(e.run(p, "tell"), "done")
	e.waitState(e.run(p, "touch"), "done")
	var ps []struct {
		Command string
		Result  json.RawMessage
	}
	e.must("deck.processes_list", map[string]any{"project": "web", "command": "tell", "brief": true}, &ps)
	if len(ps) != 1 || ps[0].Command != "tell" || ps[0].Result != nil {
		t.Fatalf("the processes of one command, without results: %+v", ps)
	}
	e.must("deck.processes_list", map[string]any{"project": "web", "command": "tell"}, &ps)
	if len(ps) != 1 || string(ps[0].Result) != `{"big":"result"}` {
		t.Fatalf("without brief the result is there: %+v", ps)
	}
}
