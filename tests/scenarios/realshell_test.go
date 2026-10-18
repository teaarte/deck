package scenarios

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"

	"deck2/internal/provider"
)

// TestRealBrowserShell drives the islands of deck in a real browser, menu, chat and columns:
// DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowserShell -v.
func TestRealBrowserShell(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "score" {
			select {
			case <-hold:
			case <-ctx.Done():
			}
		}
		return webNotesAgent()(ctx, c)
	})
	manifest := strings.Replace(webNotes, `{"version": 12,`, `{"version": 12, "icon": "rocket",`, 1)
	manifest = strings.Replace(manifest, `{"type": "table", "title": "Notes",`, `{"type": "table", "title": "Notes", "icon": "notes",`, 1)
	manifest = strings.Replace(manifest, `"columns": [{"label": "Title", "bind": "title"}]`, `"columns": [{"label": "Title", "bind": "title"}, {"label": "Status", "bind": "source"}, {"label": "Note", "bind": "note"}]`, 1)
	manifest = strings.Replace(manifest, `{"name": "note", "title": "Note", "record": "notes",`, `{"name": "more", "title": "More", "blocks": [{"type": "list", "title": "Same", "source": {"collection": "notes"}, "item": {"title": "title"}}]},
    {"name": "note", "title": "Note", "record": "notes",`, 1)
	manifest = strings.Replace(manifest, `"menu": ["main"]`, `"menu": ["main", "more"]`, 1)
	e.createProject("web", strings.Replace(manifest, `{"label": "Open", "screen": "note"}`, `{"label": "Open", "icon": "arrow-right", "screen": "note"}`, 1))
	e.createProject("solo", webNotes)
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes",
		"records": jsonl(map[string]any{"title": "alpha", "source": "mail", "note": strings.Repeat("the model did not finish the self-check within the ceiling ", 6)}, map[string]any{"title": "a-very-long-title-without-any-spaces-that-would-push-the-table-out-of-its-block-" + strings.Repeat("x", 120), "source": "web"})}, nil)
	e.waitState(e.start("web", "tell", nil), "done")
	e.waitState(e.start("web", "score", map[string]any{"title": "x"}), "running")

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1440, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/deck/cockpit")
	b.until("the cockpit in its islands", `document.querySelector('[data-nav] [data-system]') && document.querySelector('[data-block]')`)
	if got := b.eval(`String(!!document.querySelector('header'))`); got != "false" {
		t.Fatalf("the desktop has no header bar: %s", got)
	}
	if got := b.eval(`(() => { const s = getComputedStyle(document.querySelector('[data-nav]')); const r = document.querySelector('[data-nav]').getBoundingClientRect();
		return [parseFloat(s.borderTopLeftRadius) > 0, s.boxShadow !== 'none', r.left > 0, r.top > 0].join(' ') })()`); got != "true true true true" {
		t.Fatalf("the menu is an island: rounded, with a shadow, apart from the edges: %s", got)
	}
	b.until("the icons of the menu", `document.querySelector('[data-nav] [data-system-head] [data-icon]') && document.querySelector('[data-group="web"] [data-icon="rocket"]')`)
	if got := b.eval(`[...document.querySelectorAll('[data-nav] [data-group]:not([data-system] [data-group])')].map((x) => x.dataset.group).sort().join(' ')`); got != "solo web" {
		t.Fatalf("the projects of the menu without deck and the master: %s", got)
	}
	if got := b.eval(`(() => { const h = document.querySelector('[data-system-head]'), a = document.querySelector('[data-system] a[href="#/deck/schedule"]');
		return [h.getAttribute('aria-expanded'), !a || a.getBoundingClientRect().height === 0].join(' ') })()`); got != "false true" {
		t.Fatalf("the foot of the menu is one folded button until a tap: %s", got)
	}
	b.shot("shell-system-folded")
	b.eval(`(document.querySelector('[data-system-head]').click(), '')`)
	b.until("deck's screens and the master's unfold at the foot of the menu", `document.querySelector('[data-system-head]').getAttribute('aria-expanded') === 'true' &&
		document.querySelector('[data-system] [data-group=""] a[href="#/deck/schedule"]')?.getBoundingClientRect().height > 0 && document.querySelector('[data-system] [data-group="settings"] a')`)
	if got := b.eval(`String([...document.querySelectorAll('[data-system] a')].some((a) => a.getAttribute('href').startsWith('#/p/master/') && a.getAttribute('href').split('?')[0].endsWith('/settings')))`); got != "false" {
		t.Fatalf("the settings of the master are not a quick link of the foot: %s", got)
	}
	b.shot("shell-system-menu")
	b.eval(`(document.querySelector('[data-system] a[href="#/deck/schedule"]').click(), '')`)
	b.until("the schedule from the foot of the menu", `location.hash === '#/deck/schedule'`)
	b.until("the tab named after its screen, with Deki for its icon", `/^(\(\d+\) )?Schedule · deck$/.test(document.title) && document.querySelector('link[rel=icon]').getAttribute('href') === '/favicon.png'`)
	if got := b.eval(`(() => { const s = getComputedStyle(document.querySelector('[data-system] a[data-active]')), h = getComputedStyle(document.querySelector('[data-nav] [data-group-head]'));
		const accent = getComputedStyle(document.documentElement).getPropertyValue('--mantine-primary-color-filled').trim(), probe = document.createElement('i');
		probe.style.color = accent; document.body.append(probe); const filled = getComputedStyle(probe).color; probe.remove();
		return [s.borderTopLeftRadius === h.borderTopLeftRadius, s.backgroundColor === filled].join(' ') })()`); got != "true true" {
		t.Fatalf("the link of the foot on its screen has the corners and the accent of the menu: %s", got)
	}
	b.eval(`(document.querySelector('[data-nav] [data-logo]').click(), '')`)
	b.until("the logo opens the cockpit", `location.hash === '#/deck/cockpit' && !document.querySelector('[data-system] a[data-active]')`)
	b.until("the counters of the cockpit", `document.querySelectorAll('[data-stat]').length === 1`)
	if got := b.eval(`(() => { const bs = [...document.querySelectorAll('[data-stat]')].map((x) => x.closest('[data-block]').getBoundingClientRect());
		return String(new Set(bs.map((r) => Math.round(r.top))).size === 1 && new Set(bs.map((r) => Math.round(r.height))).size === 1) })()`); got != "true" {
		t.Fatalf("the counters of the cockpit stand in one row of one height: %s", got)
	}
	if got := b.eval(`String([...document.querySelectorAll('[data-block]')].some((x) => x.innerText.includes('Nothing')))`); got != "false" {
		t.Fatalf("the cockpit hides its empty lists: %s", got)
	}
	b.until("the sky of juce behind the shell", `document.documentElement.dataset.deckTheme === 'juce' && document.querySelectorAll('[data-backdrop] [data-cloud]').length === 2`)
	b.until("Deki at work while a process runs", `document.querySelector('[data-hero="full"] [data-deki="work"]')`)
	b.shot("shell-cockpit")
	b.until("a new notification on the bell", `document.querySelector('[data-bell-count="1"]')`)
	b.until("the count of new notifications ahead of the tab's name", `/^\(1\) .+ · deck$/.test(document.title)`)
	b.eval(`(document.querySelector('[data-bell]').click(), '')`)
	b.until("the notification in the list of the bell", `document.querySelector('[data-bell-list] [data-note]')?.innerText.includes('alpha scored high')`)
	b.until("an Open button on a note with a screen", `document.querySelector('[data-bell-list] [data-note] [data-note-open]')`)
	b.shot("shell-bell")
	b.until("the bell has nothing new once seen", `!document.querySelector('[data-bell-count]')`)
	b.until("a button of a notification with its icon", `document.querySelector('[data-bell-list] [data-note] [data-icon="hand-finger"]')`)
	b.eval(`([...document.querySelectorAll('[data-bell-list] button')].find((x) => x.innerText.includes('Touch it')).click(), '')`)
	b.until("the button asks first, as a button of a screen does", `document.querySelector('[data-confirm-yes]')`)
	b.eval(`(document.querySelector('[data-confirm-yes]').click(), '')`)
	e.waitFor("the call of the button", func() bool {
		for _, p := range e.processes() {
			if p.Command == "touch" {
				return true
			}
		}
		return false
	})
	if b.eval(`String(!!document.querySelector('[data-bell-list]'))`) != "true" {
		b.eval(`(document.querySelector('[data-bell]').click(), '')`)
	}
	b.until("a note to mark read", `document.querySelector('[data-bell-list] [data-note-read]')`)
	b.eval(`(document.querySelector('[data-bell-list] [data-note-read]').click(), '')`)
	b.until("the note marked read", `document.querySelector('[data-bell-list] [data-note][data-read]') && !document.querySelector('[data-bell-list] [data-note-read]')`)
	if b.eval(`String(!!document.querySelector('[data-bell-list]'))`) == "true" {
		b.eval(`(document.querySelector('[data-bell]').click(), '')`)
	}

	b.eval(`(document.querySelector('[data-group="web"] [data-group-head]').click(), '')`)
	b.until("the group of web folded", `!document.querySelector('[data-group="web"] a:not([data-extra])')?.offsetParent && document.querySelector('[data-group="web"] [data-group-head]').getAttribute('aria-expanded') === 'false'`)
	b.eval(`(document.querySelector('[data-group="web"] [data-group-head]').click(), '')`)
	b.until("the group of web open again", `document.querySelector('[data-group="web"] a:not([data-extra])')?.offsetParent`)

	b.eval(`(document.querySelector('[data-group="solo"] a[data-group-head]').click(), '')`)
	b.until("a project with one screen opens it from its header, which shows it open", `location.hash === '#/p/solo/main' &&
		document.querySelector('[data-group="solo"] a[data-group-head][data-active]') && !document.querySelector('[data-group="solo"] [aria-expanded]')`)
	b.shot("shell-one-screen")

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

	b.open(e.URL + "/#/deck/spend?tab=30 days")
	b.until("the stats and the chart of the spend", `[...document.querySelectorAll('[data-stat]')].filter((x) => x.offsetParent).length === 4 && [...document.querySelectorAll('[data-chart-block] svg')].some((x) => x.getBoundingClientRect().width > 0)`)
	if got := b.eval(`String(new Set([...document.querySelectorAll('[data-stat]')].filter((x) => x.offsetParent).map((x) => Math.round(x.closest('[data-block]').getBoundingClientRect().top))).size)`); got != "1" {
		t.Fatalf("the four numbers of the spend stand in one row: %s", got)
	}
	b.shot("shell-spend")
	if got := b.eval(`(() => { const g = [...document.querySelectorAll('[data-grid]')].find((x) => x.offsetParent).getBoundingClientRect(); const cells = [...[...document.querySelectorAll('[data-grid]')].find((x) => x.offsetParent).children].map((c) => c.getBoundingClientRect());
		const top = cells.filter((c) => Math.abs(c.top - cells[0].top) < 2); return String(Math.abs(top[top.length - 1].right - g.right) < 2) })()`); got != "true" {
		t.Fatalf("the blocks of a row fill the width of the screen: %s", got)
	}
	b.open(e.URL + "/#/deck/schedule")
	b.until("the schedule of a trigger in words", `[...document.querySelectorAll('tr')].some((r) => r.offsetParent && r.innerText.toLowerCase().includes('master') && r.innerText.includes('review'))`)
	if got := b.eval(`[...document.querySelectorAll('td')].filter((x) => x.offsetParent && /^[0-9*\/,-]+( [0-9*\/,-]+){4}$/.test(x.innerText.trim())).map((x) => x.innerText).join('|')`); got != "" {
		t.Fatalf("no schedule shows as cron: %s", got)
	}
	b.shot("shell-schedule")
	b.open(e.URL + "/#/deck/settings")
	b.until("the tabs of settings", `[...document.querySelectorAll('[role=tab]')].some((x) => x.innerText.trim() === 'Models')`)
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.innerText.trim() === 'Models').click(), '')`)
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
	b.look("theme", "glass")
	b.look("scheme", "dark")
	b.until("the dark glass", `document.documentElement.dataset.deckTheme === 'glass' && document.documentElement.dataset.mantineColorScheme === 'dark'`)
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.innerText.trim() === 'Interface').click(), '')`)
	b.until("the tint slider of the glass", `document.querySelector('[data-look="tint"]')?.offsetParent`)
	b.until("the language of the interface on the interface tab", `document.querySelector('[data-look="language"]')`)
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { const t = await page.locator('[data-look="tint"] [role=slider]'); await t.focus(); await page.keyboard.press('End'); }`})
	b.until("opaque glass at the end of the slider", `getComputedStyle(document.documentElement).getPropertyValue('--glass-tint-user').trim() === '1' && /rgb\(\d+, \d+, \d+\)$/.test(getComputedStyle(document.querySelector('[data-nav]')).backgroundColor)`)
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { const t = await page.locator('[data-look="tint"] [role=slider]'); await t.focus(); await page.keyboard.press('Home'); }`})
	b.eval(`(document.querySelector('[data-look="pattern"]').click(), '')`)
	b.until("no pattern when the owner turns it off", `!document.querySelector('[data-backdrop] > div')`)
	b.eval(`(document.querySelector('[data-look="pattern"]').click(), '')`)
	b.until("the pattern back", `document.querySelectorAll('[data-backdrop] > div').length === 2`)
	b.eval(`(document.querySelector('[data-look="mascot"]').click(), '')`)
	b.until("no Deki anywhere once the mascot is off", `!document.querySelector('[data-deki]')`)
	b.eval(`(document.querySelector('[data-look="mascot"]').click(), '')`)
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
	b.look("scheme", "light")
	b.eval(`(document.querySelector('[data-accent="grape"]').click(), '')`)
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
	b.until("the menu on a phone", `document.querySelector('[data-nav-phone] [data-system]')?.offsetParent`)
	b.shot("shell-phone-menu")
	b.eval(`(document.querySelector('[data-nav-phone] [data-system-head]').click(), '')`)
	b.until("the foot folds on a tap on a phone", `document.querySelector('[data-nav-phone] [data-system-head]').getAttribute('aria-expanded') === 'false'`)
	b.shot("shell-phone-menu-folded")

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
	b.look("theme", "neon")
	b.until("no scheme in neon, only its note", `!document.querySelector('[data-look="scheme"]') && document.querySelector('[data-look-note="scheme"]') && document.querySelector('[data-look="radius"]')`)
	e.must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
	b.until("the interface in the language the owner picked", `document.querySelector('[data-nav] [data-system]')?.innerText.includes('Настройки') && document.querySelector('[data-chat-toggle]')?.innerText.includes('Деки')`)
	b.shot("shell-ru")
	b.open(e.URL + "/#/deck/spend?tab=7 days")
	b.until("a link to a tab opens it in any language", `document.querySelector('[role="tab"][aria-selected="true"]')?.innerText.includes('7 дней')`)
	b.eval(`([...document.querySelectorAll('[role="tab"]')].find((x) => x.innerText.includes('30 дней')).click(), '')`)
	b.until("the address names the tab as written", `/tab=30(\+|%20| )days$/.test(location.hash)`)
	b.open(e.URL + "/#/deck/settings?tab=Advanced")
	b.until("the settings named in the language of the owner", `document.body.innerText.includes('Сколько агентских процессов')`)
}
