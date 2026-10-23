package scenarios

import (
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
	e := newEnv(t, webNotesAgent())
	e.createProject("web", docsManifest)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>chart</p>"}, nil)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "notes/criteria.md", "kind": "doc", "text": "be short"}, nil)
	type body struct {
		HTML, Text, Name string
		Size             int
	}
	master := e.screenAs(registry.Actor{Kind: registry.Master, Workspace: 1}, map[string]any{"project": "web", "screen": "main"})
	if d := blockData[body](t, master["b1"]["value"]); d.Text != "" || d.Size != len("be short") || d.Name != "notes/criteria.md" {
		t.Fatalf("the master reads a document by its name and size: %+v", d)
	}
	if c := blockData[body](t, master["b4"]["value"]); c.HTML != "" || c.Size != len("<p>chart</p>") || c.Name != "custom/chart.html" {
		t.Fatalf("the master reads a custom block by the name and size of its HTML: %+v", c)
	}
	owner := e.screenData("web", "main", nil)
	if d, c := blockData[body](t, owner["b1"]["value"]), blockData[body](t, owner["b4"]["value"]); d.Text != "be short" || c.HTML != "<p>chart</p>" {
		t.Fatalf("the owner reads them whole: %+v %+v", d, c)
	}
}

// TestTheMasterReadsTheCockpit: every read of the cockpit is the master's as the owner's.
func TestTheMasterReadsTheCockpit(t *testing.T) {
	t.Parallel()
	e := newEnv(t, idle)
	data := e.screenAs(registry.Actor{Kind: registry.Master, Workspace: 1}, map[string]any{"screen": "cockpit"})
	raw, _ := json.Marshal(data)
	if strings.Contains(string(raw), "cannot be called") {
		t.Fatalf("the master reads the cockpit: %s", raw)
	}
}
