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
		for _, in := range []map[string]any{{"address": "#/deck/settings?tab=Look", "scheme": "dark"},
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
