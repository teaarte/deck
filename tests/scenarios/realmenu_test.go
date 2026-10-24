package scenarios

import (
	"context"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"deck2/internal/provider"
)

// TestRealBrowserMenu: the arrow of Control stands in the middle of its button, its title whole, on a desktop and a phone:
// DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowserMenu -v.
func TestRealBrowserMenu(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, ok)
	e.createProject("notes", notesManifest)
	e.createProject("plain", plainManifest)
	b := newPlaywright(t)
	centered := func(scope string) string {
		return `(() => { const h = document.querySelector('` + scope + ` [data-system-head]'), a = h.querySelector('[data-chevron]').getBoundingClientRect(), r = h.getBoundingClientRect();
			const l = h.querySelector('[class*=linkTitle]');
			return Math.abs((a.left + a.right) / 2 - (r.left + r.right) / 2) <= 1 && l.scrollWidth <= l.clientWidth })()`
	}
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/deck/cockpit")
	b.until("the arrow of Control in the middle of its button", centered("[data-nav]"))
	b.shot("menu-control")
	b.eval(`(document.querySelector('[data-nav] [data-system-head]').click(), '')`)
	b.until("the foot unfolded", `document.querySelector('[data-nav] [data-system-head]').getAttribute('aria-expanded') === 'true'`)
	b.shot("menu-control-open")

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(e.URL + "/#/deck/cockpit")
	b.until("the phone bar", `document.querySelector('[data-phone-bar]')`)
	b.eval(`(document.querySelector('[data-nav-open]').click(), '')`)
	b.until("the arrow of Control in the middle of its button on a phone", `document.querySelector('[data-nav-phone] [data-system]')?.offsetParent && `+centered("[data-nav-phone]"))
	b.shot("menu-control-phone")
}

// TestRealBrowserCockpitHistory: History stands in the top of the cockpit clear of Deki and keeps its place:
// DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowserCockpitHistory -v.
func TestRealBrowserCockpitHistory(t *testing.T) {
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
	e.createProject("web", webNotes)
	b := newPlaywright(t)
	at := `JSON.stringify(document.querySelector('[data-hero] [data-dialog-open="changes"]')?.getBoundingClientRect())`
	for _, size := range []struct {
		name          string
		width, height int
	}{{"desktop", 1400, 900}, {"phone", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.width, "height": size.height})
		b.open(e.URL + "/?token=" + token + "#/deck/cockpit")
		b.until("History in the top of the cockpit", `document.querySelector('[data-hero] [data-dialog-open="changes"]') && !document.querySelector('[data-grid] [data-dialog-open]')`)
		before := b.eval(at)
		if got := b.eval(`(() => { const h = document.querySelector('[data-hero] [data-dialog-open="changes"]').getBoundingClientRect(), d = document.querySelector('[data-hero] [data-deki]')?.getBoundingClientRect();
			return [!document.querySelector('[data-hero-talk]'), !d || h.right <= d.left || h.left >= d.right || h.bottom <= d.top || h.top >= d.bottom].join(' ') })()`); got != "true true" {
			t.Fatalf("History stands where Tell Deki stood, clear of Deki, on a %s: %s", size.name, got)
		}
		b.shot("cockpit-history-" + size.name)
		id := e.start("web", "score", map[string]any{"title": "x"})
		e.waitState(id, "running")
		b.until("the running list on the cockpit", `[...document.querySelectorAll('[data-block]')].some((x) => x.innerText.includes('Running now'))`)
		if after := b.eval(at); after != before {
			t.Fatalf("History keeps its place on a %s when a list comes in: %s, then %s", size.name, before, after)
		}
		b.shot("cockpit-history-" + size.name + "-running")
		e.must("deck.process_stop", map[string]any{"process": id}, nil)
		_, tok := e.token("ask-"+size.name, "deck.model_set")
		if code, out := e.callWith(tok, "deck.model_set", map[string]any{"connection": "none", "model": size.name}); code != 200 || !strings.Contains(string(out), `"waiting"`) {
			t.Fatalf("a token's model waits for the owner: %d %s", code, out)
		}
		b.until("the decision on the cockpit", `document.querySelector('[data-triage-open]')`)
		if after := b.eval(at); after != before {
			t.Fatalf("History keeps its place on a %s when a decision comes in: %s, then %s", size.name, before, after)
		}
		b.eval(`(document.querySelector('[data-hero] [data-dialog-open="changes"]').click(), '')`)
		b.until("the history open", `document.querySelector('[data-dialog="changes"]') && location.hash.includes('dialog=changes')`)
	}
}

