package speed

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/tests/harness"
)

// budget is the median a read of the owner's pages may take on a large deck, measured on the fixed reads.
var budget = map[string]time.Duration{
	"today":          150 * time.Millisecond,
	"cockpit":        150 * time.Millisecond,
	"apps":           150 * time.Millisecond,
	"health_list":    60 * time.Millisecond,
	"triggers_list":  60 * time.Millisecond,
	"press to fresh": 600 * time.Millisecond,
}

// bigModule is a module of a large deck: two agent commands and one of a model, a collection and a schedule.
func bigModule(i int) string {
	return harness.Manifest(fmt.Sprintf(`{"collections": [{"name": "items", "description": "An item.", "key": "name", "display": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}, "n": {"type": "integer"}}, "required": ["name"]}}],
  "commands": [%s, %s,
    {"name": "sum", "title": "Sum", "description": "Sums the items up.", "input": {"type": "object", "properties": {}},
     "executor": {"type": "model", "kind": "text", "prompt": "Sum the items up."}}],
  "triggers": [{"command": "look", "cron": "%d 9 * * *"}],
  "screens": [{"name": "main", "title": "Items", "blocks": [{"type": "list", "title": "Items", "source": {"collection": "items"}, "item": {"title": "name"}}]}],
  "widgets": [{"min": "1x1", "blocks": [{"type": "stat", "title": "Items", "source": {"collection": "items"}}]}],
  "rights": [{"right": "money", "what": ["@text"], "why": "its summary is a model"}]}`,
		harness.AgentCommand("look", ""), harness.AgentCommand("tidy", ""), i))
}

// bigDeck is a deck of the size the owner's grew to: 15 modules of 45 agent and model commands, 34 models over four
// connections, 300 records and a module whose button asks the owner.
func bigDeck(t *testing.T) *harness.Env {
	e := harness.New(t)
	for _, c := range []string{"openrouter", "groq", "nvidia"} {
		e.Must("deck.connection_set", map[string]any{"name": c, "api": "openai", "url": "https://" + c + ".example/v1"}, nil)
		for i := range 10 {
			kind := "text"
			if i >= 8 {
				kind = "image"
			}
			e.Must("deck.model_set", map[string]any{"connection": c, "model": fmt.Sprintf("m%d", i), "kind": kind, "call_usd": 0.01}, nil)
		}
	}
	for i := range 4 {
		e.MustAgentModel(map[string]any{"name": fmt.Sprintf("acp%d", i), "url": "https://acp.example/v1", "model": "m", "key": "k", "agent": "goose"})
	}
	for i := range 15 {
		name := fmt.Sprintf("mod%d", i)
		e.Project(name, bigModule(i))
		var lines []string
		for j := range 20 {
			lines = append(lines, fmt.Sprintf(`{"name": "item%d", "n": %d}`, j, j))
		}
		e.Must("deck.record_upsert", map[string]any{"project": name, "collection": "items", "records": strings.Join(lines, "\n")}, nil)
		if i%3 == 0 {
			e.Must("deck.assignment_set", map[string]any{"role": "text", "project": name, "models": []string{"groq/m1", "openrouter/m2"}}, nil)
		}
	}
	e.Project("picker", harness.ScriptManifest("main", "", ""))
	e.SaveScript("picker", "scripts/main.py", harness.PressScript)
	return e
}

// median calls read n times and gives the median of their times; a failed read fails the test.
func median(t *testing.T, n int, read func() error) time.Duration {
	t.Helper()
	var ds []time.Duration
	for range n {
		at := time.Now()
		if err := read(); err != nil {
			t.Fatal(err)
		}
		ds = append(ds, time.Since(at))
	}
	slices.Sort(ds)
	return ds[len(ds)/2]
}

// call is a read of the owner that fails with its answer.
func call(e *harness.Env, name string, in map[string]any) error {
	if code, out := e.Call(name, in); code != 200 {
		return fmt.Errorf("%s: %d %s", name, code, out)
	}
	return nil
}

