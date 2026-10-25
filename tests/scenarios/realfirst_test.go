package scenarios

import (
	"os"
	"testing"
)

// TestRealBrowserFirstRun: an empty deck says to tell Deki what to do and offers the starter module as a card; it comes
// as a draft whose review names what it needs: DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowserFirstRun -v.
func TestRealBrowserFirstRun(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, ok)
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1440, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/deck/cockpit")
	b.until("an empty deck says what to do", `document.querySelector('[data-today-line]')?.innerText.includes('Tell Deki what to do')`)
	b.until("the starter module as a card", `[...document.querySelectorAll('[data-block]')].some((x) => x.innerText.includes('Start with a module') && x.innerText.includes('Free models'))`)
	b.shot("first-run")
	b.eval(`([...document.querySelectorAll('button')].find((x) => x.innerText.trim() === 'Add as a draft').click(), '')`)
	b.until("its review offered", `[...document.querySelectorAll('button')].some((x) => x.innerText.trim() === 'What it can do')`)
	b.eval(`([...document.querySelectorAll('button')].find((x) => x.innerText.trim() === 'What it can do').click(), '')`)
	b.until("the review names what it needs and what it may do", `location.hash.startsWith('#/deck/draft') && document.body.innerText.includes('What it needs to work') && document.body.innerText.includes('Add the connection nvidia') && document.body.innerText.includes('What it may do once enabled')`)
	b.shot("first-run-draft")
}
