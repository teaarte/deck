package scenarios

import (
	"context"
	"os"
	"slices"
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

// TestRealBrowserCockpitHistory: History stands in the top of the cockpit and keeps its place when a list comes in:
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
		b.shot("cockpit-history-" + size.name)
		id := e.start("web", "score", map[string]any{"title": "x"})
		e.waitState(id, "running")
		b.until("the running list on the cockpit", `[...document.querySelectorAll('[data-block]')].some((x) => x.innerText.includes('Running now'))`)
		if after := b.eval(at); after != before {
			t.Fatalf("History keeps its place on a %s when a list comes in: %s, then %s", size.name, before, after)
		}
		b.shot("cockpit-history-" + size.name + "-running")
		e.must("deck.process_stop", map[string]any{"process": id}, nil)
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
	b.open(e.URL + "/#/deck/cockpit")
	b.until("the order after a reload", order("[data-nav]", "beta alpha gamma"))
	if names, _ := e.side(); !slices.Equal(names, []string{"beta", "alpha", "gamma"}) {
		t.Fatalf("deck keeps the order of the browser: %v", names)
	}

	b.call("browser_resize", map[string]any{"width": 390, "height": 844})
	b.open(e.URL + "/#/deck/cockpit")
	b.until("the phone bar", `document.querySelector('[data-phone-bar]')`)
	b.eval(`(document.querySelector('[data-nav-open]').click(), '')`)
	b.until("the order on a phone", `document.querySelector('[data-nav-phone] [data-nav-order]')?.offsetParent && `+order("[data-nav-phone]", "beta alpha gamma"))
	b.eval(`(document.querySelector('[data-nav-phone] [data-nav-order]').click(), '')`)
	b.until("the arrows on a phone", `document.querySelector('[data-nav-phone] [data-order="gamma"] [data-move="up"]')`)
	b.eval(`(document.querySelector('[data-nav-phone] [data-order="gamma"] [data-move="up"]').click(), '')`)
	b.until("gamma moved up on a phone", order("[data-nav-phone]", "beta gamma alpha"))
	b.shot("menu-order-phone")
	b.open(e.URL + "/#/deck/cockpit")
	b.eval(`(document.querySelector('[data-nav-open]').click(), '')`)
	b.until("the order of the phone after a reload", order("[data-nav-phone]", "beta gamma alpha"))
}
