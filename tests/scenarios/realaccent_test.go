package scenarios

import (
	"os"
	"strings"
	"testing"
)

// filledShown counts the filled buttons a person sees in the window.
const filledShown = `String([...document.querySelectorAll('.mantine-Button-root[data-variant="filled"]')].filter((x) => { const r = x.getBoundingClientRect();
	return x.checkVisibility() && r.width > 0 && r.bottom > 0 && r.top < innerHeight }).length)`

// TestRealBrowserOneAccent: one filled button at most on the cockpit and the chat, the state in words on a card, clouds
// only in moments: DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowserOneAccent -v.
func TestRealBrowserOneAccent(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, ok)
	e.createProject("web", webNotes)
	for _, name := range []string{"first", "second"} {
		_, tok := e.token("ask-"+name, "deck.model_set")
		if code, out := e.callWith(tok, "deck.model_set", map[string]any{"connection": "none", "model": name}); code != 200 || !strings.Contains(string(out), `"waiting"`) {
			t.Fatalf("a token's model waits for the owner: %d %s", code, out)
		}
	}
	b := newPlaywright(t)
	for _, size := range []struct {
		name          string
		width, height int
	}{{"desktop", 1400, 900}, {"phone", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.width, "height": size.height})
		b.open(e.URL + "/?token=" + token + "&theme=juce&scheme=light#/deck/cockpit")
		b.until("two decisions on the cockpit", `document.querySelectorAll('[data-verb="allow"]').length >= 2`)
		if got := b.eval(filledShown); got != "1" {
			t.Fatalf("the cockpit fills its first decision alone on a %s: %s", size.name, got)
		}
		b.until("the clouds of juce on the cockpit", `document.querySelectorAll('[data-backdrop] [data-cloud]').length === 2`)
		b.shot("accent-cockpit-" + size.name)
		b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
		b.until("the chat open", `document.querySelector('[data-chat-input]')?.checkVisibility()`)
		if got := b.eval(filledShown); got != "0" && got != "1" {
			t.Fatalf("the open chat leaves one filled button at most on a %s: %s", size.name, got)
		}
		b.shot("accent-chat-" + size.name)
		b.eval(`(document.querySelector('[data-chat-close]').click(), '')`)
		b.until("the chat closed", `!document.querySelector('[data-chat-input]')?.checkVisibility()`)

		b.open(e.URL + "/?theme=juce&scheme=light#/p/web/main")
		b.until("the screen of the module", `location.hash.startsWith('#/p/web/main') && document.querySelector('[data-block]')`)
		if got := b.eval(`String(document.querySelectorAll('[data-backdrop] [data-cloud]').length)`); got != "0" {
			t.Fatalf("no clouds on a working screen on a %s: %s", size.name, got)
		}
	}
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.open(e.URL + "/?theme=juce&scheme=light#/deck/apps")
	b.until("the state in words on the cockpit's card", `[...document.querySelectorAll('[data-apps] [data-status]')].some((x) => x.innerText.includes('Waiting for you: 2') && x.querySelector('[data-asks="2"]'))`)
	b.shot("accent-cards")
	b.open(e.URL + "/?theme=juce&scheme=light#/p/web/main")
	b.until("the master's button", `document.querySelector('[data-chat-toggle]')`)
	if got := b.eval(`(() => { const m = document.querySelector('[data-chat-toggle]'); return String((m.dataset.variant === 'filled') === !!m.querySelector('[data-master-asks]')) })()`); got != "true" {
		t.Fatalf("the master's button is filled only while its asks wait for the owner: %s", b.eval(`document.querySelector('[data-chat-toggle]').outerHTML`))
	}
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the clouds with the chat open", `document.querySelector('[data-chat-input]') && document.querySelectorAll('[data-backdrop] [data-cloud]').length === 2`)
	if got := b.eval(`document.querySelector('[data-chat-toggle]')?.dataset.variant ?? 'light'`); got != "light" {
		t.Fatalf("the master's button is light with the chat open: %s", got)
	}
}
