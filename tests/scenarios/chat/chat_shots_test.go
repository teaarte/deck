package chat

import (
	"cmp"
	"context"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/tests/harness"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

// screenAs is the data of a screen read through deck.screen by act, by block id.
func screenAs(e *harness.Env, act registry.Actor, in map[string]any) map[string]map[string]json.RawMessage {
	e.T.Helper()
	raw, _ := json.Marshal(in)
	out, err := e.App.Registry.Invoke(context.Background(), act, "deck.screen", raw)
	if err != nil {
		e.T.Fatal(err)
	}
	b, _ := json.Marshal(out)
	var msgs []map[string]json.RawMessage
	if err := json.Unmarshal(b, &msgs); err != nil {
		e.T.Fatal(err)
	}
	return harness.DataOf(e.T, msgs)
}

// TestTheMasterReadsAScreenWithoutTheBodiesOfDocuments: a screen read by anyone but the owner gives the HTML of a
// custom block and the text of a document by name and size, not whole; the owner reads them whole.
func TestTheMasterReadsAScreenWithoutTheBodiesOfDocuments(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	e.Project("web", harness.DocsManifest)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "notes/criteria.md", "kind": "doc", "text": "be short"}, nil)
	type body struct {
		HTML, Text *string
		Name       string
		Size       int
		Whole      map[string]string
	}
	master := registry.Actor{Kind: registry.Master, Workspace: 1}
	brief := screenAs(e, master, map[string]any{"project": "web", "screen": "main"})
	if d := harness.BlockData[body](t, brief["b1"]["value"]); d.Text != nil || d.Size != len("be short") || d.Name != "notes/criteria.md" ||
		d.Whole["command"] != "deck.screen" || d.Whole["block"] != "b1" {
		t.Fatalf("the master reads a document by its name and size, and the block that gives it whole: %+v", d)
	}
	if c := harness.BlockData[body](t, brief["b4"]["value"]); c.HTML != nil || c.Size != len("<p>chart</p>") || c.Name != "custom/chart.html" || c.Whole["block"] != "b4" {
		t.Fatalf("the master reads a custom block by the name and size of its HTML: %+v", c)
	}
	for id, want := range map[string]string{"b1": "be short", "b4": "<p>chart</p>"} {
		whole := harness.BlockData[body](t, screenAs(e, master, map[string]any{"project": "web", "screen": "main", "block": id})[id]["value"])
		if got := cmp.Or(whole.Text, whole.HTML); got == nil || *got != want || whole.Whole != nil {
			t.Fatalf("a read of the block alone gives %s whole: %+v", id, whole)
		}
	}
	owner := e.ScreenData("web", "main", nil)
	if d, c := harness.BlockData[body](t, owner["b1"]["value"]), harness.BlockData[body](t, owner["b4"]["value"]); d.Text == nil || *d.Text != "be short" || c.HTML == nil || *c.HTML != "<p>chart</p>" {
		t.Fatalf("the owner reads them whole: %+v %+v", d, c)
	}
}

// TestTheMasterReadsTheCockpit: every read of the cockpit is the master's as the owner's.
func TestTheMasterReadsTheCockpit(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	data := screenAs(e, registry.Actor{Kind: registry.Master, Workspace: 1}, map[string]any{"screen": "cockpit"})
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), "cannot be called") {
		t.Fatalf("the master reads the cockpit: %s", raw)
	}
}

// The master takes a shot of a screen: deck draws it in its own browser and keeps the picture as a file of the
// master's process, with how long it took to draw; the owner without a process gets the time alone.
func TestTheMasterTakesAShotOfAScreen(t *testing.T) {
	t.Parallel()
	got := make(chan string, 1)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		out, bad := c.Tool(ctx, "screen_shot", map[string]any{"address": "#/deck/look", "scheme": "dark", "device": "phone"})
		if bad {
			got <- "refused: " + out
		} else {
			got <- out
		}
		return provider.Result{Text: "Looked."}
	}))

	e.Say(0, "how does the settings screen look?")
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
	e := harness.New(t)
	tok := e.App.Tokens.Lend(registry.Actor{Kind: registry.Master, Workspace: 1}, time.Minute)
	if code, out := e.As(tok).Call("deck.screen", map[string]any{"screen": "settings"}); code != 200 {
		t.Fatalf("a lent token reads a screen: %d %s", code, out)
	}
	if code, out := e.As(tok).Call("deck.menu", map[string]any{}); code != 200 {
		t.Fatalf("a lent token reads the menu: %d %s", code, out)
	}
	if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "x", "manifest": harness.PlainManifest}); code != 403 || !strings.Contains(string(out), "only reads") {
		t.Fatalf("a lent token changes nothing: %d %s", code, out)
	}
	e.App.Tokens.Return(tok)
	if code, _ := e.As(tok).Call("deck.menu", map[string]any{}); code != 401 {
		t.Fatalf("a returned token opens nothing: %d", code)
	}
}
