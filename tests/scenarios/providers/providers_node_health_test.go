package providers

import (
	"net/http"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/internal/provider/remote"
	"deck2/tests/harness"
)

// codexNode is the node Mac that runs the agents its owner allows on a fake Codex; allow comes before the start,
// since a node tells its agents when it connects.
func codexNode(t *testing.T, e *harness.Env, allow ...string) *harness.Node {
	onNode := harness.NewCodex(t)
	n := e.PairNode("Mac")
	n.RunAgents(func(spec remote.Spec, home string, _ func(provider.ModelHooks) http.Handler) provider.Provider {
		return onNode.Adapter(home)
	})
	n.Allow(allow...)
	n.Start()
	return n
}

// TestAnAgentOnANodeIsAModelDeckHas: codex@Mac is refused while the node does not run codex; allowed, it is a model
// deck has while its node is named Mac; renamed, health says deck has no such model, named Mac again, the row goes.
func TestAnAgentOnANodeIsAModelDeckHas(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithCodex(harness.NewCodex(t)))
	n := codexNode(t, e)
	e.Project("paint", harness.ColorManifest)
	e.Until("the node online", func() bool { return e.HealthRowOf("node:Mac") != nil })
	e.Refused("deck.assignment_set", map[string]any{"role": "agent", "project": "paint", "command": "work", "models": []string{"codex@Mac"}},
		422, "node Mac does not run the agent codex")
	n.Allow("codex")
	n.Stop()
	n.Start()
	e.Until("codex@Mac among the agents", func() bool { return hasModel(e, "codex@Mac") })
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

// TestWorkWhoseNodeIsAwaySaysSo: work whose first model is on a node that went away says, in the owner's words, why
// it goes to the next one.
func TestWorkWhoseNodeIsAwaySaysSo(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithCodex(harness.NewCodex(t)))
	n := codexNode(t, e, "codex")
	e.Until("codex@Mac among the agents", func() bool { return hasModel(e, "codex@Mac") })
	e.Project("paint", harness.ColorManifest)
	e.UseModels("paint.work", "codex@Mac", "codex")
	n.Stop()
	var why string
	e.Until("the work to say its node is away", func() bool {
		var out struct {
			Assignments []struct{ Project, Why string }
		}
		e.Must("deck.models_list", map[string]any{"lang": "ru"}, &out)
		for _, a := range out.Assignments {
			if a.Project == "paint" {
				why = a.Why
			}
		}
		return why != ""
	})
	if why != "codex@Mac: его узел Mac не в сети" {
		t.Fatalf("the work says its node is away: %q", why)
	}
}
