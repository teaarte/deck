package browser

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"deck2/tests/harness"
)

// TestRealBrowserAScreenReadsItsChangesOneAtATime: changes heard during a read go after it in one read of all their
// topics; a press on what is over reads anew quietly, one that went shows its change at once; a stuck read says so.
func TestRealBrowserAScreenReadsItsChangesOneAtATime(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	for _, m := range []string{"qwen", "gemma", "llama", "phi"} {
		e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": m, "kind": "text", "call_usd": 0.01}, nil)
	}
	e.Project("films", harness.PlainManifest)
	e.Project("picker", harness.ScriptManifest("main", "", ""))
	e.SaveScript("picker", "scripts/main.py", harness.PressScript)
	ask := func(model string) {
		form, _ := json.Marshal(map[string]any{"role": "text", "project": "films", "models": []string{"openrouter/" + model}})
		e.Start("picker", "main", map[string]any{"method": "deck.assignment_set", "form": string(form)})
	}

	front := newFront(t, e)
	b := Open(t)
	b.call("browser_resize", map[string]any{"width": 1280, "height": 900})
	b.OpenVia(front.URL, e, "#/deck/cockpit")
	b.until("the cockpit with nothing to decide", `document.querySelector('[data-focus-done]')`)
	for _, m := range []string{"qwen", "gemma", "llama"} {
		ask(m)
	}
	e.Until("three asks", func() bool { return len(e.CockpitItems()) == 3 })
	b.until("the three decisions", `document.querySelector('[data-focus]')?.dataset.left === '3'`)
	if most, joined := front.most.Load(), front.joined.Load(); most != 1 || joined == 0 {
		t.Fatalf("a screen reads its changes one read at a time, the topics heard meanwhile together: %d at once, %d joined", most, joined)
	}

	// The page hears nothing while deck answers every ask; its card stays as it was.
	front.cut()
	for _, it := range e.CockpitItems() {
		e.Must("deck.ask_answer", map[string]any{"ask": it.ID, "answer": "deny"}, nil)
	}
	full := front.full.Load()
	b.eval(`([...document.querySelector('[data-focus-card]').querySelectorAll('button')].find(x => x.innerText.startsWith('Allow once')).click(), '')`)
	b.until("the screen read anew after the press on what was answered", `document.querySelector('[data-focus-done]')`)
	if front.full.Load() == full {
		t.Fatal("a press on an ask answered already reads the screen whole")
	}
	if got := b.eval(`document.body.innerText`); strings.Contains(got, "already answered") {
		t.Fatalf("a press on an ask answered already tells nothing: %q", got)
	}
	b.shot("rereads-gone")

	front.heal()
	b.until("the stale mark to clear", `!document.querySelector('[data-stale]')`)
	ask("phi")
	b.until("the new decision", `document.querySelector('[data-focus]')?.dataset.left === '1'`)
	answers := front.answers.Load()
	b.eval(`([...document.querySelector('[data-focus-card]').querySelectorAll('button')].find(x => x.innerText.startsWith('Allow once')).click(), '')`)
	e.Until("the answer once its moment passed", func() bool { return front.answers.Load() > answers })
	at := time.Now()
	b.until("the decision gone", `document.querySelector('[data-focus-done]')`)
	if d := time.Since(at); d > harness.Within(2*time.Second) {
		t.Fatalf("what a press changed shows at once after deck answered it: %s", d)
	}
	if got := e.Assigned("films", "text"); len(got) != 1 || got[0] != "openrouter/phi" {
		t.Fatalf("the press allowed the call: %v", got)
	}

	front.fail.Store(true)
	b.open(front.URL + "/#/deck/spend")
	b.until("the screen that failed, with a retry", `document.querySelector('[data-screen-error] [data-screen-retry]')`)
	front.fail.Store(false)
	b.eval(`(document.querySelector('[data-screen-retry]').click(), '')`)
	b.until("the screen after the retry", `document.querySelector('[data-screen-view]') && !document.querySelector('[data-screen-error]')`)

	front.stall.Store(true)
	b.open(front.URL + "/#/deck/schedule")
	b.until("the screen that is slow says so", `document.querySelector('[data-screen-slow] [data-screen-retry]')`)
	b.shot("rereads-slow")
	front.stall.Store(false)
	b.eval(`(document.querySelector('[data-screen-retry]').click(), '')`)
	b.until("the screen after the retry", `document.body.innerText.includes('What runs by itself') && !document.querySelector('[data-screen-slow]')`)
}
