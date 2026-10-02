package scenarios

import (
	"os"
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
		"records": jsonl(map[string]any{"at": made, "horizon": "7d", "change": 0.042, "confidence": 0.6, "status": "open", "source": "https://example.com/a"})}, nil)

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1280, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/p/btc/main")
	b.until("the forecast screen", `document.querySelector('[data-stat]') && document.querySelector('[data-format="relative"]')`)
	b.shot("catalog-format")
	for what, cond := range map[string]string{
		"money in the locale of the browser": `document.querySelector('[data-stat]').innerText === new Intl.NumberFormat(navigator.language, { style: 'currency', currency: 'USD' }).format(62340.25)`,
		"a percent of a fraction":            `document.querySelector('[data-format="percent"]').innerText === new Intl.NumberFormat(navigator.language, { style: 'percent', maximumFractionDigits: 1 }).format(0.042)`,
		"a time some minutes ago":            `document.querySelector('[data-format="relative"]').innerText === new Intl.RelativeTimeFormat(navigator.language, { numeric: 'auto' }).format(-5, 'minute')`,
		"a tag":                              `document.querySelector('[data-format="tag"]').innerText.toLowerCase() === 'open'`,
		"a link":                             `document.querySelector('a[href="https://example.com/a"]')`,
	} {
		b.until(what, cond)
	}
}
