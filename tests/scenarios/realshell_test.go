package scenarios

import (
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestRealBrowserShell drives the islands of deck in a real browser, menu, chat and columns:
// DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowserShell -v.
func TestRealBrowserShell(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, boardWebAgent())
	manifest := strings.Replace(boardWebManifest, `{"version": 5,`, `{"version": 5, "icon": "rocket",`, 1)
	manifest = strings.Replace(manifest, `{"type": "table", "title": "Notes",`, `{"type": "table", "title": "Notes", "icon": "notes",`, 1)
	manifest = strings.Replace(manifest, `"columns": [{"label": "Title", "bind": "title"}]`, `"columns": [{"label": "Title", "bind": "title"}, {"label": "Status", "bind": "source"}, {"label": "Note", "bind": "note"}]`, 1)
	e.createProject("web", strings.Replace(manifest, `{"label": "Open", "screen": "note"}`, `{"label": "Open", "icon": "arrow-right", "screen": "note"}`, 1))
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes",
		"records": jsonl(map[string]any{"title": "alpha", "source": "mail", "note": strings.Repeat("the model did not finish the self-check within the ceiling ", 6)}, map[string]any{"title": "a-very-long-title-without-any-spaces-that-would-push-the-table-out-of-its-block-" + strings.Repeat("x", 120), "source": "web"})}, nil)
	e.waitState(e.start("web", "tell", nil), "done")

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1440, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/deck/cockpit")
	b.until("the cockpit in its islands", `document.querySelector('[data-nav] [data-group-head]') && document.querySelector('[data-block]')`)
	if got := b.eval(`String(!!document.querySelector('header'))`); got != "false" {
		t.Fatalf("the desktop has no header bar: %s", got)
	}
	if got := b.eval(`(() => { const s = getComputedStyle(document.querySelector('[data-nav]')); const r = document.querySelector('[data-nav]').getBoundingClientRect();
		return [parseFloat(s.borderTopLeftRadius) > 0, s.boxShadow !== 'none', r.left > 0, r.top > 0].join(' ') })()`); got != "true true true true" {
		t.Fatalf("the menu is an island: rounded, with a shadow, apart from the edges: %s", got)
	}
	b.until("the icons of the menu", `document.querySelector('[data-nav] [data-icon="layout-dashboard"]') && document.querySelector('[data-group="web"] [data-icon="rocket"]')`)
	if got := b.eval(`document.querySelector('[data-columns]') ? (() => { const hs = [...document.querySelectorAll('[data-columns] > * > [data-block]')].map((x) => Math.round(x.getBoundingClientRect().height)); return String(hs.length > 1 && new Set(hs).size === 1) })() : 'none'`); got != "true" {
		t.Fatalf("the blocks of a row take one height: %s", got)
	}
	b.until("the pattern of icons behind the shell", `getComputedStyle(document.querySelector('[data-backdrop] > div')).maskImage.startsWith('url(')`)
	b.shot("shell-cockpit")

	b.eval(`(document.querySelector('[data-group="web"] [data-group-head]').click(), '')`)
	b.until("the group of web folded", `!document.querySelector('[data-group="web"] a')?.offsetParent && document.querySelector('[data-group="web"] [data-group-head]').getAttribute('aria-expanded') === 'false'`)
	b.eval(`(document.querySelector('[data-group="web"] [data-group-head]').click(), '')`)
	b.until("the group of web open again", `document.querySelector('[data-group="web"] a')?.offsetParent`)

	b.open(e.URL + "/#/p/web/main")
	b.until("the project screen", `document.querySelector('[data-block] table')`)
	b.eval(`(document.querySelector('[data-group="web"] [data-group-head]').click(), '')`)
	b.until("the group of the open screen folds too", `document.querySelector('[data-group="web"] [data-group-head]').getAttribute('aria-expanded') === 'false'`)
	b.eval(`(document.querySelector('[data-group="web"] [data-group-head]').click(), '')`)
	if got := b.eval(`(() => { const t = [...document.querySelectorAll('[data-block]')].find((x) => x.querySelector('table')); return String(t.scrollWidth <= t.clientWidth + 1) })()`); got != "true" {
		t.Fatalf("a long word stays inside its block: %s", got)
	}
	if got := b.eval(`[...document.querySelectorAll('th')].filter((x) => x.offsetParent && x.getBoundingClientRect().height > 48).map((x) => x.innerText).join('|')`); got != "" {
		t.Fatalf("a header of a table beside a long column keeps its words whole: %s", got)
	}
	b.until("the icons of a block and its button", `document.querySelector('[data-block-icon] [data-icon="notes"]') && document.querySelector('button [data-icon="arrow-right"]')`)
	b.shot("shell-light")

	b.open(e.URL + "/#/deck/settings")
	b.until("the tabs of settings", `[...document.querySelectorAll('[role=tab]')].some((x) => x.innerText.trim() === 'Providers')`)
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.innerText.trim() === 'Providers').click(), '')`)
	b.until("the providers table", `[...document.querySelectorAll('[data-block] table')].some((x) => x.offsetParent)`)
	if got := b.eval(`[...document.querySelectorAll('th')].filter((x) => x.offsetParent && x.getBoundingClientRect().height > 48).map((x) => x.innerText).join('|')`); got != "" {
		t.Fatalf("a header of a table keeps its words whole: %s", got)
	}

	b.eval(`(document.querySelector('[data-nav-hide]').click(), '')`)
	b.until("the menu hidden, the focus on the button that brings it back", `!document.querySelector('[data-nav]') && document.activeElement === document.querySelector('[data-nav-show]')`)
	b.open(e.URL + "/#/p/web/main")
	b.until("the menu stays hidden after a reload", `document.querySelector('[data-nav-show]') && !document.querySelector('[data-nav]')`)
	b.shot("shell-no-menu")
	b.eval(`(document.querySelector('[data-nav-show]').click(), '')`)
	b.until("the menu back", `document.querySelector('[data-nav]')`)

	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the chat island", `document.querySelector('[data-chat-panel] [data-chat-input]')`)
	b.call("browser_resize", map[string]any{"width": 1100, "height": 900})
	if got := b.eval(`String(document.querySelector('[data-main]').getBoundingClientRect().width >= 380)`); got != "true" {
		t.Fatalf("a narrow window keeps room for the screen beside the chat: %s", got)
	}
	b.call("browser_resize", map[string]any{"width": 1440, "height": 900})
	fill := `(() => { const p = document.querySelector('[data-chat-panel]').getBoundingClientRect(); const i = document.querySelector('[data-chat-panel] [data-chat-send]').getBoundingClientRect();
		return String(p.bottom - i.bottom < 40 && p.height > innerHeight - 60) })()`
	if got := b.eval(fill); got != "true" {
		t.Fatalf("the chat takes the full height of its island: %s", got)
	}
	before, _ := strconv.Atoi(b.eval(`String(Math.round(document.querySelector('[data-chat-panel]').getBoundingClientRect().width))`))
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { const g = await page.locator('[data-chat-panel] [role=separator]').boundingBox();
		await page.mouse.move(g.x + 5, g.y + g.height / 2); await page.mouse.down(); await page.mouse.move(g.x - 195, g.y + g.height / 2, { steps: 8 }); await page.mouse.up(); }`})
	after, _ := strconv.Atoi(b.eval(`String(Math.round(document.querySelector('[data-chat-panel]').getBoundingClientRect().width))`))
	if after < before+150 {
		t.Fatalf("a drag of its edge widens the chat: %d → %d", before, after)
	}
	b.shot("shell-chat")
	b.eval(`(document.querySelector('[data-chat-full]').click(), '')`)
	b.until("the chat on the full screen", `!document.querySelector('[data-main]').offsetParent && document.querySelector('[data-chat-panel]').getBoundingClientRect().width > 900`)
	if got := b.eval(fill); got != "true" {
		t.Fatalf("the full screen chat takes the full height: %s", got)
	}
	b.shot("shell-chat-full")
	b.eval(`(document.querySelector('[data-chat-full]').click(), '')`)
	b.until("the screen back beside the chat", `document.querySelector('[data-main]').offsetParent`)

	b.open(e.URL + "/#/deck/settings")
	b.until("the appearance tab", `document.querySelector('[data-look="theme"]')`)
	b.eval(`(document.querySelector('[data-look="theme"] input[value="glass"]').click(), '')`)
	b.eval(`(document.querySelector('[data-look="scheme"] input[value="dark"]').click(), '')`)
	b.until("the dark glass", `document.documentElement.dataset.deckTheme === 'glass' && document.documentElement.dataset.mantineColorScheme === 'dark'`)
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.innerText.trim() === 'Interface').click(), '')`)
	b.until("the tint slider of the glass", `document.querySelector('[data-look="tint"]')?.offsetParent`)
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { const t = await page.locator('[data-look="tint"] [role=slider]'); await t.focus(); await page.keyboard.press('End'); }`})
	b.until("opaque glass at the end of the slider", `getComputedStyle(document.documentElement).getPropertyValue('--glass-tint-user').trim() === '1' && /rgb\(\d+, \d+, \d+\)$/.test(getComputedStyle(document.querySelector('[data-nav]')).backgroundColor)`)
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { const t = await page.locator('[data-look="tint"] [role=slider]'); await t.focus(); await page.keyboard.press('Home'); }`})
	b.eval(`(document.querySelector('[data-look="pattern"]').click(), '')`)
	b.until("no pattern when the owner turns it off", `!document.querySelector('[data-backdrop] > div')`)
	b.eval(`(document.querySelector('[data-look="pattern"]').click(), '')`)
	b.until("the pattern back", `document.querySelectorAll('[data-backdrop] > div').length === 2`)
	b.open(e.URL + "/#/p/web/main")
	b.until("the dark project screen", `document.querySelector('[data-block] table')`)
	if got := b.eval(`getComputedStyle(document.querySelector('[data-nav]')).backdropFilter`); got == "none" {
		t.Fatalf("the menu island is glass: %s", got)
	}
	b.shot("shell-glass-dark")
	b.eval(`(document.querySelector('[data-chat-close]').click(), '')`)
	b.open(e.URL + "/#/deck/cockpit")
	b.until("the dark cockpit", `document.querySelector('[data-columns]')`)
	b.shot("shell-glass-dark-cockpit")
	b.open(e.URL + "/#/deck/settings")
	b.until("the appearance tab", `document.querySelector('[data-look="scheme"]')`)
	b.eval(`(document.querySelector('[data-look="scheme"] input[value="light"]').click(), document.querySelector('[data-accent="grape"]').click(), '')`)
	b.open(e.URL + "/#/p/web/main")
	b.until("the light glass", `document.querySelector('[data-block] table') && document.documentElement.dataset.mantineColorScheme === 'light'`)
	b.shot("shell-glass-light")

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(e.URL + "/#/p/web/main")
	b.until("the phone bar", `document.querySelector('[data-phone-bar]') && document.querySelector('[data-block] table')`)
	if got := b.eval(`String(document.documentElement.scrollWidth <= innerWidth + 1)`); got != "true" {
		t.Fatalf("the screen fits the width of a phone: %s", got)
	}
	b.shot("shell-phone")
	b.eval(`(document.querySelector('[data-nav-open]').click(), '')`)
	b.until("the menu on a phone", `document.querySelector('[data-nav-phone] [data-group-head]')?.offsetParent`)
	b.shot("shell-phone-menu")
}
