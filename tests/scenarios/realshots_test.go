package scenarios

import (
	"bytes"
	"context"
	"encoding/json"
	"image/png"
	"os"
	"testing"

	"deck2/internal/app"
	"deck2/internal/provider"
)

// TestRealBrowserTheMasterSeesAScreen: deck draws a screen in its own browser under the master's rights, as a
// computer and as a phone, and the master gets the picture and how long it took.
func TestRealBrowserTheMasterSeesAScreen(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	shots := make(chan string, 2)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		for _, in := range []map[string]any{{"address": "#/deck/look", "scheme": "dark"},
			{"address": "#/", "device": "phone", "theme": "juce", "scheme": "light"}} {
			out, bad := c.Tool(ctx, "screen_shot", in)
			if bad {
				out = "refused: " + out
			}
			shots <- out
		}
		return provider.Result{Text: "Looked."}
	}, func(c *app.Config) { c.Browser = app.DefaultBrowser })
	e.say(0, "look at the settings")
	for i, want := range []struct{ wide bool }{{true}, {false}} {
		var shot struct {
			Path, Error string
			MS          int64
		}
		raw := <-shots
		if err := json.Unmarshal([]byte(raw), &shot); err != nil {
			t.Fatalf("shot %d: %s", i, raw)
		}
		b, err := os.ReadFile(shot.Path)
		if err != nil {
			t.Fatal(err)
		}
		cfg, err := png.DecodeConfig(bytes.NewReader(b))
		if err != nil || shot.MS <= 0 || shot.Error != "" || (cfg.Width >= 1280) != want.wide {
			t.Fatalf("shot %d is a picture of the screen drawn in %d ms: %dx%d %v %q", i, shot.MS, cfg.Width, cfg.Height, err, shot.Error)
		}
		if dir := os.Getenv("DECK_SCREENSHOTS"); dir != "" {
			_ = os.WriteFile(dir+"/shot-"+[]string{"computer", "phone"}[i]+".png", b, 0o644)
		}
	}
}

// customMagenta is a custom block that paints itself one colour once deck gives it its items.
const customMagenta = `<!doctype html><html><body style="margin:0"><div id="out" style="height:200px"></div><script type="module">
import { App } from "/ext-apps.js";
const app = new App({ name: "magenta", version: "1" });
app.ontoolresult = () => { document.getElementById("out").style.background = "#ff00ff"; };
await app.connect();
</script></body></html>`

// TestRealBrowserAShotDrawsACustomBlock: a shot the master takes draws a custom block of a module as the owner sees
// it, its HTML whole and run.
func TestRealBrowserAShotDrawsACustomBlock(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	shots := make(chan string, 1)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		out, bad := c.Tool(ctx, "screen_shot", map[string]any{"address": "#/p/web/main", "scheme": "light"})
		if bad {
			out = "refused: " + out
		}
		shots <- out
		return provider.Result{Text: "Looked."}
	}, func(c *app.Config) { c.Browser = app.DefaultBrowser })
	e.createProject("web", webNotes)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": customMagenta}, nil)
	e.say(0, "look at the chart")
	var shot struct{ Path, Error string }
	if raw := <-shots; json.Unmarshal([]byte(raw), &shot) != nil || shot.Error != "" {
		t.Fatalf("the shot: %s", raw)
	}
	b, err := os.ReadFile(shot.Path)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatal(err)
	}
	magenta := 0
	for y := img.Bounds().Min.Y; y < img.Bounds().Max.Y; y += 2 {
		for x := img.Bounds().Min.X; x < img.Bounds().Max.X; x += 2 {
			if r, g, bl, _ := img.At(x, y).RGBA(); r>>8 > 240 && g>>8 < 20 && bl>>8 > 240 {
				magenta++
			}
		}
	}
	if dir := os.Getenv("DECK_SCREENSHOTS"); dir != "" {
		_ = os.WriteFile(dir+"/shot-custom.png", b, 0o644)
	}
	if magenta < 2000 {
		t.Fatalf("the shot shows the custom block drawn: %d magenta points", magenta)
	}
}
