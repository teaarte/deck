package scenarios

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestTheMasterReadsTheFieldsOfAFormOnlyTheOwnerFillsIn: command_describe gives the master the schema of a
// command closed to it, and a call that misses a field says which one in plain words.
func TestTheMasterReadsTheFieldsOfAFormOnlyTheOwnerFillsIn(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.token("helper", "deck.command_describe", "deck.model_set")
	code, out := e.callWith(tok, "deck.command_describe", map[string]any{"command": "deck.model_set"})
	var spec struct {
		Name    string
		Callers []string
		Input   struct {
			Properties map[string]struct{ Enum []string }
			Required   []string
		}
	}
	if code != 200 || json.Unmarshal(out, &spec) != nil {
		t.Fatalf("command_describe: %d %s", code, out)
	}
	if spec.Name != "deck.model_set" || len(spec.Callers) != 1 || spec.Callers[0] != "owner" ||
		strings.Join(spec.Input.Properties["agent"].Enum, ",") != "dsh,goose" {
		t.Fatalf("the master sees the fields of the model form and the agents to pick: %s", out)
	}
	if code, out := e.callWith(tok, "deck.model_set", map[string]any{"connection": "x", "model": "m", "agent": "dsh"}); code != 403 {
		t.Fatalf("describing a command does not open it: %d %s", code, out)
	}
	e.refused("deck.model_set", map[string]any{"connection": "x"}, 422,
		"input of deck.model_set: missing property 'model'; deck.command_describe gives its parameters")
	e.refused("deck.model_set", map[string]any{"connection": "x", "model": "m", "agent": "foo"}, 422,
		"agent: value must be one of 'dsh', 'goose'")
}

// TestReadsGiveTheirResultSchema: commands that only read are reads, with the schema of their result, so a
// screen can show them.
func TestReadsGiveTheirResultSchema(t *testing.T) {
	e := newEnv(t, nil)
	for _, name := range []string{"records_list", "record_get", "manifest_get", "chat"} {
		var d struct {
			Result json.RawMessage `json:"result"`
		}
		e.must("deck.command_describe", map[string]any{"command": "deck." + name}, &d)
		if len(d.Result) == 0 || string(d.Result) == "{}" {
			t.Errorf("%s gives the schema of its result: %s", name, d.Result)
		}
	}
}
