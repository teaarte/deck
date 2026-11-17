package speed

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/tests/harness"
)

// budget is what a read of the owner's pages may cost on a large deck: the statements it reads from the base, and the
// median of its time, only against the worst on a loaded machine (×4 under race): its numbers are in the log.
var budget = map[string]struct {
	reads int64
	time  time.Duration
}{
	"today":          {950, 2000 * time.Millisecond},
	"cockpit":        {470, 2000 * time.Millisecond},
	"apps":           {100, 1000 * time.Millisecond},
	"health_list":    {450, 1500 * time.Millisecond},
	"triggers_list":  {460, 1000 * time.Millisecond},
	"press to fresh": {1550, 5000 * time.Millisecond},
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
	// The agents of models run in the fake goose, so a machine without one has them too.
	e := harness.New(t, harness.WithACP(harness.NewACP(t), nil))
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

// cost is what read n times costs: the fewest statements of the base one read took, and the median of their times.
func cost(t *testing.T, e *harness.Env, n int, read func() error) (int64, time.Duration) {
	t.Helper()
	var ds []time.Duration
	least := int64(-1)
	for range n {
		at, was := time.Now(), e.App.Store.Reads()
		if err := read(); err != nil {
			t.Fatal(err)
		}
		ds = append(ds, time.Since(at))
		if r := e.App.Store.Reads() - was; least < 0 || r < least {
			least = r
		}
	}
	slices.Sort(ds)
	return least, ds[len(ds)/2]
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
	type spent struct {
		reads int64
		time  time.Duration
	}
	got := map[string]spent{}
	for _, name := range slices.Sorted(maps.Keys(reads)) {
		r, d := cost(t, e, 7, reads[name])
		got[name] = spent{r, d}
	}
	r, d := pressToFresh(t, e, 5)
	got["press to fresh"] = spent{r, d}
	var over []string
	for _, name := range slices.Sorted(maps.Keys(budget)) {
		b, g := budget[name], got[name]
		t.Logf("%-15s %5d reads (budget %d), %5d ms (budget %d ms)", name, g.reads, b.reads, g.time.Milliseconds(), harness.Within(b.time).Milliseconds())
		if g.reads > b.reads || g.time > harness.Within(b.time) {
			over = append(over, name)
		}
	}
	if len(over) > 0 {
		t.Fatalf("over their budget: %s", strings.Join(over, ", "))
	}
}

// pressToFresh answers n asks of a button as the owner and gives what it costs from the press until Today and the
// cockpit are read again with what changed: the client waits 200 ms for the events and reads each screen once.
func pressToFresh(t *testing.T, e *harness.Env, n int) (int64, time.Duration) {
	t.Helper()
	var ds []time.Duration
	least := int64(-1)
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
		at, was := time.Now(), e.App.Store.Reads()
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
		changed := strings.Join(slices.Sorted(maps.Keys(topics)), " ")
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
		if r := e.App.Store.Reads() - was; least < 0 || r < least {
			least = r
		}
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
	return least, ds[len(ds)/2]
}
