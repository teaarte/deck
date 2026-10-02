package scenarios

import (
	"os"
	"strings"
	"testing"
	"time"
)

// TestRealBrowserCatalog draws the blocks of a forecast module in the real browser of Playwright MCP:
// DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowser -v.
func TestRealBrowserCatalog(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, nil)
	e.createProject("btc", forecastManifest)
	made := time.Now().Add(-5 * time.Minute).UTC().Format(time.RFC3339)
	e.must("deck.records_upsert", map[string]any{"project": "btc", "collection": "prices",
		"records": jsonl(map[string]any{"at": "2026-10-02T10:00:00Z", "price": 62340.25})}, nil)
	e.must("deck.records_upsert", map[string]any{"project": "btc", "collection": "forecasts",
		"records": jsonl(map[string]any{"at": made, "horizon": "7d", "change": 0.042, "confidence": 0.6, "status": "open", "source": "https://example.com/a"},
			map[string]any{"at": "2026-09-01T09:00:00Z", "horizon": "1d", "change": -0.01, "confidence": 0.3, "status": "missed"})}, nil)

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1280, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/p/btc/main")
	b.until("the forecast screen", `document.querySelector('[data-stat]') && document.querySelector('[data-format="relative"]')`)
	for what, cond := range map[string]string{
		"money in the locale of the browser":  `document.querySelector('[data-stat]').innerText === new Intl.NumberFormat(navigator.language, { style: 'currency', currency: 'USD' }).format(62340.25)`,
		"a percent of a fraction":             `document.querySelector('[data-format="percent"]').innerText === new Intl.NumberFormat(navigator.language, { style: 'percent', maximumFractionDigits: 1 }).format(0.042)`,
		"a time some minutes ago":             `document.querySelector('[data-format="relative"]').innerText === new Intl.RelativeTimeFormat(navigator.language, { numeric: 'auto' }).format(-5, 'minute')`,
		"a tag":                               `document.querySelector('[data-format="tag"]').innerText.toLowerCase() === 'open'`,
		"a link":                              `document.querySelector('a[href="https://example.com/a"]')`,
		"a stat in the color of its tone":     `getComputedStyle(document.querySelector('[data-stat] [data-tone="ok"]')).color !== getComputedStyle(document.querySelector('[data-stat]')).color`,
		"a fall in another color than a rise": `getComputedStyle(document.querySelector('td [data-tone="error"]')).color !== getComputedStyle(document.querySelector('td [data-tone="ok"]')).color`,
		"a warn tag":                          `document.querySelector('[data-format="tag"][data-tone="warn"]')`,
		"a muted line":                        `getComputedStyle(document.querySelector('[data-item][data-tone="muted"]')).opacity === '0.6'`,
	} {
		b.until(what, cond)
	}
	b.shot("catalog-format")
	b.eval(`[...document.querySelectorAll('td')].find(x => x.innerText === '7d').click(), ''`)
	b.until("the forecast page opened by a click on its row", `location.hash.startsWith('#/p/btc/forecast/') && document.body.innerText.includes('Still open')`)
	b.eval(`history.back(), ''`)
	b.until("the screen again", `document.querySelector('[data-opens]')`)
	b.eval(`document.querySelector('a[href="https://example.com/a"]').addEventListener('click', (e) => e.preventDefault()), ''`)
	b.eval(`document.querySelector('a[href="https://example.com/a"]').click(), ''`)
	time.Sleep(500 * time.Millisecond)
	if h := b.eval(`location.hash`); h != "#/p/btc/main" {
		t.Fatalf("a link in a row opens the link, not the row: %s", h)
	}

	e.createProject("fresh", strings.ReplaceAll(forecastManifest, `"title": "BTC"`, `"title": "Fresh"`))
	b.open(e.URL + "/?token=" + token + "#/p/fresh/main")
	b.until("the words of empty blocks", `[...document.querySelectorAll('[data-empty]')].map(x => x.innerText).join('|') === 'No prices yet|No forecasts yet|Nothing here'`)
	b.shot("catalog-empty")
	if b.eval(`String(document.body.innerText.includes('Missed'))`) != "false" {
		t.Fatal("a block whose when does not hold is not drawn")
	}
	e.must("deck.records_upsert", map[string]any{"project": "fresh", "collection": "forecasts",
		"records": jsonl(map[string]any{"at": "2026-09-01T09:00:00Z", "horizon": "1d", "change": -0.01, "status": "missed"})}, nil)
	b.until("the block its when now holds for, without a reload", `[...document.querySelectorAll('h4')].some(x => x.innerText === 'Missed')`)
}
