package scenarios

import (
	"context"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRealBrowserAWorkToldInItsTopic: a work of the master that failed says so in its topic with a button that goes
// on; gone on, its long result comes whole, folded with a button that shows all, on a computer and a phone.
func TestRealBrowserAWorkToldInItsTopic(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	long := strings.Repeat("Шаг сделан, одна фраза — одно дело. ", 90) + "Что заводить первым?"
	var from atomic.Int64
	var works atomic.Int32
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch {
		case strings.Contains(c.Req.Prompt, "go on where you stopped"):
			return provider.Result{Text: long}
		case c.Command() != "work":
			return handOver(ctx, c, &from)
		case works.Add(1) == 1:
			return provider.Result{Outcome: provider.Failed, Text: "the site is down"}
		}
		return provider.Result{Text: "a second fresh run"}
	}))

	e.Must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
	e.Project("plain", harness.PlainManifest)
	topic := e.Say(0, "сделай")
	told(e, topic, "error", "ru")
	b := newPlaywright(t)
	open := func(phone bool) {
		if phone {
			b.call("browser_resize", map[string]any{"width": 390, "height": 844})
		} else {
			b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
		}
		b.Open(e, "#/deck/cockpit")
		b.until("the cockpit or its chat", `document.querySelector('[data-chat-toggle], [data-told]')`)
		time.Sleep(500 * time.Millisecond)
		b.eval(`(document.querySelector('[data-told]')?.offsetParent || document.querySelector('[data-chat-toggle]').click(), '')`)
	}
	for _, phone := range []bool{false, true} {
		open(phone)
		b.until("the failed work with its button that goes on", `[...document.querySelectorAll('[data-told="error"] button')].some((x) => x.innerText.trim() === 'Продолжить')`)
		b.shot("told-failed-" + map[bool]string{false: "computer", true: "phone"}[phone])
	}
	b.eval(`([...document.querySelectorAll('[data-told="error"] button')].find((x) => x.innerText.trim() === 'Продолжить').click(), '')`)
	for _, phone := range []bool{true, false} {
		if !phone {
			open(phone)
		}
		look := map[bool]string{false: "computer", true: "phone"}[phone]
		b.until("the whole result, folded", `document.querySelector('[data-told="done"] [data-told-result]')?.innerText.includes('Что заводить первым?')`)
		if got := b.eval(`(() => { const r = document.querySelector('[data-told="done"] [data-told-result]'); return r.getBoundingClientRect().height < 480 && [...r.querySelectorAll('button')].some((x) => x.innerText.trim() === 'Показать всё') ? 'folded' : r.innerText.slice(-200); })()`); got != "folded" {
			t.Fatalf("a long result on a %s is folded with a button that shows all: %s", look, got)
		}
		b.shot("told-folded-" + look)
		b.eval(`([...document.querySelectorAll('[data-told="done"] [data-told-result] button')].find((x) => x.innerText.trim() === 'Показать всё').click(), '')`)
		b.until("the whole result shown", `document.querySelector('[data-told="done"] [data-told-result]').getBoundingClientRect().height > 600`)
		b.shot("told-whole-" + look)
		if !phone {
			break
		}
	}
	if works.Load() != 1 {
		t.Fatalf("the work went on in its session, not afresh: %d fresh runs", works.Load())
	}
}