// TestRealBrowserMenuOrder: the arrows of the order mode move a project in the side list on a desktop and a phone,
// and the order stays after a reload: DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowserMenuOrder -v.
func TestRealBrowserMenuOrder(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, ok)
	for _, name := range []string{"alpha", "beta", "gamma"} {
		e.createProject(name, plainManifest)
	}
	b := newPlaywright(t)
	order := func(scope, want string) string {
		return `[...document.querySelectorAll('` + scope + ` [data-group]:not([data-system] [data-group])')].map((x) => x.dataset.group).join(' ') === '` + want + `'`
	}
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/deck/cockpit")
	b.until("the projects by name", order("[data-nav]", "alpha beta gamma"))
	b.eval(`(document.querySelector('[data-nav] [data-nav-order]').click(), '')`)
	b.until("the arrows of the order", `document.querySelector('[data-nav] [data-order="alpha"] [data-move="up"]')?.disabled`)
	b.shot("menu-order")
	b.eval(`(document.querySelector('[data-nav] [data-order="alpha"] [data-move="down"]').click(), '')`)
	b.until("alpha moved down", order("[data-nav]", "beta alpha gamma"))
	b.open(e.URL + "/?reload=1#/deck/cockpit")
	b.until("the order after a reload", order("[data-nav]", "beta alpha gamma"))
	if names, _ := e.side(); !slices.Equal(names, []string{"beta", "alpha", "gamma"}) {
		t.Fatalf("deck keeps the order of the browser: %v", names)
	}
	b.eval(`(document.querySelector('[data-nav] [data-nav-order]').click(), '')`)
	for _, want := range []string{"beta gamma alpha", "gamma beta alpha"} {
		b.until("the arrow of gamma ready", `document.querySelector('[data-nav] [data-order="gamma"] [data-move="up"]')?.disabled === false`)
		b.eval(`(document.querySelector('[data-nav] [data-order="gamma"] [data-move="up"]').click(), '')`)
		b.until("gamma up to "+want, order("[data-nav]", want))
	}

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(e.URL + "/?reload=2#/deck/cockpit")
	b.until("the phone bar", `document.querySelector('[data-phone-bar]')`)
	b.eval(`(document.querySelector('[data-nav-open]').click(), '')`)
	b.until("the order on a phone", `document.querySelector('[data-nav-phone] [data-nav-order]')?.offsetParent && `+order("[data-nav-phone]", "gamma beta alpha"))
	b.eval(`(document.querySelector('[data-nav-phone] [data-nav-order]').click(), '')`)
	b.until("the arrows on a phone", `document.querySelector('[data-nav-phone] [data-order="alpha"] [data-move="up"]')`)
	b.eval(`(document.querySelector('[data-nav-phone] [data-order="alpha"] [data-move="up"]').click(), '')`)
	b.until("alpha moved up on a phone", order("[data-nav-phone]", "gamma alpha beta"))
	b.shot("menu-order-phone")
	b.open(e.URL + "/?reload=3#/deck/cockpit")
	b.eval(`(document.querySelector('[data-nav-open]').click(), '')`)
	b.until("the order of the phone after a reload", order("[data-nav-phone]", "gamma alpha beta"))
}

