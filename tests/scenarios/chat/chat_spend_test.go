package chat

import (
	"context"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestTheMasterCountsTheCallsOfModels: the master reads the spend of models by command and by day, to propose moving
// a frequent paid task to a server of the owner's machine.
func TestTheMasterCountsTheCallsOfModels(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		var said []string
		for _, read := range []string{"spend_list", "spend_days_list"} {
			out, bad := c.Tool(ctx, read, map[string]any{})
			if bad {
				said = append(said, read+" refused: "+out)
			} else {
				said = append(said, read+" read")
			}
		}
		return provider.Result{Text: strings.Join(said, "; ")}
	}))
	topic := e.Say(0, "Which models do my modules call most?")
	if got := e.MasterSaid(topic, 1)[0].Text; got != "spend_list read; spend_days_list read" {
		t.Fatalf("the master reads the spend of models: %s", got)
	}
}
