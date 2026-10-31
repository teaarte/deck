package runs

import (
	"context"
	"deck2/internal/app"
	"deck2/internal/events"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// held is an agent whose turns wait until the test lets one go.
func held(release chan struct{}) harness.Script {
	return func(ctx context.Context, c *harness.Call) provider.Result {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}
}

func states(e *harness.Env, ids []int64) string {
	e.T.Helper()
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, e.Proc(id).State)
	}
	return strings.Join(out, " ")
}

// TestAgentLimitHoldsTheFourthAgentUntilASlotFreesAndScriptsDoNotCount: three agent turns run at once by
// default; the rest wait for a slot, oldest first, without reaching the provider; scripts run beside them.
func TestAgentLimitHoldsTheFourthAgentUntilASlotFreesAndScriptsDoNotCount(t *testing.T) {
	t.Parallel()
	release := make(chan struct{}, 10)
	e := harness.New(t, harness.WithAgent(held(release)))
	p := e.Project("jobs", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	var ids []int64
	for range 3 {
		ids = append(ids, e.Run(p, "job"))
	}
	e.Until("three agents at their provider", func() bool { return len(e.Fake.Calls()) == 3 })
	for _, want := range []string{"running running running waiting_slot", "running running running waiting_slot waiting_slot"} {
		ids = append(ids, e.Run(p, "job"))
		e.Until(want, func() bool { return states(e, ids) == want })
	}
	var waits []string
	for _, it := range e.CockpitRunning() {
		if it.Text == "waits for a slot" {
			waits = append(waits, it.Meta)
		}
	}
	if len(waits) != 2 {
		t.Fatalf("running now says which wait for a slot: %v", e.CockpitRunning())
	}

	e.Project("tools", harness.ScriptManifest("take", "", ""))
	e.SaveScript("tools", "scripts/main.py", "import json\nprint(json.dumps({}))\n")
	if s := e.WaitState(e.Start("tools", "take", nil), "done", "error"); s.State != "done" {
		t.Fatalf("a script runs while every agent slot is taken: %+v", s)
	}

	release <- struct{}{}
	e.Until("the oldest waiting process to take the free slot", func() bool {
		st := strings.Fields(states(e, ids))
		return st[3] == "running" && st[4] == "waiting_slot" && strings.Count(states(e, ids[:3]), "done") == 1
	})
	stream := e.Events(t.Context())
	e.Must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "5"}, nil)
	harness.WaitEvent(t, stream, events.Event{Topic: "settings"})
	e.Until("a raised limit to run the last one", func() bool { return e.Proc(ids[4]).State == "running" })
	rows := harness.BlockData[[]harness.ScreenRow](t, e.SettingsBlock("Limits", nil)["rows"])
	if len(rows) != 2 || rows[1].Cells[1] != "5" || rows[1].Cells[2] != float64(3) {
		t.Fatalf("the limits show the limit and its default: %+v", rows)
	}

	e.Must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "1"}, nil)
	extra := e.Run(p, "job")
	e.Until("the new process to wait for a slot", func() bool { return e.Proc(extra).State == "waiting_slot" })
	e.Must("deck.process_stop", map[string]any{"process": extra}, nil)
	e.WaitState(extra, "stopped")
	for range 10 {
		release <- struct{}{}
	}
	for _, id := range ids {
		e.WaitState(id, "done")
	}
	if n := len(e.Fake.Calls()); n != 5 {
		t.Fatalf("a stopped process that waited for a slot never ran: %d calls", n)
	}
	e.Refused("deck.settings_set", map[string]any{"name": "agent_limit", "value": "21"}, 422, "1 to 20")
	e.Refused("deck.settings_set", map[string]any{"name": "confirm_hours", "value": ""}, 422, "give confirm_hours a value")
}

// TestAProcessWaitingForASlotStillWaitsAfterARestart: the wait is in the base, so a restarted deck runs no more
// agents than the limit and the waiting one starts when a slot frees.
func TestAProcessWaitingForASlotStillWaitsAfterARestart(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	hang := make(chan struct{})
	first := &harness.Fake{Script: held(hang)}
	clock := &harness.Clock{At: harness.T0}
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(first), harness.WithClock(clock))
	e.Must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "1"}, nil)
	p := e.Project("jobs", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	a, b := e.Run(p, "job"), e.Run(p, "job")
	e.Until("one running, one waiting", func() bool {
		s := states(e, []int64{a, b})
		return s == "running waiting_slot" || s == "waiting_slot running"
	})
	if e.Proc(a).State != "running" {
		a, b = b, a
	}

	release := make(chan struct{}, 4)
	second := &harness.Fake{Script: held(release)}
	e2 := e.Restart(harness.WithProvider(second))
	e2.Resume()
	e2.Until("the running one resumed", func() bool { return len(second.Calls()) == 1 })
	if got := states(e2, []int64{a, b}); got != "running waiting_slot" || len(second.Calls()) != 1 || !second.Calls()[0].Resume {
		t.Fatalf("after a restart the limit holds and the running one goes on: %s, %d calls", got, len(second.Calls()))
	}
	release <- struct{}{}
	e2.WaitState(a, "done")
	e2.Until("the waiting one to start", func() bool { return e2.Proc(b).State == "running" })
	release <- struct{}{}
	e2.WaitState(b, "done")
	if n := len(second.Calls()); n != 2 {
		t.Fatalf("each of the two reached its provider once: %d calls", n)
	}
}

