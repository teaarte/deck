package scenarios

import (
	"context"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// TestRealBrowserTheSidePanelOfAScreen: on a computer the panel stands left of the blocks and a pick narrows them in
// place, kept in the address; on a phone a button in the row of the tabs opens it in a sheet that stays open.
func TestRealBrowserTheSidePanelOfAScreen(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := harness.New(t)
	e.Project("lab", strings.Replace(sideManifest, `"screens": [`, `"menu": ["lab", "all"], "screens": [{"name": "all", "title": "All",
    "blocks": [{"type": "stat", "title": "All", "source": {"collection": "trades"}}]}, `, 1))
	e.Must("deck.records_upsert", map[string]any{"project": "lab", "collection": "trades", "records": harness.Jsonl(
		map[string]any{"title": "a", "coin": "BTC", "side": "long"}, map[string]any{"title": "b", "coin": "ETH", "side": "short"},
		map[string]any{"title": "c", "coin": "BTC", "side": "short"})}, nil)

	b := newPlaywright(t)
	rows := `[...document.querySelectorAll('[data-grid] tbody tr')].filter((x) => x.offsetParent).length`
	pick := func(value string) {
		b.eval(`(document.querySelector('[data-side-filter="coin"]').click(), '')`)
		b.until("the coins offered", `[...document.querySelectorAll('[role=option]')].some((o) => o.innerText.trim() === '`+value+`')`)
		b.eval(`([...document.querySelectorAll('[role=option]')].find((o) => o.innerText.trim() === '` + value + `').click(), '')`)
	}

	b.call("browser_resize", map[string]any{"width": 1280, "height": 820})
	b.Open(e, "#/p/lab/lab")
	b.until("the panel beside the blocks", `(() => { const s = document.querySelector('[data-side]'), g = document.querySelector('[data-with-side] [data-grid]');
		return !!s && !!g && s.offsetParent && s.getBoundingClientRect().right <= g.getBoundingClientRect().left })()`)
	b.until("every trade", rows+` === 3`)
	b.eval(`(document.querySelector('[data-side]').dataset.mark = 'kept', '')`)
	pick("BTC")
	b.until("the pick in the address and the blocks narrowed", `location.hash.includes('coin=BTC') && `+rows+` === 2`)
	if got := b.eval(`String(document.querySelector('[data-side]')?.dataset.mark === 'kept')`); got != "true" {
		t.Fatal("a pick reads the screen again in place, the panel stays")
	}
	b.shot("side")
	b.open(e.URL + "/#/p/lab/lab?coin=ETH")
	b.until("a link with a pick opens the screen narrowed", rows+` === 1 && document.querySelector('[data-side-filter="coin"]').value === 'ETH'`)

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(e.URL + "/#/p/lab/lab")
	b.until("the button of the panel in the row of the tabs", `document.querySelector('[data-side-slot] [data-side-open]') && document.querySelector('[data-side-open]').getBoundingClientRect().right <= innerWidth && !document.querySelector('[data-side]')?.offsetParent`)
	b.until("every trade", rows+` === 3`)
	b.shot("side-phone")
	b.eval(`(document.querySelector('[data-side-open]').click(), '')`)
	b.until("the sheet with the filters", `document.querySelector('[data-side-sheet] [data-side-filter="coin"]')?.offsetParent`)
	pick("ETH")
	b.until("the pick narrows the blocks behind the sheet", `location.hash.includes('coin=ETH') && [...document.querySelectorAll('[data-grid] tbody tr')].length === 1`)
	b.until("the sheet stays open and counts the pick", `document.querySelector('[data-side-sheet] [data-side-filter="coin"]')?.offsetParent && document.querySelector('[data-side-open]').innerText.includes('1')`)
	b.shot("side-phone-sheet")
	if got := b.eval(`String(document.documentElement.scrollWidth <= innerWidth + 1)`); got != "true" {
		t.Fatal("the screen with its panel fits the phone")
	}
}

// TestRealBrowserAFilterStartsOnItsDefault: a filter whose param has a default starts on it; all is a pick of its own
// that the address keeps, so a reload stays on every row.
func TestRealBrowserAFilterStartsOnItsDefault(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := harness.New(t)
	e.Project("lab", strings.Replace(sideManifest, `"params": {"coin": "string", "side": "string"}`, `"params": {"coin": {"type": "string", "default": "BTC"}, "side": "string"}`, 1))
	e.Must("deck.records_upsert", map[string]any{"project": "lab", "collection": "trades", "records": harness.Jsonl(
		map[string]any{"title": "a", "coin": "BTC", "side": "long"}, map[string]any{"title": "b", "coin": "ETH", "side": "short"},
		map[string]any{"title": "c", "coin": "BTC", "side": "short"})}, nil)
	b := newPlaywright(t)
	rows := `[...document.querySelectorAll('[data-grid] tbody tr')].filter((x) => x.offsetParent).length`
	b.call("browser_resize", map[string]any{"width": 1280, "height": 820})
	b.Open(e, "#/p/lab/lab")
	b.until("the default picked", rows+` === 2 && document.querySelector('[data-side-filter="coin"]').value === 'BTC'`)
	b.eval(`(document.querySelector('[data-side-filter="coin"]').click(), '')`)
	b.until("all offered first", `[...document.querySelectorAll('[role=option]')][0]?.innerText.trim() === 'All'`)
	b.eval(`([...document.querySelectorAll('[role=option]')][0].click(), '')`)
	b.until("all picked, kept in the address", `/[?&]coin=(&|$)/.test(location.hash) && `+rows+` === 3 && document.querySelector('[data-side-filter="coin"]').value === 'All'`)
	b.shot("side-default-all")
	b.call("browser_navigate", map[string]any{"url": "about:blank"})
	b.open(e.URL + "/#/p/lab/lab?coin=")
	b.until("a reload stays on all", rows+` === 3 && document.querySelector('[data-side-filter="coin"]').value === 'All'`)
}

// TestRealBrowserAWorkTellsItsTopic: the line deck writes into a topic when a work started from it ends shows its
// result and opens its process.
func TestRealBrowserAWorkTellsItsTopic(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	var from atomic.Int64
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "work" {
			return provider.Result{Text: "Counted: **3 records**."}
		}
		return handOver(ctx, c, &from)
	}))

	topic := e.Say(0, "count the records")
	told(e, topic, "done", "")

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1280, "height": 820})
	b.Open(e, "#/deck/cockpit")
	b.until("the cockpit", `document.querySelector('[data-chat-toggle]')`)
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	told := `document.querySelector('[data-chat-panel] [data-told="done"]')`
	b.until("the line of deck with the result", told+`?.innerText.includes('Counted: 3 records') && `+told+`.innerText.includes('master.work')`)
	b.shot("chat-told")
	b.eval(`(` + told + `.querySelector('button').click(), '')`)
	b.until("the process of the work", `location.hash.includes('/deck/process/')`)
}
