package scenarios

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"deck2/internal/registry"
)

// screenAs is the data of a screen read through deck.screen by act, by block id.
func (e *Env) screenAs(act registry.Actor, in map[string]any) map[string]map[string]json.RawMessage {
	e.T.Helper()
	raw, _ := json.Marshal(in)
	out, err := e.App.Registry.Invoke(context.Background(), act, "deck.screen", raw)
	if err != nil {
		e.T.Fatal(err)
	}
	b, _ := json.Marshal(out)
	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(b, &msgs); err != nil {
		e.T.Fatal(err)
	}
	for _, m := range msgs {
		if dm, ok := m["updateDataModel"]; ok {
			var v struct {
				Value map[string]map[string]json.RawMessage `json:"value"`
			}
			if err := json.Unmarshal(dm, &v); err != nil {
				e.T.Fatal(err)
			}
			return v.Value
		}
	}
	e.T.Fatalf("screen %v has no data", in)
	return nil
}

// TestTheMasterReadsTheRunsOfTheCockpit: the runs by day the cockpit counts are read by the master as by the owner.
func TestTheMasterReadsTheRunsOfTheCockpit(t *testing.T) {
	t.Parallel()
	e := newEnv(t, idle)
	data := e.screenAs(registry.Actor{Kind: registry.Master, Workspace: 1}, map[string]any{"screen": "cockpit"})
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), "runs_daily cannot be called") {
		t.Fatalf("the master reads the runs of the cockpit: %s", raw)
	}
}