// TestRealBrowserCards: the cards look opens on cards instead of the menu, the cockpit first, a module with its cover;
// a card opens its module and the way home goes back: DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowserCards -v.
func TestRealBrowserCards(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := newEnv(t, ok)
	e.createProject("notes", notesManifest)
	e.createProject("plain", plainManifest)
	png := e.upload("notes", "cover.png", "image/png", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xcf\xc0\xf0\x1f\x00\x05\x00\x01\xff\x89\x99=\x1d\x00\x00\x00\x00IEND\xaeB`\x82")
	e.must("deck.project_cover", map[string]any{"project": "notes", "file": strconv.FormatInt(png, 10)}, nil)
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/deck/cockpit")
	b.until("the client", `document.querySelector('[data-nav]')`)
	b.eval(`(localStorage.setItem('deck-look', JSON.stringify({home: 'cards'})), location.hash = '', location.reload(), '')`)
	b.until("the cards instead of the menu", `document.querySelector('[data-apps]') && !document.querySelector('[data-nav]') && location.hash === '#/deck/apps'`)
	b.until("the cockpit first, then the modules, one with its cover", `(() => { const c = [...document.querySelectorAll('[data-app]')].map((a) => a.dataset.app);
		return c[0] === 'cockpit' && c.includes('notes') && c.includes('plain') && document.querySelector('[data-app=notes]').closest('[data-app-tile]').dataset.cover === 'true' })()`)
	b.shot("cards")
	b.until("Deki on the cockpit's card in the pose of the pulse", `document.querySelector('[data-app=cockpit] ~ [data-card-deki] [data-deki]')`)
	b.eval(`(document.querySelector('[data-card-deki]').click(), '')`)
	b.until("Deki opens the chat with the master", `document.querySelector('[data-chat-close]') && location.hash === '#/deck/apps'`)
	b.eval(`(document.querySelector('[data-chat-close]').click(), '')`)
	b.until("a card lists the screens of its module, its settings last", `(() => { const a = [...document.querySelectorAll('[data-app=notes] ~ [data-card-places] a')].map((a) => a.getAttribute('href'));
		return a.length > 1 && a[0].startsWith('#/p/notes/') && a.at(-1) === '#/p/notes/settings' })()`)
	b.eval(`(document.querySelector('[data-app=plain]').click(), '')`)
	b.until("a card opens its module", `location.hash.startsWith('#/p/plain/') && document.querySelector('[data-apps-home]')`)
	b.until("the module's screens are tabs", `document.querySelector('[data-places] a[aria-current=page]') && document.querySelector('[data-places] a[href="#/p/plain/settings"]')`)
	b.eval(`(document.querySelector('[data-places] a[href="#/p/plain/settings"]').click(), '')`)
	b.until("its settings", `location.hash === '#/p/plain/settings' && document.querySelector('[data-places] a[aria-current=page]').getAttribute('href') === '#/p/plain/settings'`)
	b.until("a card without a picture offers to draw one", `[...document.querySelectorAll('button')].some((x) => x.innerText.trim() === 'Draw')`)
	b.eval(`([...document.querySelectorAll('button')].find((x) => x.innerText.trim() === 'Draw').click(), '')`)
	b.until("the dialog to draw the cover with a wish and a model", `document.querySelector('.mantine-Modal-content [data-field="wish"]') && document.querySelector('.mantine-Modal-content [data-field="model"]') && !document.querySelector('.mantine-Modal-content [data-field="project"]')`)
	b.call("browser_run_code_unsafe", map[string]any{"code": `async (page) => { await page.locator('.mantine-Modal-content [data-field="wish"]').fill('a paper boat'); await page.locator('.mantine-Modal-content [data-submit]').click(); }`})
	b.until("the master is told in the chat", `document.querySelector('[data-chat-panel]')?.innerText.includes('a paper boat')`)
	b.eval(`(document.querySelector('[data-chat-close]').click(), '')`)
	dark := e.upload("plain", "dark.png", "image/png", "\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\rIDATx\x9cc\xf8\xcf\xc0\xf0\x1f\x00\x05\x00\x01\xff\x89\x99=\x1d\x00\x00\x00\x00IEND\xaeB`\x82")
	e.must("deck.project_cover", map[string]any{"project": "plain", "file": strconv.FormatInt(dark, 10), "scheme": "dark"}, nil)
	b.eval(`(document.querySelector('[data-apps-home]').click(), '')`)
	b.until("the way home", `document.querySelector('[data-apps]')`)
	b.until("a card with only a dark cover shows it in the light scheme too", `document.querySelector('[data-app=plain]').closest('[data-app-tile]').dataset.cover === 'true'`)

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.until("the cards on a phone", `document.querySelector('[data-phone-bar]') && document.querySelector('[data-apps]') && !document.querySelector('[data-nav-open]')`)
	b.shot("cards-phone")
	b.eval(`(localStorage.removeItem('deck-look'), '')`)
}
