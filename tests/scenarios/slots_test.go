package scenarios

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
)

// held is an agent whose turns wait until the test lets one go.
func held(release chan struct{}) Script {
	return func(ctx context.Context, c *Call) provider.Result {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}
}

func (e *Env) states(ids []int64) string {
	e.T.Helper()
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, e.process(id).State)
	}
	return strings.Join(out, " ")
}

// TestAgentLimitHoldsTheFourthAgentUntilASlotFreesAndScriptsDoNotCount: three agent turns run at once by
// default; the rest wait for a slot, oldest first, without reaching the provider; scripts run beside them.
func TestAgentLimitHoldsTheFourthAgentUntilASlotFreesAndScriptsDoNotCount(t *testing.T) {
	t.Parallel()
	release := make(chan struct{}, 10)
	e := newEnv(t, held(release))
	p := e.createProject("jobs", testManifest([]string{agentCommand("job", "")}, nil))
	var ids []int64
	for range 3 {
		ids = append(ids, e.run(p, "job"))
	}
	e.waitFor("three agents at their provider", func() bool { return len(e.Fake.Calls()) == 3 })
	for _, want := range []string{"running running running waiting_slot", "running running running waiting_slot waiting_slot"} {
		ids = append(ids, e.run(p, "job"))
		e.waitFor(want, func() bool { return e.states(ids) == want })
	}
	var waits []string
	for _, it := range e.cockpitRunning() {
		if it.Text == "waits for a slot" {
			waits = append(waits, it.Meta)
		}
	}
	if len(waits) != 2 {
		t.Fatalf("running now says which wait for a slot: %v", e.cockpitRunning())
	}

	e.createProject("tools", scriptManifest("take", "", ""))
	e.saveScript("tools", "scripts/main.py", "import json\nprint(json.dumps({}))\n")
	if s := e.waitScript(e.start("tools", "take", nil), "done", "error"); s.State != "done" {
		t.Fatalf("a script runs while every agent slot is taken: %+v", s)
	}

	release <- struct{}{}
	e.waitFor("the oldest waiting process to take the free slot", func() bool {
		st := strings.Fields(e.states(ids))
		return st[3] == "running" && st[4] == "waiting_slot" && strings.Count(e.states(ids[:3]), "done") == 1
	})
	events := e.sse(t.Context())
	e.must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "5"}, nil)
	waitTopic(t, events, "settings")
	e.waitFor("a raised limit to run the last one", func() bool { return e.process(ids[4]).State == "running" })
	rows := blockData[[]screenRow](t, e.settingsBlock("General", nil)["rows"])
	if len(rows) != 3 || rows[1].Cells[1] != float64(5) || rows[1].Cells[2] != float64(3) {
		t.Fatalf("the general tab shows the limit and its default: %+v", rows)
	}

	e.must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "1"}, nil)
	extra := e.run(p, "job")
	e.waitFor("the new process to wait for a slot", func() bool { return e.process(extra).State == "waiting_slot" })
	e.must("deck.process_stop", map[string]any{"process": extra}, nil)
	e.waitState(extra, "stopped")
	for range 10 {
		release <- struct{}{}
	}
	for _, id := range ids {
		e.waitState(id, "done")
	}
	if n := len(e.Fake.Calls()); n != 5 {
		t.Fatalf("a stopped process that waited for a slot never ran: %d calls", n)
	}
	e.refused("deck.settings_set", map[string]any{"name": "agent_limit", "value": "21"}, 422, "1 to 20")
	e.refused("deck.settings_set", map[string]any{"name": "confirm_hours", "value": ""}, 422, "give confirm_hours a value")
}

// TestAProcessWaitingForASlotStillWaitsAfterARestart: the wait is in the base, so a restarted deck runs no more
// agents than the limit and the waiting one starts when a slot frees.
func TestAProcessWaitingForASlotStillWaitsAfterARestart(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	hang := make(chan struct{})
	first := &Fake{script: held(hang)}
	clock := &Clock{now: t0}
	e := newEnvWith(t, home, first, withClock(clock))
	e.Fake, e.Clock = first, clock
	e.must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "1"}, nil)
	p := e.createProject("jobs", testManifest([]string{agentCommand("job", "")}, nil))
	a, b := e.run(p, "job"), e.run(p, "job")
	e.waitFor("one running, one waiting", func() bool {
		s := e.states([]int64{a, b})
		return s == "running waiting_slot" || s == "waiting_slot running"
	})
	if e.process(a).State != "running" {
		a, b = b, a
	}

	release := make(chan struct{}, 4)
	second := &Fake{script: held(release)}
	e2 := restart(t, e, home, second)
	e2.waitFor("the running one resumed", func() bool { return len(second.Calls()) == 1 })
	if got := e2.states([]int64{a, b}); got != "running waiting_slot" || len(second.Calls()) != 1 || !second.Calls()[0].Resume {
		t.Fatalf("after a restart the limit holds and the running one goes on: %s, %d calls", got, len(second.Calls()))
	}
	release <- struct{}{}
	e2.waitState(a, "done")
	e2.waitFor("the waiting one to start", func() bool { return e2.process(b).State == "running" })
	release <- struct{}{}
	e2.waitState(b, "done")
	if n := len(second.Calls()); n != 2 {
		t.Fatalf("each of the two reached its provider once: %d calls", n)
	}
}

