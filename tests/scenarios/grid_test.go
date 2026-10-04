package scenarios

import (
	"encoding/json"

	"strings"
	"testing"
)

// TestTheSpanOfAModuleBlockReachesTheScreen: a block of a module's manifest takes the cells its span says.
func TestTheSpanOfAModuleBlockReachesTheScreen(t *testing.T) {
	e := newEnv(t, boardWebAgent())
	e.createProject("web", strings.Replace(boardWebManifest, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "stat", "title": "Two", "span": 2, "source": {"collection": "notes"}, "value": "len(rows)"},
      {"type": "stat", "title": "Whole", "span": "full", "source": {"collection": "notes"}, "value": "len(rows)"},`, 1))
	spans := map[string]any{}
	for _, m := range e.screenMessages("web", "main", nil) {
		var u struct {
			Components []map[string]any `json:"components"`
		}
		_ = json.Unmarshal(m["updateComponents"], &u)
		for _, c := range u.Components {
			if title, ok := c["title"].(string); ok {
				spans[title] = c["span"]
			}
		}
	}
	if spans["Two"] != float64(2) || spans["Whole"] != "full" {
		t.Fatalf("a block takes the span of its manifest: %v", spans)
	}
}

// TestAParamOfASourceTakesTheTypeOfItsField: a number from the address reaches a read as a number, and an
// empty one is left out.
func TestAParamOfASourceTakesTheTypeOfItsField(t *testing.T) {
	e := newEnv(t, boardWebAgent())
	e.createProject("web", strings.Replace(boardWebManifest, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "params": {"n": "integer"}, "blocks": [
      {"type": "list", "title": "Latest", "source": {"from": "deck.feed_list", "input": {"limit": "=param.n"}}, "item": {"title": "text"}},
      {"type": "stat", "title": "Many", "when": "(param.n ?? 0) > 1", "source": {"from": "deck.probe"}},`, 1))
	for _, params := range []map[string]any{{"params": `{"n": "2"}`}, nil} {
		drawn, _ := json.Marshal(e.screenData("web", "main", params)["b0"])
		if !strings.Contains(string(drawn), `"state":"empty"`) {
			t.Fatalf("the source reads with %v: %s", params, drawn)
		}
	}
	many := func(params map[string]any) bool {
		drawn, _ := json.Marshal(e.screenMessages("web", "main", params))
		return strings.Contains(string(drawn), `"title":"Many"`)
	}
	if !many(map[string]any{"params": `{"n": "2"}`}) || many(map[string]any{"params": `{"n": "1"}`}) || many(nil) {
		t.Fatal("a when reads the parameters of the screen in their type")
	}
}

// TestEachSourceOfAScreenIsReadOnce: blocks that read the same command with the same input share one read.
func TestEachSourceOfAScreenIsReadOnce(t *testing.T) {
	e := newEnv(t, boardWebAgent())
	e.createProject("web", strings.Replace(boardWebManifest, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "stat", "title": "Today", "source": {"from": "deck.probe", "input": {"days": 1}}},
      {"type": "stat", "title": "Today again", "source": {"from": "deck.probe", "input": {"days": 1}}, "value": "len(rows) * 2"},
      {"type": "table", "title": "All", "source": {"from": "deck.probe", "sort": "-name"}, "columns": [{"label": "Name", "bind": "name"}]},
      {"type": "stat", "title": "Some", "source": {"from": "deck.probe", "where": "name == 'a'"}},`, 1))
	e.Probes.Store(0)
	data := e.screenData("web", "main", nil)
	if n := e.Probes.Load(); n != 2 {
		t.Fatalf("two sources, two reads: %d", n)
	}
	if string(data["b2"]["rows"]) == "" || !strings.Contains(string(data["b2"]["rows"]), `["b"]`) || string(data["b3"]["value"]) != "1" || string(data["b1"]["value"]) != "4" {
		t.Fatalf("each block still shapes the read its own way: %v", data)
	}
}
