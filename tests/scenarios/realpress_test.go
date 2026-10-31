package scenarios

import (
	"deck2/tests/harness"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// slowApplyScript is applyScript that takes a moment first, as a real apply reads deck before it calls.
const slowApplyScript = "import time\ntime.sleep(1.5)\n" + applyScript

// TestRealBrowserTenQuickPressesEachGetAnAnswer: ten quick presses of Apply each go busy at once and make their call
// with nothing to answer, a press again while one goes says so: DECK_REAL_BROWSER=1, -run this test.
func TestRealBrowserTenQuickPressesEachGetAnAnswer(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	for _, m := range []string{"qwen", "gemma"} {
		e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": m, "kind": "text", "call_usd": 0.01}, nil)
	}
	e.Project("shorts", harness.PlainManifest)
	e.Project("picker", pickerManifest)
	e.SaveScript("picker", "scripts/apply.py", slowApplyScript)
	var rows []map[string]any
	for i := range 10 {
		rows = append(rows, map[string]any{"name": fmt.Sprint("p", i), "project": "shorts", "models": []string{"openrouter/qwen", "openrouter/gemma"}[i%2]})
	}
	e.Must("deck.records_upsert", map[string]any{"project": "picker", "collection": "proposals", "records": harness.Jsonl(rows...)}, nil)

	b := newPlaywright(t)
	applies := `[...document.querySelectorAll('[data-block] button')].filter((x) => x.innerText.trim() === 'Apply')`
	runs := func() int {
		var n int
		_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM process WHERE command = 'apply' AND trigger_id IS NULL AND parent_id IS NULL`).Scan(&n)
		return n
	}
	for i, size := range []struct {
		name          string
		width, height int
	}{{"desktop", 1400, 900}, {"phone", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.width, "height": size.height})
		b.Open(e, "#/p/picker/main")
		b.until("ten Apply buttons", applies+`.length === 10`)
		// A busy button hides its label, so the busy ones are counted as they go, from the press on.
		b.eval(`(() => { window.most = 0; const t0 = performance.now(); (function watch() {
			window.most = Math.max(window.most, document.querySelectorAll('button[data-loading]').length);
			if (performance.now() - t0 < 1000) requestAnimationFrame(watch) })();
			const bs = ` + applies + `; bs.forEach((x) => x.click()); bs[0].click(); return '' })()`)
		b.until("every pressed button busy at once", `window.most === 10`)
		b.until("the second press of a busy button said to be running already",
			`[...document.querySelectorAll('.mantine-Notification-root')].some((x) => x.innerText.includes('already running'))`)
		b.shot("ten-presses-" + size.name)
		want := 10 * (i + 1)
		e.Until("a run of every press", func() bool { return runs() == want })
		b.until("every press told how it went, nothing to answer over the screen", applies+`.length === 10 && `+applies+`.every((x) => x.dataset.loading === undefined) && !document.querySelector('[data-press-ask]')`)
		b.until("the ends of the runs told in one note", `[...document.querySelectorAll('.mantine-Notification-root')].some((x) => x.innerText.includes('Apply: done ×'))`)
		b.shot("ten-presses-done-" + size.name)
		var open int
		_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM ask WHERE state = 'open'`).Scan(&open)
		var unfinished int
		_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM process WHERE command = 'apply' AND state != 'done'`).Scan(&unfinished)
		if open != 0 || unfinished != 0 {
			t.Fatalf("each press made its call with no second yes on a %s: %d open asks, %d runs not done", size.name, open, unfinished)
		}
	}
}

// TestRealBrowserFallbacksOpenAtOnce: the dialog of the fallbacks of a work opens from the screen as it is, reading only
// the dialog, and closes without reading the screen again.
func TestRealBrowserFallbacksOpenAtOnce(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	for _, m := range []string{"qwen", "gemma"} {
		e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": m, "kind": "text", "call_usd": 0.01}, nil)
	}
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1280, "height": 900})
	b.Open(e, "#/deck/settings?tab=Models")
	b.until("the fallbacks of a work", `document.querySelector('[data-verb="fallbacks"]')`)
	b.eval(`(() => { window.reads = 0; const f = window.fetch;
		window.fetch = (u, o) => { if (String(u).includes('deck.screen') && !String(o?.body ?? '').includes('"block"')) window.reads++; return f(u, o) }; return '' })()`)
	b.eval(`(window.t1 = 0, window.t0 = performance.now(), document.querySelector('[data-verb="fallbacks"]').click(),
		(function wait() { if (document.querySelector('[data-form="deck.assignment_set"] [data-submit]')) window.t1 = performance.now(); else requestAnimationFrame(wait) })(), '')`)
	b.until("the dialog with its form", `window.t1 > 0`)
	ms, _ := strconv.Atoi(b.eval(`String(Math.round(window.t1 - window.t0))`))
	b.shot("fallbacks-open")
	if ms > 300 {
		t.Fatalf("the dialog of the fallbacks opens in %d ms; it must under 300", ms)
	}
	b.eval(`(document.querySelector('.mantine-Modal-close').click(), '')`)
	b.until("the dialog closed", `!document.querySelector('[data-form="deck.assignment_set"]')`)
	if got := b.eval(`String(window.reads) + ' ' + location.hash`); got != "0 #/deck/settings?tab=Models" {
		t.Fatalf("opening and closing the dialog reads no screen whole and leaves the address as it was: %s", got)
	}
}

// TestRealBrowserANewAskComesToTodayLive: a call that waits for the owner shows on the card of Today by the event of
// it, with no reload.
func TestRealBrowserANewAskComesToTodayLive(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "kind": "text", "call_usd": 0.01}, nil)
	e.Project("shorts", harness.PlainManifest)
	e.Project("picker", harness.ScriptManifest("main", "", ""))
	e.SaveScript("picker", "scripts/main.py", deckCallScript)
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1280, "height": 900})
	b.Open(e, "#/")
	b.until("the card of Today, calm", `document.body.innerText.includes('All is well')`)
	b.eval(`(window.seen = 0, (function wait() { if (document.body.innerText.includes('One decision waits')) window.seen = performance.now(); else requestAnimationFrame(wait) })(), '')`)
	scriptCall(e, "picker", "deck.assignment_set", map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/qwen"}})
	asked := time.Now()
	b.until("the decision on Today", `window.seen > 0`)
	if d := time.Since(asked); d > 3*time.Second {
		t.Fatalf("the decision came to Today %v after it was asked", d)
	}
	b.shot("today-live-ask")
	if got := b.eval(`String(performance.getEntriesByType('navigation').length)`); !strings.HasPrefix(got, "1") {
		t.Fatalf("the page was not reloaded: %s", got)
	}
}
