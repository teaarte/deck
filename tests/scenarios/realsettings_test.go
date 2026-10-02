package scenarios

import (
	"os"
	"testing"
)

// TestRealBrowserProjectSettings opens the settings of a project from the Projects tab in the real browser: its
// commands and tools are there, and its pause button pauses it.
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
	item := `[...document.querySelectorAll('[data-item]')].find((x) => x.innerText.includes('notes · enabled'))`
	b.until("the project in the Projects tab", item)
	b.eval(`(` + item + `.querySelector('[data-verb="settings"]').click(), '')`)
	b.until("the commands of the project", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('notes.collect'))`)
	b.until("the tools of the project", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('notes.post'))`)
	b.shot("project-settings")
	b.eval(`(` + item + `.querySelector('[data-verb="pause"]').click(), '')`)
	e.waitFor("the paused project", func() bool {
		var ps []struct {
			Name   string
			Paused bool
		}
		e.projectsOf(&ps)
		return len(ps) == 1 && ps[0].Paused
	})
	b.until("the paused project on its settings", `[...document.querySelectorAll('[data-item]')].some((x) => x.innerText.includes('notes · enabled, paused'))`)
}
