package scenarios

import (
	"os"
	"testing"
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
