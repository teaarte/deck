package scenarios

import (
	"os"
	"strings"
	"testing"
)

// TestRealBrowserCodeProject walks the owner's way to a code project in the real browser: create a project, import
// the code example, enable it, give it a repo folder, make a card, move it to Ready and press Run.
func TestRealBrowserCodeProject(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, ok)
	repo := repoFolder(t)
	module := codeModule(t, "")

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 1000})
	b.open(e.URL + "/?token=" + token + "#/deck/settings")
	form := func(cmd, field string) string {
		return `document.querySelector('[data-form="` + cmd + `"] [data-field="` + field + `"]')`
	}
	submit := func(cmd string) {
		b.eval(`(document.querySelector('[data-form="` + cmd + `"] [data-submit]').click(), '')`)
	}

	b.until("the create button", `document.querySelector('[data-dialog-open="create-project"]')`)
	b.eval(`(document.querySelector('[data-dialog-open="create-project"]').click(), '')`)
	b.until("the create form", form("deck.project_create", "name"))
	b.type_(form("deck.project_create", "name"), "notes")
	b.type_(form("deck.project_create", "prompt"), "Notes of the owner.")
	submit("deck.project_create")
	b.until("the new project in the list", itemOf("notes", "enabled · "))

	b.eval(`(document.querySelector('[data-dialog-open="import-project"]').click(), '')`)
	b.until("the import form", form("deck.project_import", "folder"))
	b.type_(form("deck.project_import", "folder"), module)
	b.type_(form("deck.project_import", "name"), "code")
	submit("deck.project_import")
	b.until("the imported draft", itemOf("code", "draft · "))
	b.eval(pressIn(itemOf("code", "draft · "), "enable"))
	b.until("the enabled project", itemOf("code", "enabled · "))

	b.eval(pressIn(itemOf("code", "enabled · "), "settings"))
	b.until("the settings of the project", `document.querySelector('[data-dialog-open="folder"]')`)
	b.eval(`(document.querySelector('[data-dialog-open="folder"]').click(), '')`)
	b.until("the folder form", form("deck.folder_add", "path"))
	b.type_(form("deck.folder_add", "path"), repo)
	submit("deck.folder_add")
	b.until("the folder in the folders of the project", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('my-repo') && r.innerText.includes('`+repo+`'))`)
	b.until("the commands of the project", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('code.run'))`)
	b.shot("code-settings")

	b.open(e.URL + "/#/p/code/board")
	b.until("the board", `document.querySelector('[data-lane="ready"]') && document.querySelector('[data-new-card]')`)
	b.type_(`document.querySelector('[data-new-card]')`, "Fix the login button")
	b.eval(`(document.querySelector('[data-new-card]').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })), '')`)
	b.until("the card in Backlog", `document.querySelector('[data-lane="backlog"] [data-card]')?.innerText.includes('Fix the login button')`)
	b.drag(`document.querySelector('[data-lane="backlog"] [data-handle]')`, `document.querySelector('[data-lane="ready"]')`)
	b.until("the card in Ready", `document.querySelector('[data-lane="ready"] [data-card]')`)
	b.shot("code-board")
	b.eval(`([...document.querySelectorAll('button')].find((x) => x.innerText.trim() === 'Run').click(), '')`)
	e.waitFor("the run at its provider", func() bool { return len(e.Fake.Calls()) > 0 })
	if c := e.Fake.Calls()[0]; c.Cwd != repo || !c.Repo || !strings.Contains(c.Prompt, `"do":"run"`) {
		t.Fatalf("Run starts the agent in the repo folder: cwd %s repo %v", c.Cwd, c.Repo)
	}
	b.until("the run in the list of runs", `document.body.innerText.includes('#1 · done')`)
	b.shot("code-run")
}
