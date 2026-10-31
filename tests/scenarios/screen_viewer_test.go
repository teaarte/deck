package scenarios

import (
	"cmp"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"deck2/internal/registry"
)

// screenAs is the data of a screen read through deck.screen by act, by block id.
func (e *Env) screenAs(act registry.Actor, in map[string]any) map[string]map[string]json.RawMessage {
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
	return dataOf(e.T, msgs)
}

// TestTheMasterReadsAScreenWithoutTheBodiesOfDocuments: a screen read by anyone but the owner gives the HTML of a
// custom block and the text of a document by name and size, not whole; the owner reads them whole.
func TestTheMasterReadsAScreenWithoutTheBodiesOfDocuments(t *testing.T) {
	t.Parallel()
	e := New(t, WithAgent(webNotesAgent()))
	e.Project("web", docsManifest)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	e.Must("deck.document_save", map[string]any{"project": "web", "name": "notes/criteria.md", "kind": "doc", "text": "be short"}, nil)
	type body struct {
		HTML, Text *string
		Name       string
		Size       int
		Whole      map[string]string
	}
	master := registry.Actor{Kind: registry.Master, Workspace: 1}
	brief := e.screenAs(master, map[string]any{"project": "web", "screen": "main"})
	if d := blockData[body](t, brief["b1"]["value"]); d.Text != nil || d.Size != len("be short") || d.Name != "notes/criteria.md" ||
		d.Whole["command"] != "deck.screen" || d.Whole["block"] != "b1" {
		t.Fatalf("the master reads a document by its name and size, and the block that gives it whole: %+v", d)
	}
	if c := blockData[body](t, brief["b4"]["value"]); c.HTML != nil || c.Size != len("<p>chart</p>") || c.Name != "custom/chart.html" || c.Whole["block"] != "b4" {
		t.Fatalf("the master reads a custom block by the name and size of its HTML: %+v", c)
	}
	for id, want := range map[string]string{"b1": "be short", "b4": "<p>chart</p>"} {
		whole := blockData[body](t, e.screenAs(master, map[string]any{"project": "web", "screen": "main", "block": id})[id]["value"])
		if got := cmp.Or(whole.Text, whole.HTML); got == nil || *got != want || whole.Whole != nil {
			t.Fatalf("a read of the block alone gives %s whole: %+v", id, whole)
		}
	}
	owner := e.screenData("web", "main", nil)
	if d, c := blockData[body](t, owner["b1"]["value"]), blockData[body](t, owner["b4"]["value"]); d.Text == nil || *d.Text != "be short" || c.HTML == nil || *c.HTML != "<p>chart</p>" {
		t.Fatalf("the owner reads them whole: %+v %+v", d, c)
	}
}

// TestTheMasterReadsTheCockpit: every read of the cockpit is the master's as the owner's.
func TestTheMasterReadsTheCockpit(t *testing.T) {
	t.Parallel()
	e := New(t, WithAgent(idle))
	data := e.screenAs(registry.Actor{Kind: registry.Master, Workspace: 1}, map[string]any{"screen": "cockpit"})
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), "cannot be called") {
		t.Fatalf("the master reads the cockpit: %s", raw)
	}
}
