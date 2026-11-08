package providers

import (
	"strings"
	"testing"

	"deck2/tests/harness"
)

// TestAnAgentOnANodeIsAModelDeckHas: an assignment of codex@Mac names a model deck has while its node is named Mac;
// renamed, health says deck has no such model, and named Mac again, the row goes.
func TestAnAgentOnANodeIsAModelDeckHas(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithCodex(harness.NewCodex(t)))
	n := e.Node("Mac")
	e.Project("paint", harness.ColorManifest)
	e.UseModels("paint.work", "codex@Mac")
	derive := func(name string) { e.Project(name, harness.PlainManifest) }
	e.Must("deck.device_rename", map[string]any{"device": n.Joined.Device, "name": "Mini"}, nil)
	derive("first")
	if h := e.WaitHealth("model:codex@Mac", "warning"); !strings.Contains(h.Error, "deck has no such model") {
		t.Fatalf("an agent on a node deck has none of is no model: %+v", h)
	}
	e.Must("deck.device_rename", map[string]any{"device": n.Joined.Device, "name": "Mac"}, nil)
	derive("second")
	e.Until("the row of codex@Mac to go", func() bool { return e.HealthRowOf("model:codex@Mac") == nil })
}
