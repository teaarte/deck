package browser

import (
	"context"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestRealBrowserShell drives the frame of deck in a real browser, tiles, tabs, chat and columns:
// DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowserShell -v.
func TestRealBrowserShell(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	hold := make(chan struct{})
	t.Cleanup(func() { close(hold) })
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "score" {
			select {
			case <-hold:
			case <-ctx.Done():
			}
		}
		return harness.WebNotesAgent()(ctx, c)
	}))

	manifest := strings.Replace(harness.WebNotes, harness.Manifest(`{`), harness.Manifest(`{"icon": "rocket",`), 1)
	manifest = strings.Replace(manifest, `{"type": "table", "title": "Notes",`, `{"type": "table", "title": "Notes", "icon": "notes",`, 1)
	manifest = strings.Replace(manifest, `"columns": [{"label": "Title", "bind": "title"}]`, `"columns": [{"label": "Title", "bind": "title"}, {"label": "Status", "bind": "source"}, {"label": "Note", "bind": "note"}]`, 1)
	manifest = strings.Replace(manifest, `{"name": "note", "title": "Note", "record": "notes",`, `{"name": "more", "title": "More", "blocks": [{"type": "list", "title": "Same", "source": {"collection": "notes"}, "item": {"title": "title"}}]},
    {"name": "note", "title": "Note", "record": "notes",`, 1)
	manifest = strings.Replace(manifest, `"menu": ["main"]`, `"menu": ["main", "more"]`, 1)
	e.Project("web", strings.Replace(manifest, `{"label": "Open", "screen": "note"}`, `{"label": "Open", "icon": "arrow-right", "screen": "note"}`, 1))
	e.Project("solo", harness.WebNotes)
	for _, n := range []string{"glass", "pixel", "neon", "paper", "default"} {
		e.Example(n)
	}
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes",
		"records": harness.Jsonl(map[string]any{"title": "alpha", "source": "mail", "note": strings.Repeat("the model did not finish the self-check within the ceiling ", 6)}, map[string]any{"title": "a-very-long-title-without-any-spaces-that-would-push-the-table-out-of-its-block-" + strings.Repeat("x", 120), "source": "web"})}, nil)
	e.WaitState(e.Start("web", "tell", nil), "done")
	e.WaitState(e.Start("web", "score", map[string]any{"title": "x"}), "running")

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1440, "height": 900})
	b.Open(e, "")
	b.until("the tiles are the home of deck", `document.querySelector('[data-apps]') && document.querySelector('[data-module="web"]') && ![...document.querySelectorAll('[data-block]')].some((x) => !x.closest('[data-widget]'))`)
	if got := b.eval(`[!document.querySelector('header'), !document.querySelector('[data-nav]'), !document.querySelector('[data-apps-home]'), !!document.querySelector('[data-toolbar] [data-chat-toggle]'), !document.querySelector('[data-phone-tabs]')].join(' ')`); got != "true true true true true" {
		t.Fatalf("the desktop has no header bar and no side menu, only Deki over the tiles: %s", got)
	}
	if got := b.eval(`[...document.querySelectorAll('[data-module]')].map((x) => x.dataset.module).sort().join(' ')`); got != "solo web" {
		t.Fatalf("the tiles of the modules without deck and the master: %s", got)
	}
	b.until("the icon of a module without a cover on its tile", `document.querySelector('[data-module="web"] [data-app-tile-icon] [data-icon="rocket"]')`)
	b.shot("shell-tiles")
	if got := b.eval(`String(!document.querySelector('[data-apps-links]'))`); got != "true" {
		t.Fatalf("no links under the tiles: %s", got)
	}
	b.open(e.URL + "/#/deck/schedule")
	b.until("the schedule", `location.hash === '#/deck/schedule'`)
	b.until("the tab named after its screen, with Deki for its icon", `/^(\(\d+\) )?Schedule · deck$/.test(document.title) && document.querySelector('link[rel=icon]').getAttribute('href') === '/favicon.png'`)
	if got := b.eval(`(() => { const home = document.querySelector('[data-apps-home]').getBoundingClientRect(), deki = document.querySelector('[data-chat-toggle]').getBoundingClientRect();
		const tabs = [...document.querySelectorAll('[data-places] a')].map((a) => a.getAttribute('href'));
		return [home.left < 100, deki.right > innerWidth - 100, tabs[0] === '#/deck/cockpit', document.querySelector('[data-places] a[aria-current=page]').getAttribute('href')].join(' ') })()`); got != "true true true #/deck/schedule" {
		t.Fatalf("the way to the tiles on the left, Deki on the right, deck's screens as tabs under them, Today first: %s", got)
	}
	b.eval(`(document.querySelector('[data-places] a[href="#/deck/cockpit"]').click(), '')`)
	b.until("Today from its tab", `location.hash === '#/deck/cockpit'`)
	b.until("what runs as a chip and the first line of Today", `document.querySelector('[data-chips] [data-chip]')?.innerText.includes('Score') && document.querySelector('[data-today-line]') && document.querySelector('[data-today-date]')`)
	if got := b.eval(`String(document.querySelectorAll('[data-stat]').length)`); got != "0" {
		t.Fatalf("the cockpit counts once, in its top, with no tiles that say it again: %s", got)
	}
	if got := b.eval(`(() => { const grid = document.querySelector('[data-grid]').getBoundingClientRect();
		const history = document.querySelector('[data-screen-head] [data-dialog-open="changes"]')?.getBoundingClientRect();
		return String(!!history && history.bottom <= grid.top) })()`); got != "true" {
		t.Fatalf("the history of Today is in its head: %s", got)
	}
	b.until("the sky of juce behind the shell", `document.documentElement.dataset.deckTheme === 'juce' && document.querySelector('[data-backdrop] [data-backdrop-picture]')`)
	b.shot("shell-cockpit")
	b.until("the count of new notifications ahead of the tab's name", `/^\(1\) .+ · deck$/.test(document.title)`)

	b.eval(`(document.querySelector('[data-apps-home]').click(), '')`)
	b.until("the tiles again", `document.querySelector('[data-module="web"]')`)
	b.eval(`(document.querySelector('[data-app="web"]').click(), '')`)
	b.until("a tile opens its module with its screens as tabs", `location.hash === '#/p/web/main' && document.querySelector('[data-places] a[href="#/p/web/more"]') && document.querySelector('[data-places] a[aria-current=page]')?.getAttribute('href') === '#/p/web/main'`)
	b.until("what the module does now in the head of its screen", `document.querySelector('[data-screen-head] [data-module-lead] [data-doing]')?.innerText.includes('Score')`)
	if got := b.eval(`(() => { const tab = document.querySelector('[data-places] a[aria-current=page]').innerText.trim();
		return [document.querySelector('[data-screen-head] h2')?.innerText.trim() === tab, !!document.querySelector('[data-assign]')].join(' ') })()`); got != "false false" {
		t.Fatalf("the screen does not repeat its tab as its title, and no button only opens the chat: %s", got)
	}
	b.shot("shell-module")
	b.until("the rows of the notes", `[...document.querySelectorAll('[data-block] tr button')].some((x) => x.innerText.includes('Open'))`)
	if got := b.eval(`String(!document.querySelector('[data-back]'))`); got != "true" {
		t.Fatalf("no way back on a screen of tabs: %s", got)
	}
	b.eval(`([...document.querySelectorAll('[data-block] tr button')].find((x) => x.innerText.includes('Open')).click(), '')`)
	b.until("an open record has the way back", `/^#\/p\/web\/note\/\d+/.test(location.hash) && document.querySelector('[data-back]')`)
	b.eval(`(document.querySelector('[data-back]').click(), '')`)
	b.until("back to the list", `location.hash === '#/p/web/main'`)

	b.open(e.URL + "/#/p/web/main")
	b.until("the project screen", `document.querySelector('[data-block] table')`)
	if got := b.eval(`(() => { const t = [...document.querySelectorAll('[data-block]')].find((x) => x.querySelector('table')); return String(t.scrollWidth <= t.clientWidth + 1) })()`); got != "true" {
		t.Fatalf("a long word stays inside its block: %s", got)
	}
	if got := b.eval(`[...document.querySelectorAll('th')].filter((x) => x.offsetParent && x.getBoundingClientRect().height > 48).map((x) => x.innerText).join('|')`); got != "" {
		t.Fatalf("a header of a table beside a long column keeps its words whole: %s", got)
	}
	b.until("the icons of a block and its button", `document.querySelector('[data-block-icon] [data-icon="notes"]') && document.querySelector('button [data-icon="arrow-right"]')`)
	b.shot("shell-light")

	b.open(e.URL + "/#/deck/spend?tab=30 days")
	b.until("the stats and the chart of the spend", `[...document.querySelectorAll('[data-stat]')].filter((x) => x.offsetParent).length === 2 && [...document.querySelectorAll('[data-chart-block] svg')].some((x) => x.getBoundingClientRect().width > 0)`)
	if got := b.eval(`String(new Set([...document.querySelectorAll('[data-stat]')].filter((x) => x.offsetParent).map((x) => Math.round(x.closest('[data-block]').getBoundingClientRect().top))).size)`); got != "1" {
		t.Fatalf("the two numbers of the spend stand in one row: %s", got)
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
	b.open(e.URL + "/#/deck/settings?tab=Modules")
	b.until("the tabs of settings", `[...document.querySelectorAll('[role=tab]')].some((x) => x.innerText.trim() === 'Models')`)
	b.eval(`([...document.querySelectorAll('[role=tab]')].find((x) => x.innerText.trim() === 'Models').click(), '')`)
	b.until("the providers table", `[...document.querySelectorAll('[data-block] table')].some((x) => x.offsetParent)`)
	if got := b.eval(`[...document.querySelectorAll('th')].filter((x) => x.offsetParent && x.getBoundingClientRect().height > 48).map((x) => x.innerText).join('|')`); got != "" {
		t.Fatalf("a header of a table keeps its words whole: %s", got)
	}

	b.open(e.URL + "/#/p/web/main")
	b.until("the project screen", `document.querySelector('[data-block] table')`)
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

	b.open(e.URL + "/#/deck/look")
	b.until("the appearance tab", `document.querySelector('[data-look="theme"]')`)
	b.look("theme", "glass")
	b.look("scheme", "dark")
	b.until("the dark glass", `document.documentElement.dataset.deckTheme === 'glass' && document.documentElement.dataset.mantineColorScheme === 'dark'`)
	b.until("the language of the interface on the interface tab", `document.querySelector('[data-look="language"]')`)
	b.open(e.URL + "/#/deck/settings")
	b.until("the glass of the theme document on the tabs", `getComputedStyle(document.querySelector('.mantine-Tabs-list')).backdropFilter.includes('blur') && document.getElementById('deck-theme').textContent.includes('--theme-blur')`)
	var worn struct{ Name string }
	e.Must("deck.theme", map[string]any{}, &worn)
	if worn.Name != "glass" {
		t.Fatalf("the theme is the whole deck's, kept on the server: %+v", worn)
	}
	b.open(e.URL + "/#/deck/look")
	b.until("the switch of the mascot", `document.querySelector('[data-look="mascot"]')`)
	b.eval(`(document.querySelector('[data-look="mascot"]').click(), '')`)
	b.until("no Deki anywhere once the mascot is off", `!document.querySelector('[data-deki]')`)
	b.eval(`(document.querySelector('[data-look="mascot"]').click(), '')`)
	b.open(e.URL + "/#/p/web/main")
	b.until("the dark project screen", `document.querySelector('[data-block] table')`)
	b.shot("shell-glass-dark")
	b.eval(`(document.querySelector('[data-chat-close]').click(), '')`)
	b.open(e.URL + "/#/deck/cockpit")
	b.until("the dark cockpit", `document.querySelector('[data-today-line]') && document.querySelector('[data-grid] [data-block]')`)
	b.shot("shell-glass-dark-cockpit")
	b.open(e.URL + "/#/deck/look")
	b.until("the appearance tab", `document.querySelector('[data-look="scheme"]')`)
	b.look("scheme", "light")
	b.eval(`(document.querySelector('[data-accent="grape"]').click(), '')`)
	b.open(e.URL + "/#/p/web/main")
	b.until("the light glass", `document.querySelector('[data-block] table') && document.documentElement.dataset.mantineColorScheme === 'light'`)
	b.shot("shell-glass-light")

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(e.URL + "/#/p/web/main")
	b.until("the tabs of the phone", `document.querySelector('[data-phone-tabs]') && document.querySelector('[data-block] table')`)
	if got := b.eval(`String(document.documentElement.scrollWidth <= innerWidth + 1)`); got != "true" {
		t.Fatalf("the screen fits the width of a phone: %s", got)
	}
	if got := b.eval(`(() => { const t = document.querySelector('[data-phone-tabs]').getBoundingClientRect(), last = [...document.querySelectorAll('[data-block]')].at(-1);
		const labels = [...document.querySelectorAll('[data-phone-tabs] [data-tab]')].map((x) => x.querySelector('span:last-child'));
		return [document.querySelectorAll('[data-phone-tabs] [data-tab]').length, innerHeight - t.bottom >= 14, labels.every((l) => l.scrollWidth <= l.clientWidth),
			document.querySelector('[data-tab="apps"]').hasAttribute('data-active'), !document.querySelector('[data-toolbar]'), document.querySelector('[data-phone-title]')?.innerText].join(' ') })()`); got != "5 true true true true web" {
		t.Fatalf("five tabs at the foot of a phone, whole words, Modules active in a module, the title of the module on top: %s", got)
	}
	b.shot("shell-phone")
	b.eval(`(document.querySelector('[data-tab="more"]').click(), '')`)
	b.until("the rest of deck behind More", `['#/deck/spend', '#/deck/files', '#/deck/settings'].every((h) => document.querySelector('[data-more] a[href="' + h + '"]')) && !document.querySelector('[data-more] a[href="#/deck/schedule"]')`)
	b.shot("shell-phone-more")
	b.eval(`(document.querySelector('[data-more] a[href="#/deck/spend"]').click(), '')`)
	b.until("Spend from More, More active", `location.hash.startsWith('#/deck/spend') && document.querySelector('[data-tab="more"]').hasAttribute('data-active')`)
	b.eval(`(document.querySelector('[data-tab="deki"]').click(), '')`)
	b.until("Deki opens the chat as a sheet", `document.querySelector('[data-chat-phone] [data-chat-input]')`)
	b.eval(`(document.querySelector('[data-chat-close]').click(), '')`)
	b.open(e.URL + "/#/deck/cockpit")
	b.until("the cockpit on a phone", `document.querySelector('[data-today-line]') && document.querySelector('[data-grid] [data-block]')`)
	if got := b.eval(`String(document.documentElement.scrollWidth <= innerWidth + 1)`); got != "true" {
		t.Fatalf("the cockpit fits the width of a phone: %s", got)
	}
	b.shot("shell-phone-cockpit")

	b.call("browser_resize", map[string]any{"width": 1440, "height": 900})
	for _, look := range []string{"pixel light", "neon dark", "paper light", "default dark", "juce dark"} {
		parts := strings.Fields(look)
		b.open(e.URL + "/?theme=" + parts[0] + "&scheme=" + parts[1] + "#/p/web/main")
		b.until("the preview of "+look, `document.documentElement.dataset.deckTheme === '`+parts[0]+`' && document.documentElement.dataset.mantineColorScheme === '`+parts[1]+`' && document.querySelector('[data-block] table')`)
		b.shot("theme-" + parts[0] + "-" + parts[1])
	}
	if got := b.eval(`String(!!document.querySelector('[data-backdrop] [data-backdrop-picture]'))`); got != "true" {
		t.Fatalf("the clouds of juce are its picture behind the page: %s", got)
	}
	var cur struct{ Name string }
	e.Must("deck.theme", map[string]any{}, &cur)
	if cur.Name != "glass" {
		t.Fatalf("a preview changes no theme: %+v", cur)
	}
	b.eval(`(document.querySelector('[data-chat-toggle]').click(), '')`)
	b.until("the chat beside the screen", `document.querySelector('[data-chat-panel] [data-block]')`)
	if got := b.eval(`getComputedStyle(document.querySelector('[data-chat-panel] [data-block]')).backgroundColor`); got != "rgba(0, 0, 0, 0)" {
		t.Fatalf("the chat has one background, none of a block under its text: %s", got)
	}
	if got := b.eval(`getComputedStyle([...document.querySelectorAll('[data-block]')].find((x) => !x.closest('[data-chat-panel]'))).backgroundColor`); got == "rgba(0, 0, 0, 0)" {
		t.Fatalf("a block on the screen keeps its own background: %s", got)
	}
	b.eval(`(document.querySelector('[data-chat-close]').click(), '')`)
	b.open(e.URL + "/#/deck/settings?tab=Modules")
	b.look("theme", "pixel")
	b.open(e.URL + "/#/deck/settings?tab=Modules")
	b.until("the projects of settings in pixels", `document.documentElement.dataset.deckTheme === 'pixel' && document.querySelector('[data-item] [data-actions]')`)
	if got := b.eval(`(() => { const bs = [...document.querySelector('[data-item] [data-actions]').children].map((x) => x.getBoundingClientRect());
		return String(bs.every((r, i) => i === 0 || r.left - bs[i - 1].right >= 6)) })()`); got != "true" {
		t.Fatalf("the hard shadow of a pixel button leaves room before the next one: %s", got)
	}
	b.shot("theme-pixel-settings")
	b.look("theme", "neon")
	b.until("no scheme in neon, only its note", `!document.querySelector('[data-look="scheme"]') && document.querySelector('[data-look-note="scheme"]')?.innerText.includes('Neon') && document.documentElement.dataset.mantineColorScheme === 'dark'`)
	b.look("theme", "juce")
	b.until("juce gives the device its accent", `JSON.parse(localStorage.getItem('deck-look')).accent === 'coral'`)
	e.Must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
	b.until("the interface in the language the owner picked", `document.querySelector('[data-places]')?.innerText.includes('Настройки') && document.querySelector('[data-places]')?.innerText.includes('Сегодня') && document.querySelector('[data-chat-toggle]')?.innerText.includes('Деки')`)
	b.shot("shell-ru")
	b.open(e.URL + "/#/deck/spend?tab=7 days")
	b.until("a link to a tab opens it in any language", `document.querySelector('[role="tab"][aria-selected="true"]')?.innerText.includes('7 дней')`)
	b.eval(`([...document.querySelectorAll('[role="tab"]')].find((x) => x.innerText.includes('30 дней')).click(), '')`)
	b.until("the address names the tab as written", `/tab=30(\+|%20| )days$/.test(location.hash)`)
	b.open(e.URL + "/#/deck/settings?tab=Advanced")
	b.until("the settings named in the language of the owner", `document.body.innerText.includes('Сколько агентских процессов')`)
	e.Must("deck.notify", map[string]any{"project": "web", "text": "one more note"}, nil)
	b.open(e.URL + "/#/deck/cockpit")
	readAll := `document.querySelector('[data-block] [data-verb="read_all"]')`
	b.until("the button that reads every note in the head of the feed", readAll)
	b.eval(`(` + readAll + `.click(), '')`)
	b.until("the owner asked before every note is read", `document.querySelector('[data-confirm-yes]')`)
	b.eval(`(document.querySelector('[data-confirm-yes]').click(), '')`)
	b.until("no note left to read", `!`+readAll)
}
