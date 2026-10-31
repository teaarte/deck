package scenarios

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/internal/registry"
)

// The master takes a shot of a screen: deck draws it in its own browser and keeps the picture as a file of the
// master's process, with how long it took to draw; the owner without a process gets the time alone.
func TestTheMasterTakesAShotOfAScreen(t *testing.T) {
	t.Parallel()
	got := make(chan string, 1)
	e := New(t, WithAgent(func(ctx context.Context, c *Call) provider.Result {
		out, bad := c.Tool(ctx, "screen_shot", map[string]any{"address": "#/deck/look", "scheme": "dark", "device": "phone"})
		if bad {
			got <- "refused: " + out
		} else {
			got <- out
		}
		return provider.Result{Text: "Looked."}
	}))

	e.say(0, "how does the settings screen look?")
	var shot struct {
		File, Path string
		MS         int64
	}
	if err := json.Unmarshal([]byte(<-got), &shot); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(shot.File, "results/screen-") || shot.MS != 12 {
		t.Fatalf("the shot is a file of the process with its time: %+v", shot)
	}
	if b, err := os.ReadFile(shot.Path); err != nil || string(b) != "PNG" {
		t.Fatalf("the picture is where the answer says: %q %v", b, err)
	}
	var owner struct {
		Kept *bool
		MS   int64
	}
	e.Must("deck.screen_shot", map[string]any{"address": "#/"}, &owner)
	if owner.Kept == nil || *owner.Kept || owner.MS != 12 {
		t.Fatalf("the owner without a process gets the time alone: %+v", owner)
	}
	e.Refused("deck.screen_shot", map[string]any{"address": "https://example.com"}, 422, "begins with #/")
	e.Refused("deck.screen_shot", map[string]any{"address": "#/", "theme": "nope"}, 404, `no theme "nope"`)
}

// A token deck lends its own browser acts as its caller for a while and only reads: a page opens, nothing changes,
// and once returned it opens nothing.
func TestALentTokenOnlyReadsWhileItLasts(t *testing.T) {
	t.Parallel()
	e := New(t)
	tok := e.App.Tokens.Lend(registry.Actor{Kind: registry.Master, Workspace: 1}, time.Minute)
	if code, out := e.As(tok).Call("deck.screen", map[string]any{"screen": "settings"}); code != 200 {
		t.Fatalf("a lent token reads a screen: %d %s", code, out)
	}
	if code, out := e.As(tok).Call("deck.menu", map[string]any{}); code != 200 {
		t.Fatalf("a lent token reads the menu: %d %s", code, out)
	}
	if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "x", "manifest": plainManifest}); code != 403 || !strings.Contains(string(out), "only reads") {
		t.Fatalf("a lent token changes nothing: %d %s", code, out)
	}
	e.App.Tokens.Return(tok)
	if code, _ := e.As(tok).Call("deck.menu", map[string]any{}); code != 401 {
		t.Fatalf("a returned token opens nothing: %d", code)
	}
}
