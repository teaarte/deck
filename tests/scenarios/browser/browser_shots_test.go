package browser

import (
	"bytes"
	"context"
	"deck2/internal/app"
	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

// TestRealBrowserTheMasterSeesAScreen: deck draws a screen in its own browser under the master's rights, as a
// computer and as a phone, and the master gets the picture and how long it took.
func TestRealBrowserTheMasterSeesAScreen(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	checkBuiltClient(t)
	shots := make(chan string, 2)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		for _, in := range []map[string]any{{"address": "#/deck/look", "scheme": "dark"},
			{"address": "#/", "device": "phone", "theme": "juce", "scheme": "light"}} {
			out, bad := c.Tool(ctx, "screen_shot", in)
			if bad {
				out = "refused: " + out
			}
			shots <- out
		}
		return provider.Result{Text: "Looked."}
	}), harness.WithConfig(

		func(c *app.Config) { c.Browser = app.DefaultBrowser }))

	e.Say(0, "look at the settings")
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
	harness.Needs(t, "DECK_REAL_BROWSER")
	checkBuiltClient(t)
	shots := make(chan string, 1)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		out, bad := c.Tool(ctx, "screen_shot", map[string]any{"address": "#/p/web/main", "scheme": "light"})
		if bad {
			out = "refused: " + out
		}
		shots <- out
		return provider.Result{Text: "Looked."}
	}), harness.WithConfig(

		func(c *app.Config) { c.Browser = app.DefaultBrowser }))

	e.Project("web", harness.WebNotes)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": customMagenta}, nil)
	e.Say(0, "look at the chart")
	var shot struct{ Path, Error string }
	if raw := <-shots; json.Unmarshal([]byte(raw), &shot) != nil || shot.Error != "" {
		t.Fatalf("the shot: %s", raw)
	}
	b, err := os.ReadFile(shot.Path)
	if err != nil {
		t.Fatal(err)
	}
	magenta := magentaIn(t, b)
	if dir := os.Getenv("DECK_SCREENSHOTS"); dir != "" {
		_ = os.WriteFile(dir+"/shot-custom.png", b, 0o644)
	}
	if magenta < 2000 {
		t.Fatalf("the shot shows the custom block drawn: %d magenta points", magenta)
	}
}

// magentaIn counts the magenta points of a picture, one in four.
func magentaIn(t *testing.T, b []byte) int {
	t.Helper()
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
	return magenta
}

// customReads is a custom block that, given its items, takes a moment, then reads a picture of its module's data
// folder by resources/read and shows it, as a block of photos does.
const customReads = `<!doctype html><html><body style="margin:0"><img id="pic" style="display:block;width:100%;height:200px"><script type="module">
import { App } from "/ext-apps.js";
const app = new App({ name: "photo", version: "1" });
app.ontoolresult = async () => {
  await new Promise((r) => setTimeout(r, 200));
  const c = (await app.readServerResource({ uri: "deck://data/pics/photo.png" })).contents[0];
  document.getElementById("pic").src = URL.createObjectURL(new Blob([Uint8Array.from(atob(c.blob), (x) => x.charCodeAt(0))], { type: c.mimeType }));
};
await app.connect();
</script></body></html>`

// TestRealBrowserAShotWaitsForThePicturesACustomBlockReads: a shot of a screen waits for the pictures a custom block
// reads itself, on a computer and on a phone.
func TestRealBrowserAShotWaitsForThePicturesACustomBlockReads(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	checkBuiltClient(t)
	shots := make(chan string, 2)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		for _, device := range []string{"computer", "phone"} {
			out, bad := c.Tool(ctx, "screen_shot", map[string]any{"address": "#/p/web/main", "scheme": "light", "device": device})
			if bad {
				out = "refused: " + out
			}
			shots <- out
		}
		return provider.Result{Text: "Looked."}
	}), harness.WithConfig(

		func(c *app.Config) { c.Browser = app.DefaultBrowser }))

	p := e.Project("web", harness.WebNotes)
	pic := image.NewRGBA(image.Rect(0, 0, 400, 200))
	for i := range pic.Pix {
		pic.Pix[i] = []uint8{255, 0, 255, 255}[i%4]
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, pic)
	data := deckhome.Layout(e.App.Cfg.Home).Data(p)
	_ = os.MkdirAll(filepath.Join(data, "pics"), 0o700)
	_ = os.WriteFile(filepath.Join(data, "pics", "photo.png"), buf.Bytes(), 0o600)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": customReads}, nil)
	e.Say(0, "look at the photo")
	for _, device := range []string{"computer", "phone"} {
		var shot struct{ Path, Error string }
		if raw := <-shots; json.Unmarshal([]byte(raw), &shot) != nil || shot.Error != "" {
			t.Fatalf("the shot: %s", raw)
		}
		b, err := os.ReadFile(shot.Path)
		if err != nil {
			t.Fatal(err)
		}
		if dir := os.Getenv("DECK_SCREENSHOTS"); dir != "" {
			_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("shot-custom-picture-%s.png", device)), b, 0o644)
		}
		if magenta := magentaIn(t, b); magenta < 2000 {
			t.Fatalf("the shot on a %s shows the picture the block read: %d magenta points", device, magenta)
		}
	}
}
