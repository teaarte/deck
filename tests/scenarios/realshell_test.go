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
	manifest := strings.Replace(boardWebManifest, `{"version": 6,`, `{"version": 6, "icon": "rocket",`, 1)
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
	b.until("a new notification on the bell", `document.querySelector('[data-bell-count="1"]')`)
	b.eval(`(document.querySelector('[data-bell]').click(), '')`)
	b.until("the notification in the list of the bell", `document.querySelector('[data-bell-list] [data-note]')?.innerText.includes('alpha scored high')`)
	b.shot("shell-bell")
	b.until("the bell has nothing new once seen", `!document.querySelector('[data-bell-count]')`)
	b.eval(`(document.querySelector('[data-bell]').click(), '')`)

	b.eval(`(document.querySelector('[data-group="web"] [data-group-head]').click(), '')`)
	b.until("the group of web folded", `!document.querySelector('[data-group="web"] a:not([data-extra])')?.offsetParent && document.querySelector('[data-group="web"] [data-group-head]').getAttribute('aria-expanded') === 'false'`)
	b.eval(`(document.querySelector('[data-group="web"] [data-group-head]').click(), '')`)
	b.until("the group of web open again", `document.querySelector('[data-group="web"] a:not([data-extra])')?.offsetParent`)

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

	b.open(e.URL + "/#/deck/journal?tab=Spend")
	b.until("the stats and the chart of the spend", `document.querySelectorAll('[data-stat]').length === 4 && document.querySelector('[data-chart-block] svg')`)
	if got := b.eval(`String(new Set([...document.querySelectorAll('[data-stat]')].map((x) => Math.round(x.closest('[data-block]').getBoundingClientRect().top))).size)`); got != "1" {
		t.Fatalf("the four numbers of the spend stand in one row: %s", got)
	}
	b.shot("shell-spend")
	b.open(e.URL + "/#/deck/schedule")
	b.until("the runs chart of the schedule", `document.querySelector('[data-chart-block] svg')`)
	if got := b.eval(`(() => { const g = document.querySelector('[data-grid]').getBoundingClientRect(); const cells = [...document.querySelector('[data-grid]').children].map((c) => c.getBoundingClientRect());
		const top = cells.filter((c) => Math.abs(c.top - cells[0].top) < 2); return String(Math.abs(top[top.length - 1].right - g.right) < 2) })()`); got != "true" {
		t.Fatalf("the blocks of a row fill the width of the screen: %s", got)
	}
	b.shot("shell-schedule")
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
	b.until("the language of the interface on the interface tab", `document.querySelector('[data-look="language"] input[value="ru"]')`)
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

	b.call("browser_resize", map[string]any{"width": 1440, "height": 900})
	for _, look := range []string{"pixel vivid light", "neon tinted dark", "paper neutral light", "default vivid dark"} {
		parts := strings.Fields(look)
		b.eval(`(localStorage.setItem('deck-look', JSON.stringify({ ...JSON.parse(localStorage.getItem('deck-look')), theme: '` + parts[0] + `', surface: '` + parts[1] + `' })),
			localStorage.setItem('mantine-color-scheme-value', '` + parts[2] + `'), location.hash = '#/p/web/main', location.reload(), '')`)
		b.until("the look "+look, `document.documentElement.dataset.deckTheme === '`+parts[0]+`' && document.documentElement.dataset.deckSurface === '`+parts[1]+`' && document.querySelector('[data-block] table')`)
		b.shot("theme-" + parts[0] + "-" + parts[1])
	}
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the chat on a vivid surface", `document.querySelector('[data-chat-panel] [data-block]')`)
	if got := b.eval(`getComputedStyle(document.querySelector('[data-chat-panel] [data-block]')).backgroundColor`); got != "rgba(0, 0, 0, 0)" {
		t.Fatalf("the chat has one background, none of a block under its text: %s", got)
	}
	if got := b.eval(`getComputedStyle([...document.querySelectorAll('[data-block]')].find((x) => !x.closest('[data-chat-panel]'))).backgroundColor`); got == "rgba(0, 0, 0, 0)" {
		t.Fatalf("a block on the screen keeps its own background: %s", got)
	}
	b.eval(`(document.querySelector('[data-chat-close]').click(), '')`)
	b.eval(`(localStorage.setItem('deck-look', JSON.stringify({ ...JSON.parse(localStorage.getItem('deck-look')), theme: 'pixel', surface: 'neutral' })), location.hash = '#/deck/settings', location.reload(), '')`)
	b.until("the projects of settings in pixels", `document.documentElement.dataset.deckTheme === 'pixel' && document.querySelector('[data-item] [data-actions]')`)
	if got := b.eval(`(() => { const bs = [...document.querySelector('[data-item] [data-actions]').children].map((x) => x.getBoundingClientRect());
		return String(bs.every((r, i) => i === 0 || r.left - bs[i - 1].right >= 6)) })()`); got != "true" {
		t.Fatalf("the hard shadow of a pixel button leaves room before the next one: %s", got)
	}
	b.shot("theme-pixel-settings")
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.innerText.trim() === 'Interface').click(), '')`)
	b.until("no corners in pixels", `document.querySelector('[data-look="theme"]')?.offsetParent && !document.querySelector('[data-look="radius"]')`)
	b.eval(`(document.querySelector('[data-look="theme"] input[value="neon"]').click(), '')`)
	b.until("no scheme in neon, only its note", `!document.querySelector('[data-look="scheme"]') && document.querySelector('[data-look-note="scheme"]') && document.querySelector('[data-look="radius"]')`)
	e.must("deck.settings_set", map[string]any{"language": "ru"}, nil)
	b.until("the interface in the language the owner picked", `document.querySelector('[data-nav]')?.innerText.includes('Кокпит') && document.querySelector('[data-chat-toggle]')?.innerText.includes('Мастер')`)
	b.shot("shell-ru")
	b.open(e.URL + "/#/deck/journal?tab=Spend")
	b.until("a link to a tab opens it in any language", `document.querySelector('[role="tab"][aria-selected="true"]')?.innerText.includes('Расходы')`)
	b.eval(`([...document.querySelectorAll('[role="tab"]')].find((x) => x.innerText.includes('Вызовы')).click(), '')`)
	b.eval(`([...document.querySelectorAll('[role="tab"]')].find((x) => x.innerText.includes('Файлы')).click(), '')`)
	b.until("the address names the tab as written", `location.hash.endsWith('tab=Files')`)
	b.open(e.URL + "/#/deck/settings?tab=General")
	b.until("the settings named in the language of the owner", `document.body.innerText.includes('Язык интерфейса')`)
}
