package owner

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// TestTheMemoryTabMapsMemoryWithAPictureAndButtons: Settings → Memory draws what feeds what from the numbers of the
// memory map, and its cards carry the buttons that open a fact, delete a lesson, change the model of sleep and run it.
func TestTheMemoryTabMapsMemoryWithAPictureAndButtons(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle))
	e.Project("shop", harness.TestManifest([]string{harness.AgentCommand("collect", "")}, nil))
	e.Must("deck.document_save", map[string]any{"name": "memory/likes-tea.md", "kind": "memory",
		"text": harness.Fact("likes-tea", "user", "The owner drinks tea")}, nil)
	e.Must("deck.document_save", map[string]any{"name": "memory/MEMORY.md", "kind": "memory", "text": "- [Tea](likes-tea.md) — mornings\n"}, nil)
	e.Must("deck.document_save", map[string]any{"project": "shop", "name": "memory/site-waits.md", "kind": "memory",
		"text": "---\nname: site-waits\ndescription: The site answers after a minute\ntype: lesson\nsource: process:1\ncommand: collect\n---\n\nWait a minute.\n"}, nil)

	type line struct {
		Title, Text, Meta string
		Actions           []struct{ Label, Command string }
	}
	lines := func(title string) []line {
		var ls []line
		if err := json.Unmarshal(e.SettingsBlock(title, map[string]any{"tab": "Memory"})["items"], &ls); err != nil {
			t.Fatalf("%s: %v", title, err)
		}
		return ls
	}
	labels := func(l line) string {
		var out []string
		for _, a := range l.Actions {
			out = append(out, a.Label)
		}
		return strings.Join(out, ",")
	}

	var files []struct{ Title, Type, URL, Error string }
	_ = json.Unmarshal(e.SettingsBlock("What feeds what", map[string]any{"tab": "Memory"})["files"], &files)
	if len(files) != 1 || files[0].Error != "" || files[0].URL != "/api/files/data/master/memory-map.svg" {
		t.Fatalf("the picture is a file of the master: %+v", files)
	}
	code, svg := e.Do("GET", files[0].URL, nil)
	if code != 200 || !strings.HasPrefix(string(svg), "<svg") || !strings.Contains(string(svg), `data-node="sleep"`) ||
		!strings.Contains(string(svg), "1 facts, 1 of 150 lines") || !strings.Contains(string(svg), "1 lessons") {
		t.Fatalf("the picture is drawn from the numbers of memory: %d %s", code, svg)
	}

	parts := map[string]line{}
	for _, l := range lines("Parts of memory") {
		parts[l.Title] = l
	}
	if p := parts["The owner's memory"]; p.Text != "1 facts, 0 lessons" || p.Meta != "1 of 150 index lines" {
		t.Fatalf("the owner's memory is counted: %+v", parts)
	}
	if p := parts["Sleep"]; !strings.Contains(p.Meta, "30 3 * * *") || labels(p) != "Run now" {
		t.Fatalf("sleep says its schedule and runs now: %+v", p)
	}
	if p := parts["Own memory of the agents"]; !strings.HasPrefix(p.Text, "Off") {
		t.Fatalf("the agents keep no memory of their own: %+v", p)
	}

	ls := lines("Lessons")
	if len(ls) != 1 || ls[0].Title != "site-waits" || labels(ls[0]) != "Open,Delete" || ls[0].Meta != "shop · collect" {
		t.Fatalf("a lesson opens and deletes: %+v", ls)
	}
	mods := map[string]line{}
	for _, l := range lines("Memory of each module") {
		mods[l.Title] = l
	}
	if labels(mods["master"]) != "Open" || labels(mods["shop"]) != "" {
		t.Fatalf("a memory with an index opens it: %+v", mods)
	}
	who := lines("Who sleeps and reviews")
	if len(who) != 2 || !strings.Contains(fmt.Sprint(who), "Change the model") {
		t.Fatalf("sleep and review change their model: %+v", who)
	}

	var r struct{ Process int64 }
	e.Must("master.sleep", map[string]any{}, &r)
	e.WaitState(r.Process, "done")
	for _, l := range lines("Parts of memory") {
		if l.Title == "Sleep" && !strings.Contains(l.Meta, "1 runs lately") {
			t.Fatalf("sleep counts its run: %+v", l)
		}
	}
}
