package scenarios

import (
	"deck2/tests/harness"
	"encoding/json"
	"testing"
)

// TestReadsGiveTheirResultSchema: commands that only read are reads, with the schema of their result, so a
// screen can show them.
func TestReadsGiveTheirResultSchema(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	for _, name := range []string{"records_list", "record_get", "manifest_get", "chat"} {
		var d struct {
			Result json.RawMessage `json:"result"`
		}
		e.Must("deck.command_describe", map[string]any{"command": "deck." + name}, &d)
		if len(d.Result) == 0 || string(d.Result) == "{}" {
			t.Errorf("%s gives the schema of its result: %s", name, d.Result)
		}
	}
}
