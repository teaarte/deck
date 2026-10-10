package scenarios

import (
	"testing"
)

// TestACallOnASubscriptionShowsAnEstimateOutOfTheMoneySpent: what Claude Code says a turn cost is an estimate of a
// subscription: the process, the spend by day and by model keep it apart from the money spent.
func TestACallOnASubscriptionShowsAnEstimateOutOfTheMoneySpent(t *testing.T) {
	t.Parallel()
	c := newCLI(t)
	c.stream(0, "ok.jsonl")
	e := clockCLIEnv(t, c)
	id := e.run(e.createProject("plain", plainManifest), "work")
	if got := e.waitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the turn runs: %+v", got)
	}
	var p struct {
		CostUSD         *float64 `json:"cost_usd"`
		SubscriptionUSD *float64 `json:"subscription_usd"`
	}
	e.must("deck.process_get", map[string]any{"process": id}, &p)
	if p.CostUSD != nil || p.SubscriptionUSD == nil || *p.SubscriptionUSD < 0.04 {
		t.Fatalf("the process cost nothing apart from the subscription, and shows the estimate: %+v", p)
	}
	var days []struct {
		CostUSD         float64 `json:"cost_usd"`
		SubscriptionUSD float64 `json:"subscription_usd"`
		Subscription    bool
	}
	e.must("deck.spend_daily", map[string]any{"by": "provider"}, &days)
	if len(days) != 1 || days[0].CostUSD != 0 || days[0].SubscriptionUSD < 0.04 || !days[0].Subscription {
		t.Fatalf("the spend keeps the estimate out of the money: %+v", days)
	}
	var models struct {
		Models []struct {
			Name         string
			Subscription bool
		}
	}
	e.must("deck.models_list", nil, &models)
	for _, m := range models.Models {
		if m.Name == "claude-code" && !m.Subscription {
			t.Fatalf("Claude Code is a model on a subscription: %+v", m)
		}
	}
}

// TestAModelOfTheSettingsMayBeOnASubscription: the owner marks a model of a connection as paid by a subscription,
// and it keeps the mark when changed without it.
func TestAModelOfTheSettingsMayBeOnASubscription(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "zai", "api": "openai", "url": "http://127.0.0.1:1/v1", "key": "k"}, nil)
	var m struct{ Subscription bool }
	e.must("deck.model_set", map[string]any{"connection": "zai", "model": "glm-5", "kind": "text", "subscription": true}, &m)
	if !m.Subscription {
		t.Fatalf("the model is on a subscription: %+v", m)
	}
	e.must("deck.model_set", map[string]any{"connection": "zai", "model": "glm-5", "kind": "text", "input_usd": 1}, &m)
	if !m.Subscription {
		t.Fatalf("a change without the mark keeps it: %+v", m)
	}
}

// TestAModelNeverCalledSaysSo: a model of a connection no call reached yet says so in the table of models instead of
// an empty status.
func TestAModelNeverCalledSaysSo(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "zai", "api": "openai", "url": "http://127.0.0.1:1/v1", "key": "k"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "zai", "model": "glm-5", "kind": "text"}, nil)
	rows := blockData[[]screenRow](t, e.settingsBlock("Models", nil)["rows"])
	for _, r := range rows {
		if r.Cells[0] == "zai/glm-5" {
			if r.Cells[5] != "not called yet" {
				t.Fatalf("a model never called says so: %+v", r.Cells)
			}
			return
		}
	}
	t.Fatalf("no row of the model: %+v", rows)
}
