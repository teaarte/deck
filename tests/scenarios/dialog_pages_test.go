package scenarios

import (
	"encoding/json"
	"strings"
	"testing"
)

// pagedNotes is webNotes whose main screen has a dialog with a table of the notes two at a time.
var pagedNotes = strings.Replace(webNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "dialog", "name": "all", "title": "All notes", "blocks": [
        {"type": "table", "title": "All", "source": {"collection": "notes", "sort": "title", "limit": 2}, "columns": [{"label": "Title", "bind": "title"}]},
        {"type": "table", "title": "Broken", "source": {"collection": "notes"}, "columns": [{"label": "Score", "bind": "score > 0 ? score : nil"}]}]},`, 1)

// TestADialogReadsItsBlocksWhenItOpensAPageAtATime: a closed dialog of any screen sends no data of its blocks, an
// open one does, read alone too; a table shows its limit a page at a time; a save still checks the dialogs.
func TestADialogReadsItsBlocksWhenItOpensAPageAtATime(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("web", pagedNotes)
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes",
		"records": jsonl(map[string]any{"title": "a", "score": 1}, map[string]any{"title": "b", "score": 1}, map[string]any{"title": "c", "score": 1},
			map[string]any{"title": "d", "score": 1}, map[string]any{"title": "e", "score": 1})}, nil)

	closed := e.screenData("web", "main", nil)
	if _, ok := closed["b0-0"]; ok || closed["b1"] == nil {
		t.Fatalf("a closed dialog sends no data of its blocks, the rest of the screen does: %v", keys(closed))
	}
	open := e.screenData("web", "main", map[string]any{"dialog": "all"})
	var rows []screenRow
	_ = json.Unmarshal(open["b0-0"]["rows"], &rows)
	if len(rows) != 2 || string(open["b0-0"]["more"]) != "3" {
		t.Fatalf("an open dialog reads its table, its limit a page: %d rows, %s more", len(rows), open["b0-0"]["more"])
	}

	msgs := e.screenMessages("web", "main", map[string]any{"dialog": "all", "block": "b0", "pages": `{"b0-0": 2}`})
	alone := dataOf(t, msgs)
	_ = json.Unmarshal(alone["b0-0"]["rows"], &rows)
	if len(msgs) != 2 || len(alone) != 2 || len(rows) != 4 || string(alone["b0-0"]["more"]) != "1" {
		t.Fatalf("a read of the dialog alone gives only its data, its table two pages: %v %d rows", keys(alone), len(rows))
	}
	for _, m := range msgs {
		if m["updateDataModel"] == nil {
			t.Fatalf("a read of one block gives only data: %v", m)
		}
	}

	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": jsonl(map[string]any{"title": "f"})}, nil)
	var saved struct{ Screens []string }
	e.must("deck.manifest_save", map[string]any{"project": "web", "manifest": pagedNotes}, &saved)
	if len(saved.Screens) == 0 || !strings.Contains(strings.Join(saved.Screens, "\n"), `table block "Broken"`) {
		t.Fatalf("the check of a saved manifest reads the blocks of its dialogs too: %q", saved.Screens)
	}
}
