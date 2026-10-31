package harness

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// SettingsBlock is the data of the block of deck's settings with this title, found by its component.
func (e *Env) SettingsBlock(title string, extra map[string]any) map[string]json.RawMessage {
	e.T.Helper()
	return e.ScreenBlock("", "settings", title, extra)
}

// ScreenBlock is the data of the block of a screen with this title, found by its component in the tab it is in.
func (e *Env) ScreenBlock(project, screen, title string, extra map[string]any) map[string]json.RawMessage {
	e.T.Helper()
	id, shown := e.FindBlock(project, screen, title)
	if id == "" {
		e.T.Fatalf("%s has no block %q", screen, title)
	}
	extra = Merge(extra, shown)
	if extra["topic"] == nil {
		return e.ScreenData(project, screen, extra)[id]
	}
	for _, m := range e.ScreenMessages(project, screen, extra) {
		var dm struct {
			Path  string
			Value map[string]json.RawMessage
		}
		if json.Unmarshal(m["updateDataModel"], &dm) == nil && dm.Path == "/"+id {
			return dm.Value
		}
	}
	e.T.Fatalf("topic %v does not read block %q", extra["topic"], title)
	return nil
}

// FindBlock is the id of the block of a screen with this title and the tabs and dialog that show it: a block shown by a when in
// a tab is drawn only with its tab shown.
func (e *Env) FindBlock(project, screen, title string) (string, map[string]any) {
	e.T.Helper()
	comps := func(extra map[string]any) []map[string]any {
		var out []map[string]any
		for _, m := range e.ScreenMessages(project, screen, extra) {
			var uc struct{ Components []map[string]any }
			if json.Unmarshal(m["updateComponents"], &uc) == nil {
				out = append(out, uc.Components...)
			}
		}
		return out
	}
	all := comps(nil)
	shown := func(drawn []map[string]any, id string) map[string]any {
		out := map[string]any{}
		n := 0
		for _, c := range drawn {
			p, _ := c["id"].(string)
			if name, _ := c["name"].(string); name != "" {
				p = strings.TrimSuffix(p, "-in")
			}
			if c["component"] != "screen" || id != p && !strings.HasPrefix(id, p+"-") {
				continue
			}
			if c["key"] != nil {
				out[[]string{"tab", "sub"}[min(n, 1)]] = c["key"]
				n++
			} else if name, _ := c["name"].(string); name != "" {
				out["dialog"] = name
			}
		}
		return out
	}
	find := func(cs []map[string]any) string {
		for _, c := range cs {
			if c["title"] == title {
				return c["id"].(string)
			}
		}
		for _, c := range cs {
			if c["component"] == "Text" && c["text"] == title {
				return c["id"].(string)
			}
		}
		return ""
	}
	if id := find(all); id != "" {
		return id, shown(all, id)
	}
	for _, c := range all {
		if p, _ := c["id"].(string); c["component"] == "screen" && c["key"] != nil {
			drawn := comps(shown(all, p))
			if id := find(drawn); id != "" {
				return id, shown(drawn, id)
			}
		}
	}
	return "", nil
}

func Verb(t *testing.T, acts []map[string]any, v string) map[string]any {
	t.Helper()
	for _, a := range acts {
		if a["verb"] == v {
			return a
		}
	}
	t.Fatalf("no %s button in %+v", v, acts)
	return nil
}

func HasVerb(acts []map[string]any, v string) bool {
	return slices.ContainsFunc(acts, func(a map[string]any) bool { return a["verb"] == v })
}
