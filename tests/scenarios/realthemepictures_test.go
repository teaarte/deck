package scenarios

import (
	"deck2/tests/harness"
	"fmt"
	"image/color"
	"os"
	"testing"
)

// TestRealBrowserThemePictures: juce wears its clouds behind the page; a theme with a scene of Today and a pattern
// shows the scene on the card of Today and repeats the tile behind the page, in both schemes.
func TestRealBrowserThemePictures(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	e := harness.New(t, harness.WithAgent(harness.Idle))
	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1280, "height": 820})
	b.Open(e, "#/")
	b.until("the clouds of juce behind the tiles", `(() => { const p = document.querySelector('[data-backdrop-picture]');
		return !!p && getComputedStyle(p).backgroundImage.startsWith('url("blob:') })()`)
	b.shot("pictures-juce")

	e.Example("paper")
	for _, x := range []struct {
		slot, scheme string
		c            color.RGBA
	}{{"cockpit", "light", color.RGBA{40, 90, 70, 255}}, {"cockpit", "dark", color.RGBA{20, 40, 30, 255}}, {"pattern", "light", color.RGBA{230, 220, 200, 255}}} {
		id := upload(e, "master", x.slot+".png", "image/png", solid(x.c))
		e.Must("deck.theme_image", map[string]any{"theme": "paper", "slot": x.slot, "scheme": x.scheme, "file": fmt.Sprint(id)}, nil)
	}
	e.Must("deck.theme_use", map[string]any{"name": "paper"}, nil)
	for _, scheme := range []string{"light", "dark"} {
		b.open(e.URL + "/?theme=paper&scheme=" + scheme + "#/")
		b.until("the scene of Today and the pattern behind in "+scheme, `(() => { const p = document.querySelector('[data-backdrop-pattern]');
			const scene = document.querySelector('[data-app-tile][data-big] [data-cover-img]');
			return !!p && getComputedStyle(p).backgroundRepeat.startsWith('repeat') && !!scene && scene.complete && !document.querySelector('[data-backdrop-picture]') })()`)
		b.shot("pictures-paper-" + scheme)
	}
}
