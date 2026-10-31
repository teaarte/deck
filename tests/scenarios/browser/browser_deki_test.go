package browser

import (
	"deck2/tests/harness"
	"os"
	"testing"
)

// TestRealBrowserDekiAndMotion: Deki greets, points at what waits, moves a few times, rests and wakes; a card grows
// into its module's screen and back, still with the motion off: DECK_REAL_BROWSER=1, -run this test.
func TestRealBrowserDekiAndMotion(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "kind": "text", "call_usd": 0.01}, nil)
	e.Project("shorts", harness.PlainManifest)
	e.Project("picker", harness.ScriptManifest("main", "", `, "screens": [{"name": "main", "title": "Picker", "blocks": [
    {"type": "actions", "title": "Run", "actions": [{"label": "Run", "command": "main", "params": {"method": "deck.models_list", "form": "{}"}}]}]}], "menu": ["main"]`))
	e.SaveScript("picker", "scripts/main.py", harness.DeckCallScript)

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.Open(e, "#/")
	deki := `document.querySelector('[data-card-deki] [data-pose]:not([data-leaving])')`
	b.until("Deki greeting on the first open of the device", deki+`?.dataset.pose === 'hello'`)
	b.until("Deki at rest after the greeting", `['chill', 'sleepy'].includes(`+deki+`?.dataset.pose)`)

	e.ScriptCall("picker", "deck.assignment_set", map[string]any{"role": "text", "project": "shorts", "models": []string{"openrouter/qwen"}})
	b.until("Deki pointing at the decision", deki+`?.dataset.pose === 'point' && `+deki+`.dataset.deki === 'point'`)
	b.shot("deki-point")
	b.until("the motion of the pose playing", deki+`.getAnimations().some((a) => a.playState === 'running')`)
	b.until("the motion of the pose played a few times and rests", deki+`.getAnimations().length === 0`)
	b.eval(`(window.img = ` + deki + `.querySelector('img'), document.querySelector('[data-card-deki]').parentElement.dispatchEvent(new PointerEvent('pointerenter')), '')`)
	b.until("Deki wakes as the pointer comes, the picture staying as it was", deki+`.getAnimations().some((a) => a.playState === 'running') && `+deki+`.querySelector('img') === window.img`)

	b.eval(`(() => { window.moves = []; const go = document.startViewTransition.bind(document);
		document.startViewTransition = (f) => { window.moves.push(document.querySelector('[data-module=picker]')?.style.viewTransitionName
			|| document.querySelector('[data-module-view]')?.style.viewTransitionName || ''); return go(f) }; return '' })()`)
	b.eval(`(document.querySelector('[data-app=picker]').click(), '')`)
	b.until("the module open", `location.hash.startsWith('#/p/picker/') && document.querySelector('[data-module-view]')`)
	b.until("the move over, the screen no longer named for it", `!document.querySelector('[data-module-view]').style.viewTransitionName`)
	b.eval(`(history.back(), '')`)
	b.until("the tiles again", `document.querySelector('[data-app=picker]')`)
	if got := b.eval(`window.moves.join(' ')`); got != "module module" {
		t.Fatalf("the card grows into the screen of its module and back: %q", got)
	}

	b.eval(`(localStorage.setItem('deck-look', JSON.stringify({ motion: false })), '')`)
	b.open(e.URL + "/?still=1#/")
	b.until("the tiles without motion", `document.documentElement.dataset.deckMotion === 'off' && document.querySelector('[data-app=picker]')`)
	b.eval(`(() => { window.moves = []; const go = document.startViewTransition.bind(document);
		document.startViewTransition = (f) => { window.moves.push('move'); return go(f) }; return '' })()`)
	b.eval(`(document.querySelector('[data-app=picker]').click(), '')`)
	b.until("the module open", `location.hash.startsWith('#/p/picker/') && document.querySelector('[data-module-view]')`)
	if got := b.eval(`String(window.moves.length) + ' ' + String(!!document.querySelector('[data-card-deki] [data-still]') || !document.querySelector('[data-card-deki]'))`); got != "0 true" {
		t.Fatalf("no move once the owner turned the motion off: %s", got)
	}
}
