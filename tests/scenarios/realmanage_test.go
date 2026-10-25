package scenarios

import (
	"context"
	"os"
	"testing"

	"deck2/internal/provider"
)

// TestRealBrowserTheScreensOfManagement: a failed schedule is counted on the tab of Health and is on top of the
// Schedule; no screen repeats its tab; Files goes into a module and back by its steps, on a phone too.
func TestRealBrowserTheScreensOfManagement(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: "the model is down"}
	})
	e.createProject("digest", testManifest([]string{agentCommand("work", "")}, []string{`{"command": "work", "cron": "0 9 * * *"}`}))
	e.createProject("inbox", filesManifest())
	e.upload("inbox", "invoice.pdf", "application/pdf", "%PDF-1.4")
	var ts []struct {
		ID          int64
		LastProcess int64 `json:"last_process"`
	}
	e.must("deck.triggers_list", map[string]any{"project": "digest"}, &ts)
	e.must("deck.trigger_run", map[string]any{"trigger": ts[0].ID}, nil)
	e.must("deck.triggers_list", map[string]any{"project": "digest"}, &ts)
	e.waitState(ts[0].LastProcess, "error")

	b := newPlaywright(t)
	fits := func(what string) {
		if got := b.eval(`String(document.documentElement.scrollWidth <= innerWidth + 1)`); got != "true" {
			t.Fatalf("%s fits the width: %s", what, got)
		}
	}
	untitled := `(() => { const tab = document.querySelector('[data-places] a[aria-current=page]')?.innerText.trim();
		return String(![...document.querySelectorAll('[data-screen-head] h2')].some((h) => h.innerText.trim() === tab)) })()`
	for _, size := range []struct {
		name          string
		width, height int
	}{{"", 1280, 820}, {"-phone", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.width, "height": size.height})
		b.open(e.URL + "/?token=" + token + "#/deck/settings")
		b.until("Health first, the failure counted on its tab", `document.querySelector('[role=tab][aria-selected=true]')?.innerText.includes('Health') && document.querySelector('[data-tab-count="failures"]')?.innerText.trim() === '1'`)
		b.until("what is broken with a way to run it again", `[...document.querySelectorAll('[data-item]')].some((x) => x.offsetParent && x.innerText.includes('the model is down') && [...x.querySelectorAll('button')].some((b) => b.innerText.includes('Run again')))`)
		if got := b.eval(untitled); got != "true" {
			t.Fatal("settings do not repeat their tab as a title")
		}
		fits("Health")
		b.shot("manage-health" + size.name)
		b.open(e.URL + "/#/deck/settings?tab=Models")
		b.until("who does what, the connections behind a button", `[...document.querySelectorAll('[data-block]')].some((x) => x.offsetParent && x.innerText.includes('Who does what')) && document.querySelector('[data-dialog-open="connections"]')`)
		b.shot("manage-models" + size.name)
		b.open(e.URL + "/#/deck/settings?tab=Modules")
		b.until("the modules as cards", `document.querySelector('[data-cards] [data-item]')`)
		fits("Modules")
		b.shot("manage-modules" + size.name)
		b.open(e.URL + "/#/deck/schedule")
		b.until("what did not pass on top", `[...document.querySelectorAll('[data-item]')].some((x) => x.offsetParent && x.innerText.includes('work') && x.innerText.includes('Run again'))`)
		fits("Schedule")
		b.shot("manage-schedule" + size.name)
		b.open(e.URL + "/#/deck/spend")
		b.until("the two numbers of the spend", `[...document.querySelectorAll('[data-stat]')].filter((x) => x.offsetParent).length === 2`)
		b.shot("manage-spend" + size.name)
		b.open(e.URL + "/#/deck/cockpit")
		b.until("Today begins with its first line and the date", `document.querySelector('[data-screen-head] [data-today-line]') && document.querySelector('[data-screen-head] [data-today-date]')`)
		if got := b.eval(untitled); got != "true" {
			t.Fatal("Today does not repeat its tab as a title")
		}
		b.shot("manage-today" + size.name)
		b.open(e.URL + "/#/deck/files")
		folder := `[...document.querySelectorAll('[data-place]')].find((x) => x.offsetParent && x.innerText.includes('inbox'))`
		b.until("the folders of the modules with what they hold", folder+`?.querySelector('[data-place-meta]')`)
		b.shot("manage-files" + size.name)
		b.eval(`(` + folder + `.click(), '')`)
		b.until("inside the module on the same screen", `location.hash.startsWith('#/deck/files') && [...document.querySelectorAll('[data-place]')].some((x) => x.offsetParent && x.innerText.includes('Data folder'))`)
		b.eval(`([...document.querySelectorAll('[data-place]')].find((x) => x.offsetParent && x.innerText.trim().startsWith('Files')).click(), '')`)
		b.until("its files with the steps back", `[...document.querySelectorAll('[data-tile], [data-row]')].some((x) => x.offsetParent && x.innerText.includes('invoice.pdf')) && document.querySelector('[data-crumb-up]')`)
		fits("Files")
		b.shot("manage-files-module" + size.name)
		b.eval(`(document.querySelector('[data-crumb-up]').click(), '')`)
		b.until("one step back", `[...document.querySelectorAll('[data-place]')].some((x) => x.offsetParent && x.innerText.includes('Data folder'))`)
	}
}
