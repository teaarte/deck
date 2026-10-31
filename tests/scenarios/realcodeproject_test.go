package scenarios

import (
	"deck2/tests/harness"
	"os"
	"strings"
	"testing"
)

// TestRealBrowserCodeProject walks the owner's way to a code project in the real browser: create a project, import
// the code example, enable it, give it a repo folder, mark a task ready on its page and press Run.
func TestRealBrowserCodeProject(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := harness.New(t, harness.WithAgent(harness.Ok))
	repo := repoFolder(t)
	module := codeModule(t, "")

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 1000})
	b.Open(e, "#/deck/settings?tab=Modules")
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
	b.until("the new project in the list", itemOf("notes", ""))

	b.eval(`(document.querySelector('[data-dialog-open="import-project"]').click(), '')`)
	b.until("the import form", form("deck.project_import", "folder"))
	b.type_(form("deck.project_import", "folder"), module)
	b.type_(form("deck.project_import", "name"), "code")
	submit("deck.project_import")
	b.until("the imported draft", itemOf("code", "draft"))
	b.reviewAndEnable("code")
	e.Until("the enabled project", func() bool { return project(e, "code").Status == "enabled" })
	b.open(e.URL + "/#/deck/settings?tab=Modules")
	b.until("the enabled project", itemOf("code", ""))

	tab := func(name string) {
		b.until("the tab "+name, `[...document.querySelectorAll('[role="tab"]')].some((x) => x.innerText.trim() === '`+name+`')`)
		b.eval(`([...document.querySelectorAll('[role="tab"]')].find((x) => x.innerText.trim() === '` + name + `').click(), '')`)
	}
	b.eval(pressIn(itemOf("code", ""), "settings"))
	tab("Access")
	b.until("the settings of the project", `document.querySelector('[data-dialog-open="folder"]')`)
	b.eval(`(document.querySelector('[data-dialog-open="folder"]').click(), '')`)
	b.until("the folder form", form("deck.folder_add", "path"))
	b.type_(form("deck.folder_add", "path"), repo)
	submit("deck.folder_add")
	b.until("the folder in the folders of the project", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('my-repo') && r.innerText.includes('`+repo+`'))`)
	tab("Module")
	b.until("the commands of the project", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('Run') && r.querySelector('input[data-pick="model"]'))`)
	b.shot("code-settings")

	e.Must("deck.records_upsert", map[string]any{"project": "code", "collection": "tasks",
		"records": harness.Jsonl(map[string]any{"title": "Fix the login button", "status": "backlog"})}, nil)
	b.open(e.URL + "/#/p/code/tasks")
	b.until("the task", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('Fix the login button'))`)
	b.eval(`([...document.querySelectorAll('tr')].find((r) => r.innerText.includes('Fix the login button')).click(), '')`)
	b.until("the form of the task", `document.querySelector('[data-form="deck.record_update"]')`)
	b.pick(`[data-form="deck.record_update"] [data-field="status"]`, "ready")
	b.eval(`(document.querySelector('[data-form="deck.record_update"] [data-submit]').click(), '')`)
	e.Until("the task marked ready", func() bool {
		var rs []harness.RecordRow
		e.Must("deck.records_list", map[string]any{"project": "code", "collection": "tasks"}, &rs)
		return len(rs) == 1 && rs[0].Data["status"] == "ready"
	})
	b.open(e.URL + "/#/p/code/tasks")
	b.until("the ready task", `[...document.querySelectorAll('tr')].some((r) => r.innerText.toLowerCase().includes('ready'))`)
	b.shot("code-tasks")
	b.eval(`([...document.querySelectorAll('button')].find((x) => x.innerText.trim() === 'Run').click(), '')`)
	e.Until("the run at its provider", func() bool { return len(e.Fake.Calls()) > 0 })
	if c := e.Fake.Calls()[0]; c.Cwd != repo || !c.Repo || !strings.Contains(c.Prompt, `"do":"run"`) {
		t.Fatalf("Run starts the agent in the repo folder: cwd %s repo %v", c.Cwd, c.Repo)
	}
	b.until("the run in the list of runs", `document.body.innerText.includes('#1 · done')`)
	b.shot("code-run")
}
