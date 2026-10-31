package scenarios

import (
	"deck2/tests/harness"
	"fmt"
	"os"
	"strings"
	"testing"
)

// TestRealBrowserTheTilesStandOnTheWayBack: back from a module the tiles stand as they were at once, nothing in them
// moves or comes in; a card of another size settles softly.
func TestRealBrowserTheTilesStandOnTheWayBack(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := harness.New(t, harness.WithAgent(harness.Ok))
	for i := range 3 {
		name := fmt.Sprintf("lab%d", i)
		e.Project(name, strings.Replace(widgetsOfWork, `"screens": [`, `"screens": [{"name": "main", "title": "Main", "blocks": [{"type": "table", "source": {"collection": "items"}, "columns": [{"label": "Name", "bind": "name"}]}]}, `, 1))
		e.Must("deck.records_upsert", map[string]any{"project": name, "collection": "items", "records": harness.Jsonl(map[string]any{"name": "one", "n": 1})}, nil)
	}
	b := newPlaywright(t)
	// back goes back to the tiles and watches them for a second: they stand in the first frames the address is theirs,
	// then no card moves and nothing in them plays but Deki at rest; it says what went otherwise.
	const back = `new Promise((done) => {
		const at = () => [...document.querySelectorAll('[data-apps] [data-cell]')].map((c) => { const r = c.getBoundingClientRect(); return c.dataset.cell + ':' + Math.round(r.x) + ',' + Math.round(r.y) + ',' + Math.round(r.width); }).join(' ');
		const playing = () => document.getAnimations().filter((a) => a.playState === 'running' && a.effect?.target instanceof Element && a.effect.target.closest('[data-apps]') && !a.effect.target.closest('[data-pose]'))
			.map((a) => a.animationName || a.transitionProperty || 'animation');
		const why = new Set(); let since = -1, first = '';
		const t0 = performance.now();
		const step = () => {
			const now = at();
			if (location.hash === '#/' || location.hash === '') {
				since++;
				if (since > 3 && !first) why.add('no tiles in the first frames');
				if (now && !first) first = now;
				if (first && now !== first) why.add('moved: ' + now);
				if (first) playing().forEach((a) => why.add('plays ' + a));
			}
			if (performance.now() - t0 < 1000) requestAnimationFrame(step); else done(why.size ? JSON.stringify([...why].slice(0, 8)) : 'true');
		};
		history.back();
		requestAnimationFrame(step);
	})`
	for _, size := range []struct{ w, h int }{{1400, 1000}, {390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.w, "height": size.h})
		b.Open(e, "#/")
		b.until("the cards", `document.querySelector('[data-cell="today"] [data-widget]') && document.querySelector('[data-cols]')`)
		cols := int64(colsOf(b))
		// The owner's own layout: the modules first, Today after them.
		place(e, b.eval(`localStorage.getItem('deck-device')`), int(cols), harness.DeskCard{ID: "lab2", X: 0, Y: 0, W: 1, H: 1}, harness.DeskCard{ID: "lab1", X: 0, Y: 1, W: 1, H: 1},
			harness.DeskCard{ID: "lab0", X: 0, Y: 2, W: 1, H: 1}, harness.DeskCard{ID: "today", X: 0, Y: 3, W: min(2, cols), H: 2})
		b.open(e.URL + "/?reload=" + fmt.Sprint(size.w) + "#/")
		b.until("the cards with their views where the owner put them", `document.querySelectorAll('[data-cell] [data-widget] [data-screen]').length >= 4 &&
			document.querySelector('[data-cell="lab2"]').getBoundingClientRect().y < document.querySelector('[data-cell="today"]').getBoundingClientRect().y`)
		b.eval(`(location.hash = '#/p/lab1/main', '')`)
		b.until("the screen of the module", `location.hash.includes('lab1') && document.querySelector('[data-screen-view] [data-screen]') && !document.querySelector('[data-apps]')`)
		if got := b.eval(back); got != "true" {
			t.Fatalf("the tiles stand on the way back at %dx%d: %s", size.w, size.h, got)
		}
		b.shot(fmt.Sprintf("tiles-back-%d", size.w))
	}
}
