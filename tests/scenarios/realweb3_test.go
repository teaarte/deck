package scenarios

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

// themeProbe is the HTML of a custom block that reports the theme of its host, first and after each change.
const themeProbe = `<!doctype html><html><body><p>theme</p><script type="module">
import { App } from "/ext-apps.js";
const app = new App({ name: "theme", version: "1" });
const report = () => { const c = app.getHostContext() ?? {}; const v = c.styles?.variables ?? {};
  return app.callServerTool({ name: "web.mark", arguments: { text: JSON.stringify({ theme: c.theme ?? "",
    bg: v["--color-background-primary"] ?? "", ring: v["--color-ring-primary"] ?? "", radius: v["--border-radius-md"] ?? "" }) } }); };
app.onhostcontextchanged = () => void report();
await app.connect();
await report();
</script></body></html>`

type hostTheme struct{ Theme, Bg, Ring, Radius string }

// themesSeen are the themes the custom block reported, in order.
func (e *Env) themesSeen() []hostTheme {
	e.T.Helper()
	var out []hostTheme
	ps := e.processes()
	slices.Reverse(ps)
	for _, x := range ps {
		if x.Command != "mark" {
			continue
		}
		var res struct{ Seen string }
		_ = json.Unmarshal(e.waitState(x.ID, "done").Result, &res)
		var th hostTheme
		_ = json.Unmarshal([]byte(res.Seen), &th)
		out = append(out, th)
	}
	return out
}

