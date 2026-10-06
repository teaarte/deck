package scenarios

import (
	"os"
	"testing"
)

// TestRealBrowserRouter: a button opens a dialog with its form filled from the address, back closes it, a tab
// lives in the address and back returns to it: DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowserRouter -v.
func TestRealBrowserRouter(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, webNotesAgent())
	e.createProject("web", routedManifest)
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": jsonl(map[string]any{"title": "alpha"})}, nil)
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/p/web/main")
	b.until("the screen", `document.querySelector('[data-dialog-open="rescore"]') && document.querySelector('[data-verb], button')`)
	if got := b.eval(`String(!!document.querySelector('[data-back]'))`); got != "false" {
		t.Fatalf("no back button on the first screen: %s", got)
	}
	b.eval(`([...document.querySelectorAll('button')].find((x) => x.innerText.trim() === 'Again').click(), '')`)
	b.until("the dialog with its form filled from the row", `location.hash.includes('dialog=rescore') && document.querySelector('[data-dialog="rescore"] input[data-field="title"]')?.value === 'alpha'`)
	b.shot("router-dialog")
	b.eval(`(document.querySelector('[data-back]').click(), '')`)
	b.until("back closes the dialog", `!document.querySelector('[data-dialog="rescore"] input') && !location.hash.includes('dialog')`)
	b.eval(`(document.querySelector('[data-dialog-open="rescore"]').click(), '')`)
	b.until("the dialog by its button, empty", `document.querySelector('[data-dialog="rescore"] input[data-field="title"]')?.value === ''`)
	b.eval(`(document.querySelector('[data-dialog="rescore"] input[data-field="title"]').focus(), '')`)
	b.type_(`document.querySelector('[data-dialog="rescore"] input[data-field="title"]')`, "beta")
	b.eval(`([...document.querySelectorAll('[data-dialog="rescore"] button')].find((x) => x.innerText.trim() === 'Run').click(), '')`)
	b.until("the dialog closed after its form ran", `!document.querySelector('[data-dialog="rescore"] input') && !location.hash.includes('dialog')`)

	b.open(e.URL + "/#/deck/settings")
	b.until("the tabs of settings", `[...document.querySelectorAll('[role=tab]')].some((x) => x.innerText.trim() === 'Tokens')`)
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.innerText.trim() === 'Tokens').click(), '')`)
	b.until("the tab in the address", `decodeURIComponent(location.hash).includes('tab=Tokens')`)
	b.open(e.URL + "/#/deck/journal")
	b.until("the journal", `[...document.querySelectorAll('[role=tab]')].some((x) => x.innerText.trim() === 'Spend')`)
	b.eval(`(document.querySelector('[data-back]').click(), '')`)
	b.until("back on the tab picked before", `[...document.querySelectorAll('[role=tab][data-active]')].some((x) => x.innerText.trim() === 'Tokens')`)
	b.eval(`(location.reload(), '')`)
	b.until("the tab kept over a reload", `[...document.querySelectorAll('[role=tab][data-active]')].some((x) => x.innerText.trim() === 'Tokens')`)
}
