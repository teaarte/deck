package scenarios

import (
	"os"
	"testing"
)

// TestRealBrowserProjectSettings opens the settings of a project from the Projects tab in the real browser: its
// commands with a picker of their model, its tools beside its folders, and its pause button pauses it.
func TestRealBrowserProjectSettings(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, ok)
	e.createProject("notes", notesManifest)

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 1000})
	b.open(e.URL + "/?token=" + token + "#/deck/settings")
	item := itemOf("notes", "enabled")
	b.until("the project in the Projects tab", item)
	b.eval(pressIn(item, "settings"))
	b.until("the commands of the project", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('Collect'))`)
	b.shot("project-settings")
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.textContent.includes('Access')).click(), '')`)
	b.until("the tools of the project with its folders and secrets", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('notes.post')) && [...document.querySelectorAll('button')].some((x) => x.textContent.includes('Add a folder'))`)
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.textContent.includes('Project')).click(), '')`)
	b.eval(pressIn(item, "pause"))
	e.waitFor("the paused project", func() bool {
		var ps []struct {
			Name   string
			Paused bool
		}
		e.projectsOf(&ps)
		return len(ps) == 1 && ps[0].Paused
	})
	b.until("the paused project on its settings", itemOf("notes", "enabled, paused"))

	e.createProject("plain", plainManifest)
	b.open(e.URL + "/#/p/plain/settings")
	b.until("the model of a command in a picker", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('Work') && r.querySelector('input[data-pick="model"]')?.value === 'fake')`)
	b.eval(`(document.querySelector('tr input[data-pick="model"]').click(), '')`)
	b.until("the models it may run on", `[...document.querySelectorAll('[role=option]')].some((o) => o.textContent === 'fake')`)
	b.shot("project-settings-pick")
}
