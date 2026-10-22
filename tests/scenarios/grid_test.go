package scenarios

import (
	"encoding/json"
	"slices"

	"strings"
	"testing"
)

// TestTheSpanOfAModuleBlockReachesTheScreen: a block of a module's manifest takes the cells its span says.
func TestTheSpanOfAModuleBlockReachesTheScreen(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	e.createProject("web", strings.Replace(webNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
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
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	e.createProject("web", strings.Replace(webNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "params": {"n": "integer"}, "blocks": [
      {"type": "list", "title": "Latest", "source": {"from": "deck.feed_list", "input": {"limit": "=param.n ?? 5"}}, "item": {"title": "text"}},
      {"type": "stat", "title": "Many", "when": "(param.n ?? 0) > 1", "source": {"from": "deck.probe"}},
      {"type": "list", "title": "Exact", "source": {"from": "deck.feed_list", "input": {"limit": "=param.n"}}, "item": {"title": "text"}},`, 1))
	for _, params := range []map[string]any{{"params": `{"n": "2"}`}, nil} {
		drawn, _ := json.Marshal(e.screenData("web", "main", params)["b0"])
		if !strings.Contains(string(drawn), `"state":"empty"`) {
			t.Fatalf("the source reads with %v: %s", params, drawn)
		}
	}
	if drawn, _ := json.Marshal(e.screenData("web", "main", nil)["b2"]); !strings.Contains(string(drawn), `"state":"error"`) {
		t.Fatalf("a param left out with no default is an error of the block, not a read without it: %s", drawn)
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
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	e.createProject("web", strings.Replace(webNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
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

// TestAnEventReadsNoBlockItDoesNotChange: after an event the when of a block that does not follow its topic is
// not read again.
func TestAnEventReadsNoBlockItDoesNotChange(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	e.createProject("web", strings.Replace(webNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "stat", "title": "Some", "when": "len(rows) > 0", "source": {"from": "deck.probe"}},`, 1))
	e.Probes.Store(0)
	e.screenMessages("web", "main", map[string]any{"topic": "asks"})
	if n := e.Probes.Load(); n != 0 {
		t.Fatalf("an event of asks reads no probe: %d", n)
	}
}

// TestAParamReachesEveryExpressionOfAScreen: the series of a chart and the tone of a stat read the parameters
// of the screen like any other expression.
func TestAParamReachesEveryExpressionOfAScreen(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	e.createProject("web", strings.Replace(webNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "params": {"k": "integer"}, "blocks": [
      {"type": "chart", "title": "Scaled", "source": {"from": "deck.probe"}, "x": "name", "series": [{"label": "N", "y": "id * param.k"}]},
      {"type": "stat", "title": "Toned", "source": {"from": "deck.probe"}, "tone": "param.k > 1 ? 'warn' : 'ok'"},`, 1))
	data := e.screenData("web", "main", map[string]any{"params": `{"k": "3"}`})
	chart, _ := json.Marshal(data["b0"])
	if !strings.Contains(string(chart), `"values":[3,6]`) || string(data["b1"]["tone"]) != `"warn"` {
		t.Fatalf("the chart and the tone read param: %s %s", chart, data["b1"]["tone"])
	}
}

// TestAScreenNamesTheTopicsItFollows: a whole screen names the topics of the reads of its blocks, after which its
// client reads it again, and no other.
func TestAScreenNamesTheTopicsItFollows(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	e.createProject("web", webNotes)
	topics := func(project, screen string) []string {
		for _, m := range e.screenMessages(project, screen, nil) {
			var u struct {
				Components []struct {
					ID     string
					Topics []string
				}
			}
			if json.Unmarshal(m["updateComponents"], &u) == nil && len(u.Components) > 0 && u.Components[0].ID == "root" {
				return u.Components[0].Topics
			}
		}
		return nil
	}
	if got := topics("", "cockpit"); !slices.Contains(got, "asks") || !slices.Contains(got, "feed") || slices.Contains(got, "chat") {
		t.Fatalf("the cockpit follows what it shows: %v", got)
	}
	if got := topics("web", "main"); !slices.Contains(got, "collection") || !slices.Contains(got, "projects") || slices.Contains(got, "asks") {
		t.Fatalf("a project screen follows its collections and its project: %v", got)
	}
	if got := topics("web", "main"); !slices.Contains(got, "documents") {
		t.Fatalf("a custom block follows the document of its HTML: %v", got)
	}
}
