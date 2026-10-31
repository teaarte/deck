package browser

import (
	"testing"

	"deck2/tests/harness"
)

// TestRealBrowserProjectSettings opens the settings of a project from the Projects tab in the real browser: its
// commands with a picker of their model, its tools beside its folders, and its pause button pauses it.
func TestRealBrowserProjectSettings(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("notes", harness.NotesManifest)

	b := Open(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 1000})
	b.Open(e, "#/deck/settings?tab=Modules")
	item := itemOf("notes", "")
	b.until("the project in the Projects tab", item)
	b.eval(pressIn(item, "settings"))
	b.until("the commands of the project", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('Collect'))`)
	b.shot("project-settings")
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.textContent.includes('Access')).click(), '')`)
	b.until("the folders and secrets of the project", `[...document.querySelectorAll('button')].some((x) => x.textContent.includes('Add a folder'))`)
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.textContent.includes('Advanced')).click(), '')`)
	b.until("its tools, read when their tab shows", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('notes.post'))`)
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.textContent.includes('Module')).click(), '')`)
	b.eval(pressIn(item, "pause"))
	e.Until("the paused project", func() bool {
		var ps []struct {
			Name   string
			Paused bool
		}
		e.ProjectsOf(&ps)
		return len(ps) == 1 && ps[0].Paused
	})
	b.until("the paused project on its settings", itemOf("notes", "enabled, paused"))

	e.Project("plain", harness.PlainManifest)
	b.open(e.URL + "/#/p/plain/settings")
	b.until("the model of a command in a picker", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('Work') && r.querySelector('input[data-pick="model"]')?.value === 'fake')`)
	b.eval(`(document.querySelector('tr input[data-pick="model"]').click(), '')`)
	b.until("the models it may run on", `[...document.querySelectorAll('[role=option]')].some((o) => o.textContent === 'fake')`)
	b.shot("project-settings-pick")
}
