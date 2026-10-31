package scenarios

import (
	"os"
	"strings"
	"testing"
)

// TestRealBrowserThemeStylesTheCards: the cards wear a theme of white cards, a meadow and its own grid on a desktop and a
// phone: DECK_REAL_BROWSER=1 go test ./tests/scenarios -run TestRealBrowserThemeStylesTheCards -v.
func TestRealBrowserThemeStylesTheCards(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := New(t, WithAgent(ok))
	e.Project("web", webNotes)
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1400, "height": 900})
	b.Open(e, "#/deck/apps")
	b.until("the cockpit's card in the accent", `(() => { const c = document.querySelector('[data-app-tile][data-big]'); return c && getComputedStyle(c).backgroundImage.includes('gradient') })()`)
	b.shot("tiles-juce")

	cards := `{"version": 1, "title": {"en": "Cards"}, "mood": "m", "schemes": ["light"],
  "tokens": {"cockpit": "linear-gradient(180deg, #ffffff, #f2f6ee)", "tile": "#ffffff", "tile-text": "#0e1116", "tile-shade": "none",
    "tile-icon": "#faece7", "tile-min": "240px", "tile-height": "200px", "tile-gap": "24px",
    "meadow": "radial-gradient(70% 100% at 22% 100%, #b7d79a 0%, transparent 75%)"},
  "parts": {"tile": {"radius": "24px", "shadow": "var(--ring), var(--shadow)", "background": "#ffffff"}}}`
	e.Must("deck.theme_save", map[string]any{"name": "cards", "text": cards}, nil)
	e.Must("deck.theme_use", map[string]any{"name": "cards"}, nil)
	for _, size := range []struct {
		name          string
		width, height int
	}{{"desktop", 1400, 900}, {"phone", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.width, "height": size.height})
		b.open(e.URL + "/#/deck/apps")
		b.until("the cards theme worn", `document.documentElement.dataset.deckTheme === 'cards' && document.querySelector('[data-app-tile]:not([data-big])')`)
		got := b.eval(`(() => { const c = document.querySelector('[data-app-tile]:not([data-big])'), big = document.querySelector('[data-app-tile][data-big]'), s = getComputedStyle(c);
			const cells = [...document.querySelectorAll('[data-cell]')].map((x) => x.getBoundingClientRect()), meadow = document.querySelector('[data-meadow]');
			const gaps = cells.flatMap((a) => cells.flatMap((b) => Math.abs(b.left - a.left) < 1 && b.top > a.bottom ? [b.top - a.bottom]
				: Math.abs(b.top - a.top) < 1 && b.left > a.right ? [b.left - a.right] : [])).map(Math.round);
			return [s.backgroundColor, s.color, s.borderTopLeftRadius, getComputedStyle(big).backgroundImage.includes('rgb(242, 246, 238)'), Math.min(...gaps) + 'px',
				getComputedStyle(c.querySelector('[data-app-tile-icon]')).backgroundColor, !!meadow && getComputedStyle(meadow).backgroundImage.includes('radial-gradient')].join(' ') })()`)
		gap := map[string]string{"desktop": "24px", "phone": "12px"}[size.name]
		if want := "rgb(255, 255, 255) rgb(14, 17, 22) 24px true " + gap + " rgb(250, 236, 231) true"; got != want {
			t.Fatalf("the cards wear their theme on a %s: %s, want %s", size.name, got, want)
		}
		if size.name == "desktop" {
			if h := b.eval(`String(Math.round(document.querySelector('[data-app-tile]:not([data-big])').getBoundingClientRect().height))`); h != "200" {
				t.Fatalf("a card is as high as its theme says: %s", h)
			}
		}
		b.shot("tiles-cards-" + size.name)
	}
	if !strings.Contains(b.eval(`document.getElementById('deck-theme')?.textContent ?? ''`), "--deck-tile-min: 240px") {
		t.Fatal("the grid's least width comes from the theme")
	}
}