// TestRealBrowserSettings drives the Settings screen of deck in the real browser of Playwright MCP:
// DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowser -v.
func TestRealBrowserSettings(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, boardWebAgent())
	e.createProject("web", boardWebManifest)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": themeProbe}, nil)

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1280, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/deck/settings")
	b.until("the appearance tab", `document.querySelector('[data-appearance]')`)
	root := func(v string) string {
		return `getComputedStyle(document.documentElement).getPropertyValue('` + v + `').trim()`
	}
	if got := b.eval(`document.documentElement.dataset.mantineColorScheme + ' ' + document.querySelector('[data-look="scheme"] input[value="auto"]').checked`); got != "light true" {
		t.Fatalf("the scheme follows the system, light in the headless browser: %s", got)
	}
	b.eval(`(document.querySelector('[data-look="scheme"] input[value="dark"]').click(), '')`)
	b.until("the dark scheme", `document.documentElement.dataset.mantineColorScheme === 'dark'`)
	b.eval(`(document.querySelector('[data-accent="grape"]').click(), '')`)
	b.until("the accent", root("--mantine-primary-color-filled")+` === `+root("--mantine-color-grape-filled"))
	b.eval(`(document.querySelector('[data-look="density"] input[value="compact"]').click(), '')`)
	b.until("the compact spacing", root("--mantine-spacing-md")+`.includes('0.75')`)
	b.eval(`(document.querySelector('[data-look="font"] input[value="large"]').click(), '')`)
	b.until("the large text", root("--mantine-font-size-md")+`.includes('1.15')`)
	b.eval(`(document.querySelector('[data-look="radius"] input[value="xl"]').click(), '')`)
	b.until("the round corners", root("--mantine-radius-default")+` === `+root("--mantine-radius-xl"))
	b.shot("settings-interface")

	b.open(e.URL + "/#/deck/settings")
	b.until("the look kept on the device", `document.documentElement.dataset.mantineColorScheme === 'dark' && `+
		root("--mantine-primary-color-filled")+` === `+root("--mantine-color-grape-filled")+` && `+root("--mantine-spacing-md")+`.includes('0.75')`)

	b.open(e.URL + "/#/p/web/main")
	b.until("the custom block", `document.querySelector('[data-custom]')`)
	dark := hostTheme{Theme: "dark", Bg: b.eval(root("--mantine-color-body")), Ring: b.eval(root("--mantine-color-grape-filled")),
		Radius: b.eval(root("--mantine-radius-md"))}
	e.waitFor("the custom block to report its theme", func() bool { return len(e.themesSeen()) > 0 })
	if got := e.themesSeen()[0]; got != dark || got.Bg == "" || got.Radius == "" {
		t.Fatalf("a custom block gets the scheme and the resolved variables of the host: %+v, want %+v", got, dark)
	}

	b.call("browser_tabs", map[string]any{"action": "new"})
	b.open(e.URL + "/#/deck/settings")
	b.until("the appearance tab in the second tab", `document.querySelector('[data-appearance]')`)
	b.eval(`(document.querySelector('[data-look="scheme"] input[value="light"]').click(), '')`)
	b.until("the light scheme", `document.documentElement.dataset.mantineColorScheme === 'light'`)
	light := b.eval(root("--mantine-color-body"))
	b.call("browser_tabs", map[string]any{"action": "select", "index": 0})
	e.waitFor("the custom block to hear of the light scheme in place", func() bool {
		ts := e.themesSeen()
		return len(ts) > 1 && ts[len(ts)-1].Theme == "light"
	})
	if ts := e.themesSeen(); ts[len(ts)-1].Bg != light || ts[len(ts)-1].Bg == dark.Bg {
		t.Fatalf("the new scheme brings its colors to the frame: %+v, want %s", ts, light)
	}

	_, tok := e.token("terminal", "deck.*")
	if code, out := e.callWith(tok, "deck.project_create", map[string]any{"name": "notes", "manifest": draftManifest}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	tab := func(name string) {
		b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.innerText.trim() === '` + name + `').click(), '')`)
	}
	b.open(e.URL + "/#/deck/settings")
	b.until("the projects tab", `[...document.querySelectorAll('[data-item]')].some((x) => x.innerText.includes('notes · draft') && x.innerText.includes('enabling grants'))`)
	b.eval(`([...document.querySelectorAll('[data-item]')].find((x) => x.innerText.includes('notes · draft')).querySelector('[data-verb="enable"]').click(), '')`)
	b.until("the enabled draft", `[...document.querySelectorAll('[data-item]')].some((x) => x.innerText.includes('notes · enabled'))`)
	if p := e.project("notes"); p.Status != "enabled" {
		t.Fatalf("enable on the projects tab turns the draft on: %+v", p)
	}
	b.shot("settings-projects")

	tab("Secrets")
	secret := `document.querySelector('[data-form="deck.secret_set"] input[data-field="value"]')`
	b.until("the secret form", secret)
	if got := b.eval(secret + `.type`); got != "password" {
		t.Fatalf("the value of a secret is typed into a hidden field: %s", got)
	}
	b.type_(`document.querySelector('[data-form="deck.secret_set"] input[data-field="name"]')`, "mail-key")
	b.type_(secret, "s3cret-value")
	b.eval(`(document.querySelector('[data-form="deck.secret_set"] [data-submit]').click(), '')`)
	b.until("the secret in the list and its value gone from the field", `[...document.querySelectorAll('[data-item]')].some((x) => x.innerText.trim() === 'mail-key') && `+secret+`.value === ''`)
	if got := b.eval(`String(document.body.innerText.includes('s3cret-value'))`); got != "false" {
		t.Fatal("the value of a secret is not on the page after it is set")
	}

	tab("Tokens")
	b.until("the token form", `document.querySelector('[data-form="deck.token_create"]')`)
	b.type_(`document.querySelector('[data-form="deck.token_create"] input[data-field="name"]')`, "shortcuts")
	b.eval(`(() => { const i = document.querySelector('[data-form="deck.token_create"] input[data-field="commands"]'); i.focus();
		Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(i, 'deck.notify');
		i.dispatchEvent(new Event('input', { bubbles: true })); i.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })); return '' })()`)
	b.eval(`(document.querySelector('[data-form="deck.token_create"] [data-submit]').click(), '')`)
	b.until("the new token shown once under its form", `document.querySelector('[data-form="deck.token_create"] [data-outcome="done"]')?.innerText.includes('claude mcp add')`)
	if got := b.eval(`[...document.querySelectorAll('[data-form="deck.token_create"] [data-field]')].map((x) => x.dataset.field).join(' ')`); got != "name commands" {
		t.Fatalf("a form asks in the order of the command's params: %s", got)
	}
	b.until("the token in the list", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('shortcuts') && r.innerText.includes('live'))`)
	b.eval(`([...document.querySelectorAll('tr')].find((r) => r.innerText.includes('shortcuts')).querySelector('[data-verb="revoke"]').click(), '')`)
	b.until("the owner asked before the revoke", `document.querySelector('[data-confirm]')`)
	var toks []struct {
		Name    string
		Revoked bool
	}
	e.must("deck.tokens_list", nil, &toks)
	for _, x := range toks {
		if x.Name == "shortcuts" && x.Revoked {
			t.Fatal("the token is not revoked before the owner says yes")
		}
	}
	b.eval(`(document.querySelector('[data-confirm-yes]').click(), '')`)
	b.until("the revoked token", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('shortcuts') && r.innerText.includes('revoked'))`)
	b.shot("settings-tokens")

	tab("Providers")
	b.until("the provider form", `document.querySelector('[data-form="deck.provider_set"] input[data-field="command"]')?.offsetParent`)
	b.pick(`[data-form="deck.provider_set"] input[data-field="command"]`, "Score (web.score)")
	b.pick(`[data-form="deck.provider_set"] input[data-field="provider"]`, "fake")
	b.eval(`(document.querySelector('[data-form="deck.provider_set"] [data-submit]').click(), '')`)
	b.until("the provider set", `document.querySelector('[data-form="deck.provider_set"] [data-outcome="done"]')`)
	var chosen struct{ Chosen string }
	e.must("deck.providers_list", map[string]any{"command": "web.score"}, &chosen)
	if chosen.Chosen != "fake" {
		t.Fatalf("the command and provider picked from the lists are set: %+v", chosen)
	}

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(e.URL + "/#/deck/settings")
	tab("Projects")
	b.until("the projects tab on a phone", `[...document.querySelectorAll('[data-item]')].some((x) => x.offsetParent && x.innerText.includes('notes · enabled'))`)
	if got := b.eval(`String(document.documentElement.scrollWidth <= innerWidth + 1)`); got != "true" {
		t.Fatalf("the projects tab fits the width of a phone: %s", got)
	}
	b.shot("settings-projects-phone")
	tab("Interface")
	b.until("the appearance tab on a phone", `document.querySelector('[data-appearance]')`)
	if got := b.eval(`String(document.documentElement.scrollWidth <= innerWidth + 1)`); got != "true" {
		t.Fatalf("settings fit the width of a phone: %s", got)
	}
	b.shot("settings-phone")

	tab("Projects")
	if got := b.eval(`String(document.querySelector('nav').innerText.toLowerCase().includes('notes'))`); got != "true" {
		t.Fatalf("the project is in the menu before the delete: %s", got)
	}
	b.eval(`([...document.querySelectorAll('[data-item]')].find((x) => x.offsetParent && x.innerText.includes('notes · enabled')).querySelector('[data-verb="delete"]').click(), '')`)
	b.until("the owner asked before the delete", `document.querySelector('[data-confirm]')?.innerText.includes('notes')`)
	var still []projectV
	e.must("deck.projects_list", nil, &still)
	if len(still) != 2 {
		t.Fatalf("nothing is deleted before the owner says yes: %+v", still)
	}
	b.eval(`(document.querySelector('[data-confirm-yes]').click(), '')`)
	b.until("the deleted project gone from the tab and the menu", `![...document.querySelectorAll('[data-item]')].some((x) => x.innerText.includes('notes ·')) && !document.querySelector('nav').innerText.toLowerCase().includes('notes')`)
	e.must("deck.projects_list", nil, &still)
	if len(still) != 1 || still[0].Name != "web" {
		t.Fatalf("the project is deleted after the yes: %+v", still)
	}
}

// TestRealBrowserDocuments reads, edits and reverts a document of a project on its page, and draws markdown
// where a list says so: DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowser -v.
func TestRealBrowserDocuments(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	evil := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { w.Write([]byte("GIF89a")) })
	e := newEnv(t, nil)
	e.createProject("web", docsManifest)
	var d struct{ ID int64 }
	e.must("deck.document_save", map[string]any{"project": "web", "name": "notes/criteria.md", "kind": "doc",
		"text": "# Criteria\n\n- **short** <img src=x onerror=alert(1)>\n\n![seen](" + evil.URL + "/pixel?d=secret)"}, &d)
	for _, text := range []string{"<p>one</p>", "<p>two</p>"} {
		e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": text}, nil)
	}
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": jsonl(map[string]any{"title": "alpha", "note": "a **bold** note"})}, nil)

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1280, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/p/web/main")
	b.until("markdown in the list that asks for it", `[...document.querySelectorAll('[data-item] [data-markdown] strong')].some((x) => x.innerText === 'bold')`)
	b.until("the named document on the main screen", `document.querySelector('[data-document="notes/criteria.md"] [data-markdown] h1')?.innerText === 'Criteria'`)
	if got := b.eval(`String(!!document.querySelector('[data-markdown] img[onerror]'))`); got != "false" {
		t.Fatal("markdown is sanitized: no handlers in the page")
	}
	b.until("the picture of the markdown in the page", `document.querySelector('[data-markdown] img[alt="seen"]')`)
	time.Sleep(time.Second)
	if n := len(evil.requests()); n != 0 {
		t.Fatalf("a picture in markdown is not fetched from the network: %d requests", n)
	}

	b.eval(`([...document.querySelectorAll('nav a')].find((a) => a.innerText.trim() === 'Documents').click(), '')`)
	b.until("the documents screen", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('notes/criteria.md'))`)
	b.eval(`([...document.querySelectorAll('tr')].find((r) => r.innerText.includes('notes/criteria.md')).querySelector('button').click(), '')`)
	b.until("the document page", `location.hash === '#/p/web/document/`+fmt.Sprint(d.ID)+`' && document.querySelector('[data-edit]')`)
	b.eval(`(document.querySelector('[data-edit]').click(), '')`)
	b.type_(`document.querySelector('[data-draft]')`, "# Criteria\n\n- *brief*")
	b.type_(`document.querySelector('[data-reason]')`, "shorter")
	b.eval(`(document.querySelector('[data-save-document]').click(), '')`)
	b.until("the saved revision drawn", `document.querySelector('[data-markdown] em')?.innerText === 'brief' && !document.querySelector('[data-draft]')`)
	var got struct {
		Text     string
		Revision int64
	}
	e.must("deck.document_get", map[string]any{"document": d.ID}, &got)
	if got.Text != "# Criteria\n\n- *brief*" || got.Revision != 2 {
		t.Fatalf("the edit is a new revision: %+v", got)
	}
	b.eval(`(document.querySelector('[data-history]').click(), '')`)
	b.until("the revisions", `document.querySelector('[data-revert="1"]')`)
	b.shot("document-page")
	b.eval(`(document.querySelector('[data-revert="1"]').click(), '')`)
	b.until("the first text back", `document.querySelector('[data-markdown] strong')?.innerText === 'short'`)
	e.must("deck.document_get", map[string]any{"document": d.ID}, &got)
	if got.Revision != 3 {
		t.Fatalf("going back is a new revision: %+v", got)
	}

	var ds []struct {
		ID   int64
		Name string
	}
	e.must("deck.documents_list", map[string]any{"project": "web"}, &ds)
	for _, x := range ds {
		if x.Name == "manifest.json" {
			b.open(e.URL + "/#/p/web/document/" + fmt.Sprint(x.ID))
		}
	}
	b.until("the manifest as source", `document.querySelector('[data-document="manifest.json"] [data-source]')?.innerText.includes('"collections"')`)
	if got := b.eval(`String(!!document.querySelector('[data-edit]'))`); got != "false" {
		t.Fatal("the manifest is read only on screen")
	}
	for _, x := range ds {
		if x.Name == "custom/chart.html" {
			b.open(e.URL + "/#/p/web/document/" + fmt.Sprint(x.ID))
		}
	}
	b.until("the history of the custom block", `document.querySelector('[data-document="custom/chart.html"] [data-history]')`)
	b.eval(`(document.querySelector('[data-history]').click(), '')`)
	b.until("its revisions", `document.querySelector('[data-revision="1"]')`)
	if got := b.eval(`String(!!document.querySelector('[data-revert]') || !!document.querySelector('[data-edit]'))`); got != "false" {
		t.Fatal("a read-only document has no edit and no going back on screen")
	}
	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(e.URL + "/#/p/web/document/" + fmt.Sprint(d.ID))
	b.until("the page on a phone", `document.querySelector('[data-document] [data-markdown] h1')`)
	if got := b.eval(`String(document.documentElement.scrollWidth <= innerWidth + 1)`); got != "true" {
		t.Fatalf("a document page fits the width of a phone: %s", got)
	}
	b.shot("document-phone")
}

