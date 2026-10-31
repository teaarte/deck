package providers

import (
	"slices"
	"testing"

	"deck2/tests/harness"
)

// TestACallOnASubscriptionShowsAnEstimateOutOfTheMoneySpent: what Claude Code says a turn cost is an estimate of a
// subscription: the process, the spend by day and by model and the spend screen keep it apart from the money spent.
func TestACallOnASubscriptionShowsAnEstimateOutOfTheMoneySpent(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	e := harness.ClockCLIEnv(t, c)
	id := e.Run(e.Project("plain", harness.PlainManifest), "work")
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the turn runs: %+v", got)
	}
	var p struct {
		CostUSD         *float64 `json:"cost_usd"`
		SubscriptionUSD *float64 `json:"subscription_usd"`
	}
	e.Must("deck.process_get", map[string]any{"process": id}, &p)
	if p.CostUSD != nil || p.SubscriptionUSD == nil || *p.SubscriptionUSD < 0.04 {
		t.Fatalf("the process cost nothing apart from the subscription, and shows the estimate: %+v", p)
	}
	var days []struct {
		CostUSD         float64 `json:"cost_usd"`
		SubscriptionUSD float64 `json:"subscription_usd"`
		Subscription    bool
	}
	e.Must("deck.spend_daily", map[string]any{"by": "provider"}, &days)
	if len(days) != 1 || days[0].CostUSD != 0 || days[0].SubscriptionUSD < 0.04 || !days[0].Subscription {
		t.Fatalf("the spend keeps the estimate out of the money: %+v", days)
	}
	page := e.Screen("", "spend", nil)
	rows := harness.BlockData[[]harness.ScreenRow](t, page["b2-0-0"]["rows"])
	if len(rows) != 1 || rows[0].Cells[0] != "plain" || rows[0].Cells[1] != nil || rows[0].Cells[2].(float64) < 0.04 {
		t.Fatalf("the spend of a project shows a model on a subscription apart from the money paid: %+v", rows)
	}
	if paid := string(page["b0"]["value"]); paid != "0" {
		t.Fatalf("what is paid leaves the subscription out: %s", page["b0"])
	}
	if covered := string(page["b1"]["value"]); covered == "0" || covered == "" {
		t.Fatalf("what a subscription covers is shown apart: %s", page["b1"])
	}
	var models struct {
		Models []struct {
			Name         string
			Subscription bool
		}
	}
	e.Must("deck.models_list", nil, &models)
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
	f := harness.NewACP(t)
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	e.Must("deck.model_set", map[string]any{"connection": "acp", "model": "m", "name": "acp", "subscription": true, "input_usd": 1000, "output_usd": 1000}, nil)
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, harness.AcpModel,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	id := e.Run(e.AcpProject("paint", harness.ColorManifest), "work")
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the turn runs: %+v", got)
	}
	var p struct {
		CostUSD         *float64 `json:"cost_usd"`
		SubscriptionUSD *float64 `json:"subscription_usd"`
	}
	e.Must("deck.process_get", map[string]any{"process": id}, &p)
	if p.CostUSD != nil || p.SubscriptionUSD == nil || *p.SubscriptionUSD <= 0 {
		t.Fatalf("the turn cost nothing apart from the subscription, and shows the estimate: %+v", p)
	}
	var days []struct {
		Key             string
		CostUSD         float64 `json:"cost_usd"`
		SubscriptionUSD float64 `json:"subscription_usd"`
	}
	e.Must("deck.spend_daily", map[string]any{"by": "provider"}, &days)
	if len(days) != 1 || days[0].CostUSD != 0 || days[0].SubscriptionUSD <= 0 {
		t.Fatalf("the spend keeps the estimate out of the money: %+v", days)
	}
}

// TestAModelOfTheSettingsMayBeOnASubscription: the owner marks a model of a connection as paid by a subscription,
// and it keeps the mark when changed without it.
func TestAModelOfTheSettingsMayBeOnASubscription(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "zai", "api": "openai", "url": "http://127.0.0.1:1/v1", "key": "k"}, nil)
	var m struct{ Subscription bool }
	e.Must("deck.model_set", map[string]any{"connection": "zai", "model": "glm-5", "kind": "text", "subscription": true}, &m)
	if !m.Subscription {
		t.Fatalf("the model is on a subscription: %+v", m)
	}
	e.Must("deck.model_set", map[string]any{"connection": "zai", "model": "glm-5", "kind": "text", "input_usd": 1}, &m)
	if !m.Subscription {
		t.Fatalf("a change without the mark keeps it: %+v", m)
	}
}

// TestAModelNeverCalledSaysSo: a model of a connection no call reached yet says so in the table of models instead of
// an empty status.
func TestAModelNeverCalledSaysSo(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "zai", "api": "openai", "url": "http://127.0.0.1:1/v1", "key": "k"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "zai", "model": "glm-5", "kind": "text"}, nil)
	rows := e.AllModels(nil)
	for _, r := range rows {
		if r.Title == "zai/glm-5" {
			if r.Tone != "muted" || r.Details != "No work has gone to it yet" || r.Group != "zai" {
				t.Fatalf("a model never called says so, under its connection: %+v", r)
			}
			return
		}
	}
	t.Fatalf("no row of the model: %+v", rows)
}