// TestTheOwnersPagesReadWithinTheirBudgetOnALargeDeck: the owner's pages of a deck of fifteen modules read within
// their budgets, and so does an answer with the reads it causes; four times as long under the race detector.
func TestTheOwnersPagesReadWithinTheirBudgetOnALargeDeck(t *testing.T) {
	e := bigDeck(t)
	reads := map[string]func() error{
		"today":   func() error { return call(e, "deck.screen_get", map[string]any{"screen": "today", "widget": "4x3"}) },
		"cockpit": func() error { return call(e, "deck.screen_get", map[string]any{"screen": "cockpit"}) },
		"apps": func() error {
			if err := call(e, "deck.menu_get", nil); err != nil {
				return err
			}
			return call(e, "deck.layout_get", map[string]any{"columns": 4})
		},
		"health_list":   func() error { return call(e, "deck.health_list", nil) },
		"triggers_list": func() error { return call(e, "deck.triggers_list", nil) },
	}
	got := map[string]time.Duration{}
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range reads {
			if !yield(k) {
				return
			}
		}
	}) {
		got[name] = median(t, 7, reads[name])
	}
	got["press to fresh"] = pressToFresh(t, e, 5)
	var over []string
	for _, name := range slices.Sorted(func(yield func(string) bool) {
		for k := range budget {
			if !yield(k) {
				return
			}
		}
	}) {
		t.Logf("%-15s %6d ms (budget %d ms)", name, got[name].Milliseconds(), harness.Within(budget[name]).Milliseconds())
		if got[name] > harness.Within(budget[name]) {
			over = append(over, name)
		}
	}
	if len(over) > 0 {
		t.Fatalf("over their budget: %s", strings.Join(over, ", "))
	}
}

// pressToFresh answers n asks of a button as the owner and gives the median time from the press until Today and the
// cockpit are read again with what changed: the client waits 200 ms for the events and reads each screen once.
func pressToFresh(t *testing.T, e *harness.Env, n int) time.Duration {
	t.Helper()
	var ds []time.Duration
	for i := range n {
		form, _ := json.Marshal(map[string]any{"role": "text", "project": "mod1", "models": []string{fmt.Sprintf("nvidia/m%d", i)}})
		run := e.Start("picker", "main", map[string]any{"method": "deck.assignment_set", "form": string(form)})
		var asks struct{ Asks []harness.PressAsk }
		e.Until("the ask of the button", func() bool {
			e.Must("deck.process_asks_list", map[string]any{"process": run}, &asks)
			return len(asks.Asks) == 1
		})
		ctx, cancel := context.WithCancel(context.Background())
		evs := e.Events(ctx)
		at := time.Now()
		if code, out := e.Press(asks.Asks[0].Allow); code != 200 {
			t.Fatalf("the press answers the ask: %d %s", code, out)
		}
		topics := map[string]bool{}
		quiet := time.NewTimer(200 * time.Millisecond)
	heard:
		for {
			select {
			case ev := <-evs:
				topics[ev.Topic] = true
				// The client reads once no event came for a while after the first.
				quiet.Reset(200 * time.Millisecond)
			case <-quiet.C:
				break heard
			}
		}
		cancel()
		changed := strings.Join(slices.Sorted(func(yield func(string) bool) {
			for k := range topics {
				if !yield(k) {
					return
				}
			}
		}), " ")
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, in := range []map[string]any{{"screen": "today", "widget": "4x3", "topic": changed}, {"screen": "cockpit", "topic": changed}} {
			wg.Go(func() { errs <- call(e, "deck.screen_get", in) })
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		ds = append(ds, time.Since(at))
		e.Until("the run of the button to end", func() bool {
			for _, p := range e.Procs() {
				if p.ID == run {
					return p.State != "running" && !strings.HasPrefix(p.State, "waiting")
				}
			}
			return false
		})
	}
	slices.Sort(ds)
	return ds[len(ds)/2]
}