// TestRealBrowserJournal takes a snapshot from the journal, downloads a file and shows its tabs on a phone:
// DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowser -v.
func TestRealBrowserJournal(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, nil)
	e.createProject("web", boardWebManifest)
	for _, name := range []string{"notes ü.txt", "100%.txt", `100% a;b".txt`} {
		e.upload("web", name, "text/html", "body of "+name)
	}
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(e.URL + "/?token=" + token + "#/deck/journal")
	b.until("the journal tabs", `[...document.querySelectorAll('[role=tab]')].map((x) => x.innerText.trim()).join(' ') === 'Calls Spend Files Snapshots'`)
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.innerText.trim() === 'Snapshots').click(), '')`)
	b.until("the snapshot form", `document.querySelector('[data-form="deck.snapshot_create"] [data-submit]')?.offsetParent`)
	b.eval(`(document.querySelector('[data-form="deck.snapshot_create"] [data-submit]').click(), '')`)
	b.until("the new snapshot in the list", `[...document.querySelectorAll('tr')].some((r) => r.offsetParent && r.innerText.includes('deck-'))`)
	b.eval(`(window.saved = [], HTMLAnchorElement.prototype.click = function () { fetch(this.href).then((r) => r.blob()).then(async (b) => { window.saved.push(b.type + ' ' + this.download + '=' + await b.text()); }); }, '')`)
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.innerText.trim() === 'Files').click(), '')`)
	b.until("the download buttons of the files", `[...document.querySelectorAll('[data-verb=download]')].filter((x) => x.offsetParent).length === 3`)
	b.eval(`([...document.querySelectorAll('[data-verb=download]')].filter((x) => x.offsetParent).forEach((x) => x.click()), '')`)
	b.until("the downloaded files under their names, never as a page", `window.saved.length === 3 && window.saved.sort().join('|') === `+
		`'application/octet-stream 100% a;b".txt=body of 100% a;b".txt|application/octet-stream 100%.txt=body of 100%.txt|application/octet-stream notes ü.txt=body of notes ü.txt'`)
	if got := b.eval(`String(document.documentElement.scrollWidth <= innerWidth + 1)`); got != "true" {
		t.Fatalf("the journal fits the width of a phone: %s", got)
	}
	b.shot("journal-phone")
}

// TestRealBrowserRework sends a finished agent back with a remark from its page:
// DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowser -v.
func TestRealBrowserRework(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, ok)
	p := e.createProject("jobs", testManifest([]string{agentCommand("job", "")}, nil))
	job := e.run(p, "job")
	e.waitState(job, "done")
	b := newPlaywright(t)
	b.open(e.URL + "/?token=" + token + "#/deck/process/" + fmt.Sprint(job))
	b.until("the rework button", `document.querySelector('[data-verb="rework"]')`)
	b.eval(`(document.querySelector('[data-verb="rework"]').click(), '')`)
	b.until("the remark field", `document.querySelector('input[placeholder="What to change"]')`)
	b.type_(`document.querySelector('input[placeholder="What to change"]')`, "shorter, please")
	b.eval(`([...document.querySelectorAll('button')].find((x) => x.innerText === 'Send').click(), '')`)
	e.waitFor("the second turn with the remark", func() bool {
		cs := e.Fake.Calls()
		return len(cs) == 2 && strings.Contains(cs[1].Prompt, "shorter, please")
	})
	e.waitState(job, "done")
}