// TestConfirmationTermIsASettingOfTheWorkspace: a confirmation made after the change expires by the new term.
func TestConfirmationTermIsASettingOfTheWorkspace(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		c.Approve(ctx, "WebFetch", map[string]any{"url": "https://example.com/page", "prompt": "read it"})
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}, func(c *app.Config) { c.ConfirmTTL = 24 * time.Hour })
	e.must("deck.settings_set", map[string]any{"name": "confirm_hours", "value": "2"}, nil)
	p := e.createProject("jobs", testManifest([]string{agentCommand("job", "")}, nil))
	e.run(p, "job")
	var asks []struct {
		ID        int64
		ExpiresAt string `json:"expires_at"`
	}
	e.waitFor("the confirmation", func() bool {
		e.must("deck.asks_list", nil, &asks)
		return len(asks) == 1
	})
	t.Cleanup(func() { e.call("deck.ask_answer", map[string]any{"ask": asks[0].ID, "answer": "deny"}) })
	if want := t0.Add(2 * time.Hour).UTC().Format("2006-01-02T15:04:05.000Z"); asks[0].ExpiresAt != want {
		t.Fatalf("the confirmation expires by the term the owner set: %+v, want %s", asks, want)
	}
	rows := blockData[[]screenRow](t, e.settingsBlock("General", nil)["rows"])
	if rows[0].Cells[1] != float64(2) || rows[0].Cells[2] != float64(24) {
		t.Fatalf("the general tab shows the term and its default: %+v", rows)
	}
}

// TestAProcessLeftWaitingForAFreeSlotStartsWhenDeckStarts: deck stopped after a slot freed and before it was
// handed over; the next start hands it over.
func TestAProcessLeftWaitingForAFreeSlotStartsWhenDeckStarts(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	clock := &Clock{now: t0}
	first := &Fake{script: held(make(chan struct{}))}
	e := newEnvWith(t, home, first, withClock(clock))
	e.Fake, e.Clock = first, clock
	e.must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "1"}, nil)
	p := e.createProject("jobs", testManifest([]string{agentCommand("job", "")}, nil))
	a, b := e.run(p, "job"), e.run(p, "job")
	e.waitFor("one running, one waiting", func() bool { return e.states([]int64{a, b}) == "running waiting_slot" })
	if _, err := e.App.Store.DB.Exec(`UPDATE process SET state = 'done', result = '{"n": 1}' WHERE id = ?`, a); err != nil {
		t.Fatal(err)
	}
	release := make(chan struct{}, 1)
	second := &Fake{script: held(release)}
	e2 := restart(t, e, home, second)
	e2.waitFor("the waiting process to start on the free slot", func() bool { return e2.process(b).State == "running" })
	release <- struct{}{}
	e2.waitState(b, "done")
}

// TestASlotFreedWhileDeckStopsLeavesTheWaitingProcessWaiting: a slot handed over after deck began to stop starts
// no turn, so the process stays waiting for a slot instead of passing for one that runs.
func TestASlotFreedWhileDeckStopsLeavesTheWaitingProcessWaiting(t *testing.T) {
	t.Parallel()
	e := newEnv(t, held(make(chan struct{})))
	e.must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "1"}, nil)
	p := e.createProject("jobs", testManifest([]string{agentCommand("job", "")}, nil))
	a, b := e.run(p, "job"), e.run(p, "job")
	either := func(got string) bool { return got == "running waiting_slot" || got == "waiting_slot running" }
	e.waitFor("one running, one waiting", func() bool { return either(e.states([]int64{a, b})) })
	e.App.Runner.Shutdown()
	if got := e.states([]int64{a, b}); !either(got) {
		t.Fatalf("the running one stays to resume and the waiting one still waits: %s", got)
	}
}

// TestAnAgentWaitingForAProcessLendsItsSlot: with one agent slot, an agent that waits in process_get for another agent
// lends its slot, so the other runs and the wait ends with it; a process waiting for a slot names how many are ahead.
func TestAnAgentWaitingForAProcessLendsItsSlot(t *testing.T) {
	t.Parallel()
	var job atomic.Int64
	start, got := make(chan struct{}), make(chan string, 1)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "boss" {
			<-start
			out, _ := c.Tool(ctx, "process_get", map[string]any{"process": job.Load(), "wait": 600})
			got <- out
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	e.must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "1"}, nil)
	p := e.createProject("jobs", testManifest([]string{agentCommand("boss", ""), agentCommand("job", "")}, nil))
	boss := e.run(p, "boss")
	e.waitState(boss, "running")
	ids := []int64{e.run(p, "job"), e.run(p, "job")}
	job.Store(ids[0])
	e.waitFor("both jobs to wait for the slot", func() bool { return e.states(ids) == "waiting_slot waiting_slot" })
	var v struct {
		State     string
		SlotAhead *int64 `json:"slot_ahead"`
	}
	if e.must("deck.process_get", map[string]any{"process": ids[1]}, &v); v.SlotAhead == nil || *v.SlotAhead != 1 {
		t.Fatalf("a process waiting for a slot names how many wait before it: %+v", v)
	}
	close(start)
	select {
	case out := <-got:
		if !strings.Contains(out, `"state":"done"`) {
			t.Fatalf("the wait ends with the process it waited for: %s", out)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the job waited for never took the slot the waiting agent holds")
	}
	for _, id := range append(ids, boss) {
		e.waitState(id, "done")
	}
}
