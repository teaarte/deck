package scenarios

import (
	"strings"
	"testing"
)

// TestSettingsIsInTheMenuAndItsInterfaceTabIsTheLookOfTheDevice: the look is the client's, so the block
// carries no data, and a project screen cannot hold it.
func TestSettingsIsInTheMenuAndItsInterfaceTabIsTheLookOfTheDevice(t *testing.T) {
	e := newEnv(t, nil)
	var menu []struct {
		Project string
		Screens []struct{ Name, Title string }
	}
	e.must("deck.menu", nil, &menu)
	var names []string
	for _, s := range menu[0].Screens {
		names = append(names, s.Name)
	}
	if !strings.Contains(strings.Join(names, " "), "cockpit schedule settings") {
		t.Fatalf("the deck menu is the cockpit, the schedule and settings: %v", names)
	}
	check := newCatalogCheck(t)
	msgs := e.screenMessages("", "settings", nil)
	check.messages(t, msgs)
	var look string
	for id, comp := range check.comps {
		if comp == "appearance" {
			look = id
		}
	}
	if look == "" {
		t.Fatalf("settings has the appearance block: %v", check.comps)
	}
	if got := string(e.screenData("", "settings", nil)[look]["state"]); got != `"ready"` {
		t.Fatalf("the appearance block is ready without a source: %s", got)
	}
	if n := len(e.screenMessages("", "settings", map[string]any{"topic": "processes"})); n != 0 {
		t.Fatalf("no event reads the look again: %d messages", n)
	}

	e.createProject("web", boardWebManifest)
	bad := strings.Replace(boardWebManifest, `{"type": "board", "title": "Work"}`, `{"type": "appearance"}`, 1)
	code, out := e.call("deck.manifest_save", map[string]any{"project": "web", "manifest": bad})
	if code != 422 || !strings.Contains(string(out), "only on deck's own screens") {
		t.Fatalf("a project screen holds no appearance block: %d %s", code, out)
	}
}