// TestConfirmationTermIsASettingOfTheWorkspace: a confirmation made after the change expires by the new term.
func TestConfirmationTermIsASettingOfTheWorkspace(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		c.Approve(ctx, "WebFetch", map[string]any{"url": "https://example.com/page", "prompt": "read it"})
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}), harness.WithClock(), harness.WithConfig(

		func(c *app.Config) { c.ConfirmTTL = 24 * time.Hour }))

	e.Must("deck.settings_set", map[string]any{"name": "confirm_hours", "value": "2"}, nil)
	p := e.Project("jobs", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	e.Run(p, "job")
	var asks []struct {
		ID        int64
		ExpiresAt string `json:"expires_at"`
	}
	e.Until("the confirmation", func() bool {
		e.Must("deck.asks_list", nil, &asks)
		return len(asks) == 1
	})
	t.Cleanup(func() { e.Call("deck.ask_answer", map[string]any{"ask": asks[0].ID, "answer": "deny"}) })
	if want := harness.T0.Add(2 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z"); asks[0].ExpiresAt != want {
		t.Fatalf("the confirmation expires by the term the owner set: %+v, want %s", asks, want)
	}
	rows := harness.BlockData[[]harness.ScreenRow](t, e.SettingsBlock("Limits", nil)["rows"])
	if rows[0].Cells[1] != "2" || rows[0].Cells[2] != float64(24) {
		t.Fatalf("the limits show the term and its default: %+v", rows)
	}
}

// TestAProcessLeftWaitingForAFreeSlotStartsWhenDeckStarts: deck stopped after a slot freed and before it was
// handed over; the next start hands it over.
func TestAProcessLeftWaitingForAFreeSlotStartsWhenDeckStarts(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	clock := &harness.Clock{At: harness.T0}
	first := &harness.Fake{Script: held(make(chan struct{}))}
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(first), harness.WithClock(clock))
	e.Must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "1"}, nil)
	p := e.Project("jobs", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	a, b := e.Run(p, "job"), e.Run(p, "job")
	e.Until("one running, one waiting", func() bool { return states(e, []int64{a, b}) == "running waiting_slot" })
	// A process that finished as deck stopped, before its slot went to the next.
	e.SQL(`UPDATE process SET state = 'done', result = '{"n": 1}' WHERE id = ?`, a)
	release := make(chan struct{}, 1)
	second := &harness.Fake{Script: held(release)}
	e2 := e.Restart(harness.WithProvider(second))
	e2.Resume()
	e2.Until("the waiting process to start on the free slot", func() bool { return e2.Proc(b).State == "running" })
	release <- struct{}{}
	e2.WaitState(b, "done")
}

// TestASlotFreedWhileDeckStopsLeavesTheWaitingProcessWaiting: a slot handed over after deck began to stop starts
// no turn, so the process stays waiting for a slot instead of passing for one that runs.
func TestASlotFreedWhileDeckStopsLeavesTheWaitingProcessWaiting(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(held(make(chan struct{}))))
	e.Must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "1"}, nil)
	p := e.Project("jobs", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	a, b := e.Run(p, "job"), e.Run(p, "job")
	either := func(got string) bool { return got == "running waiting_slot" || got == "waiting_slot running" }
	e.Until("one running, one waiting", func() bool { return either(states(e, []int64{a, b})) })
	e.App.Runner.Shutdown()
	if got := states(e, []int64{a, b}); !either(got) {
		t.Fatalf("the running one stays to resume and the waiting one still waits: %s", got)
	}
}

// TestAnAgentWaitingForAProcessLendsItsSlot: with one agent slot, an agent that waits in process_get for another agent
// lends its slot, so the other runs and the wait ends with it; a process waiting for a slot names how many are ahead.
func TestAnAgentWaitingForAProcessLendsItsSlot(t *testing.T) {
	t.Parallel()
	var job atomic.Int64
	start, got := make(chan struct{}), make(chan string, 1)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "boss" {
			select {
			case <-start:
			case <-ctx.Done():
				return provider.Result{}
			}
			out, _ := c.Tool(ctx, "process_get", map[string]any{"process": job.Load(), "wait": 600})
			got <- out
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))

	e.Must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "1"}, nil)
	p := e.Project("jobs", harness.TestManifest([]string{harness.AgentCommand("boss", ""), harness.AgentCommand("job", "")}, nil))
	boss := e.Run(p, "boss")
	e.Until("the boss to hold the slot", func() bool { return len(e.Fake.Calls()) == 1 })
	ids := []int64{e.Run(p, "job"), e.Run(p, "job")}
	job.Store(ids[0])
	e.Until("both jobs to wait for the slot", func() bool { return states(e, ids) == "waiting_slot waiting_slot" })
	var v struct {
		State     string
		SlotAhead *int64 `json:"slot_ahead"`
	}
	if e.Must("deck.process_get", map[string]any{"process": ids[1]}, &v); v.SlotAhead == nil || *v.SlotAhead != 1 {
		t.Fatalf("a process waiting for a slot names how many wait before it: %+v", v)
	}
	close(start)
	if out := harness.Receive(t, "the job waited for to take the slot the waiting agent holds", got); !strings.Contains(out, `"state":"done"`) {
		t.Fatalf("the wait ends with the process it waited for: %s", out)
	}
	for _, id := range append(ids, boss) {
		e.WaitState(id, "done")
	}
}
