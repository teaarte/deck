package browser

import (
	"testing"

	"deck2/tests/harness"
)

// TestRealBrowserTheHomeIsArrangedLikeAPhone: on a desk and a phone the pencil arranges the cards, a card's menu sizes
// it and puts it in a new folder that gives it back, Today opens the settings, nothing goes past the edge.
func TestRealBrowserTheHomeIsArrangedLikeAPhone(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	for _, name := range []string{"alpha", "beta", "gamma"} {
		e.Project(name, harness.PlainManifest)
	}
	b := Open(t)
	for _, size := range []struct {
		name string
		w, h int
	}{{"desktop", 1400, 900}, {"phone", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.w, "height": size.h})
		b.Open(e, "#/")
		b.until(size.name+": the pencil at the bottom right", `(() => { const p = document.querySelector('[data-placing-toggle]')?.getBoundingClientRect();
			return p && p.right > innerWidth - 80 && p.bottom > innerHeight - 160 })()`)
		b.eval(`(document.querySelector('[data-placing-toggle]').click(), '')`)
		b.until(size.name+": the cards wear their menus", `document.querySelector('[data-apps][data-placing]') && document.querySelector('[data-card-menu="alpha"]')`)
		b.eval(`(document.querySelector('[data-card-menu="alpha"]').click(), '')`)
		b.until(size.name+": the menu of a card", `document.querySelector('[data-card-menu-list="alpha"] [data-size="2x1"]')`)
		b.eval(`(document.querySelector('[data-card-menu-list="alpha"] [data-size="2x1"]').click(), '')`)
		b.until(size.name+": the card two cells wide", `(() => { const a = document.querySelector('[data-cell="alpha"]')?.getBoundingClientRect(), g = document.querySelector('[data-cell="gamma"]')?.getBoundingClientRect();
			return a && g && a.width > g.width * 1.6 })()`)
		b.eval(`(document.querySelector('[data-card-menu="beta"]').click(), '')`)
		b.until(size.name+": the field of a new folder", `document.querySelector('[data-card-menu-list="beta"] [data-new-folder]')`)
		b.type_(`document.querySelector('[data-card-menu-list="beta"] [data-new-folder]')`, "Seldom")
		b.eval(`(document.querySelector('[data-card-menu-list="beta"] [data-new-folder]').dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', bubbles: true })), '')`)
		b.until(size.name+": the folder lists the module, out of the grid", `document.querySelector('[data-folder="Seldom"] [data-folder-module="beta"]') && !document.querySelector('[data-cell="beta"]') && document.querySelector('[data-apps][data-placing]')`)
		b.shot("home-placing-" + size.name)
		b.eval(`(document.querySelector('[data-out="beta"]').click(), '')`)
		b.until(size.name+": the module back on the grid, the empty folder gone", `document.querySelector('[data-cell="beta"]') && !document.querySelector('[data-folder]')`)
		b.eval(`(document.querySelector('[data-placing-toggle]').click(), '')`)
		b.until(size.name+": the placing done", `!document.querySelector('[data-apps][data-placing]')`)
		b.until(size.name+": Today opens the settings", `document.querySelector('[data-today-settings]')?.getAttribute('href')?.includes('settings')`)
		b.until(size.name+": nothing goes past the edge", `document.documentElement.scrollWidth <= window.innerWidth + 1`)
		b.shot("home-" + size.name)
	}
}
