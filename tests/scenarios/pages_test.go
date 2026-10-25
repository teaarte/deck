package scenarios

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
	"testing"
)

// TestATabNotShownIsReadWhenShown: a screen reads the blocks of the tab shown only, the one its address names or the
// first; a read of the block of another tab gives its blocks, as the client does when the owner shows it.
func TestATabNotShownIsReadWhenShown(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("notes", webManifest("http://127.0.0.1:1"))
	e.must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes", "records": jsonl(map[string]any{"title": "alpha", "score": 7})}, nil)
	read := func(extra map[string]any) []string {
		return slices.Sorted(maps.Keys(dataOf(t, e.screenMessages("notes", "main", extra))))
	}
	if got := read(nil); !slices.Contains(got, "b1-0-0") || slices.Contains(got, "b1-1-0") {
		t.Fatalf("the first tab is read, the other waits: %v", got)
	}
	if got := read(map[string]any{"tab": "Top"}); slices.Contains(got, "b1-0-0") || !slices.Contains(got, "b1-1-0") {
		t.Fatalf("the tab the address names is read instead: %v", got)
	}
	if got := read(map[string]any{"block": "b1-1"}); !slices.Equal(got, []string{"b1-1-0"}) {
		t.Fatalf("a read of the block of a tab gives its blocks: %v", got)
	}
	var tabs int
	all := dataOf(t, e.screenMessages("", "spend", nil))
	whole := e.screenData("", "spend", nil)
	for id := range whole {
		if strings.HasPrefix(id, "b4-") {
			tabs++
		}
	}
	if len(whole)-len(all) < 3 || tabs == 0 {
		t.Fatalf("Spend reads its stats and the tab shown, not the other tabs: %d of %d", len(all), len(whole))
	}
}

// TestABlockWithoutALimitShowsAPage: a table or list without a limit shows a page and more by pages, details that
// repeat the cell are left out, a save names such a block on a collection past a page.
func TestABlockWithoutALimitShowsAPage(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	m := strings.Replace(webNotes, `"columns": [{"label": "Title", "bind": "title"}]`,
		`"columns": [{"label": "Title", "bind": "title", "details": "title"}, {"label": "Note", "bind": "source", "details": "note"}]`, 1)
	e.createProject("web", m)
	var recs []map[string]any
	for i := range 60 {
		recs = append(recs, map[string]any{"title": fmt.Sprintf("note %02d", i), "source": "web", "note": "longer"})
	}
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": jsonl(recs...)}, nil)
	data := dataOf(t, e.screenMessages("web", "main", nil))
	rows := blockData[[]screenRow](t, data["b3"]["rows"])
	if len(rows) != 50 || string(data["b3"]["more"]) != "10" || len(blockData[[]map[string]any](t, data["b0"]["items"])) != 50 {
		t.Fatalf("a table and a list without a limit show a page and say how many more: %d rows, more %s", len(rows), data["b3"]["more"])
	}
	if d := rows[0].Details; len(d) != 2 || d[0] != nil || d[1] != "longer" {
		t.Fatalf("details that say what the cell says are left out: %v", d)
	}
	more := dataOf(t, e.screenMessages("web", "main", map[string]any{"pages": `{"b3": 2}`}))
	if n := len(blockData[[]screenRow](t, more["b3"]["rows"])); n != 60 {
		t.Fatalf("the next page shows the rest: %d", n)
	}
	var saved struct{ Warnings []string }
	e.must("deck.manifest_save", map[string]any{"project": "web", "manifest": m}, &saved)
	if len(saved.Warnings) != 2 || !strings.Contains(saved.Warnings[0], `screen main block "Notes" has no limit and notes holds 60 records`) {
		t.Fatalf("a save names the blocks without a limit on a collection past a page: %v", saved.Warnings)
	}
	raw, _ := json.Marshal(saved)
	if strings.Contains(string(raw), "Chart") {
		t.Fatalf("a custom block keeps every row: %s", raw)
	}
}
