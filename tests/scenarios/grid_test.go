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
