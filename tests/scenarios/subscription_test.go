package scenarios

import (
	"slices"
	"testing"
)

// TestACallOnASubscriptionShowsAnEstimateOutOfTheMoneySpent: what Claude Code says a turn cost is an estimate of a
// subscription: the process, the spend by day and by model and the spend screen keep it apart from the money spent.
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
	page := e.screenData("", "spend", nil)
	rows := blockData[[]screenRow](t, page["b4-0-0"]["rows"])
	if len(rows) != 1 || rows[0].Cells[0] != "plain" || rows[0].Cells[1] != nil || rows[0].Cells[4].(float64) < 0.04 {
		t.Fatalf("the spend of a project shows a model on a subscription apart from the money paid: %+v", rows)
	}
	if paid := string(page["b2"]["value"]); paid != "0" {
		t.Fatalf("what is paid leaves the subscription out: %s", page["b2"])
	}
	var models struct {
		Models []struct {
			Name         string
			Subscription bool
		}
	}
	e.must("deck.models_list", nil, &models)
	if i := slices.IndexFunc(models.Models, func(m struct {
		Name         string
		Subscription bool
	}) bool {
		return m.Name == "claude-code"
	}); i < 0 || !models.Models[i].Subscription {
		t.Fatalf("Claude Code is a model on a subscription: %+v", models.Models)
	}
}

// TestATurnOnAModelOfAConnectionOnASubscriptionCostsNoMoney: a model of a connection the owner marked as paid by a
// subscription keeps what the turns of its agent would cost out of the money, as Claude Code does.
func TestATurnOnAModelOfAConnectionOnASubscriptionCostsNoMoney(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	e := acpEnv(t, f, "goose", acpLLM(t))
	if _, err := e.App.Store.DB.Exec(`UPDATE model SET subscription = 1, input_usd = 1000, output_usd = 1000 WHERE name = 'acp'`); err != nil {
		t.Fatal(err)
	}
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, acpModel,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	id := e.run(e.acpProject("paint", colorManifest), "work")
	if got := e.waitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the turn runs: %+v", got)
	}
	var p struct {
		CostUSD         *float64 `json:"cost_usd"`
		SubscriptionUSD *float64 `json:"subscription_usd"`
	}
	e.must("deck.process_get", map[string]any{"process": id}, &p)
	if p.CostUSD != nil || p.SubscriptionUSD == nil || *p.SubscriptionUSD <= 0 {
		t.Fatalf("the turn cost nothing apart from the subscription, and shows the estimate: %+v", p)
	}
	var days []struct {
		Key             string
		CostUSD         float64 `json:"cost_usd"`
		SubscriptionUSD float64 `json:"subscription_usd"`
	}
	e.must("deck.spend_daily", map[string]any{"by": "provider"}, &days)
	if len(days) != 1 || days[0].CostUSD != 0 || days[0].SubscriptionUSD <= 0 {
		t.Fatalf("the spend keeps the estimate out of the money: %+v", days)
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
			if r.Cells[2] != "unchecked" || r.Details[2] != "No work has gone to it yet" {
				t.Fatalf("a model never called says so: %+v", r.Cells)
			}
			return
		}
	}
	t.Fatalf("no row of the model: %+v", rows)
}
