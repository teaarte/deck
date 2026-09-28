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
	root := func(v string) string { return `getComputedStyle(document.documentElement).getPropertyValue('` + v + `').trim()` }
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

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(e.URL + "/#/deck/settings")
	b.until("the appearance tab on a phone", `document.querySelector('[data-appearance]')`)
	if got := b.eval(`String(document.documentElement.scrollWidth <= innerWidth + 1)`); got != "true" {
		t.Fatalf("settings fit the width of a phone: %s", got)
	}
	b.shot("settings-phone")
}
