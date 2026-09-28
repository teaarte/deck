package scenarios

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
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
