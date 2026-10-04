package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"deck2/internal/events"
)

// TestASlowDeviceHearsEveryChange: a device that reads its live channel slower than deck publishes still hears
// each topic of each project once; nothing is dropped.
func TestASlowDeviceHearsEveryChange(t *testing.T) {
	e := newEnv(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phone := e.live(ctx)
	for i := range 500 {
		e.App.Bus.Emit(events.Event{Topic: "collection", Project: fmt.Sprintf("p%d", i)})
	}
	heard := map[string]bool{}
	timeout := time.After(10 * time.Second)
	for len(heard) < 500 {
		select {
		case ev := <-phone:
			heard[ev.Project] = true
		case <-timeout:
			t.Fatalf("heard %d of 500 changes", len(heard))
		}
	}
}

// TestAChangeOfAnotherProjectDoesNotRereadAScreen: a screen reads its blocks again only for a change in a project
// they read, its own or one its manifest reads.
func TestAChangeOfAnotherProjectDoesNotRereadAScreen(t *testing.T) {
	e := newEnv(t, boardWebAgent())
	e.createProject("a", boardWebManifest)
	e.createProject("b", boardWebManifest)
	e.createProject("c", strings.Replace(strings.Replace(boardWebManifest, `"menu": ["main"]}`, `"menu": ["main"], "reads": [{"project": "a", "collections": ["notes"]}]}`, 1),
		`{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "table", "title": "Of a", "source": {"project": "a", "collection": "notes"}, "columns": [{"label": "Title", "bind": "title"}]},`, 1))
	data := func(project, changed string) string {
		var out []string
		for _, m := range e.screenMessages(project, "main", map[string]any{"topic": "collection", "changed": changed}) {
			if m["updateDataModel"] != nil {
				var dm struct{ Path string }
				_ = json.Unmarshal(m["updateDataModel"], &dm)
				out = append(out, dm.Path)
			}
		}
		return strings.Join(out, ",")
	}
	if got := data("b", "a"); got != "" {
		t.Fatalf("a change in a reads nothing of b: %s", got)
	}
	if got := data("b", "b"); got == "" {
		t.Fatal("a change in b reads the blocks of b")
	}
	if got := data("c", "a"); got != "/b0" {
		t.Fatalf("a change in a reads the block of c that reads a: %s", got)
	}
}

// TestACardThatLeavesARedrawsBothBoards: a card moved to another project is heard on the board it left and on the
// one it entered.
func TestACardThatLeavesARedrawsBothBoards(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("work", boardManifest)
	e.createProject("home", boardManifest)
	c := e.card(map[string]any{"project": "work", "title": "Move me"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phone := e.live(ctx)
	e.must("deck.card_transfer", map[string]any{"card": c.ID, "project": "home"}, nil)
	heard := map[string]bool{}
	timeout := time.After(5 * time.Second)
	for !heard["work"] || !heard["home"] {
		select {
		case ev := <-phone:
			if ev.Topic == "board" {
				heard[ev.Project] = true
			}
		case <-timeout:
			t.Fatalf("both boards are heard: %v", heard)
		}
	}
}

// TestABlockThatReadsEveryProjectFollowsThemAll: a block whose read names no project follows the changes of every
// project, and a when of another screen does not draw that screen whole for them.
func TestABlockThatReadsEveryProjectFollowsThemAll(t *testing.T) {
	e := newEnv(t, boardWebAgent())
	e.createProject("a", boardWebManifest)
	e.createProject("b", strings.Replace(boardWebManifest, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "list", "title": "All files", "source": {"from": "deck.files_list", "input": {"project": ""}}, "item": {"title": "name"}},
      {"type": "stat", "title": "Some", "when": "len(rows) > 0", "source": {"collection": "notes"}},`, 1))
	msgs := e.screenMessages("b", "main", map[string]any{"topic": "files", "changed": "a"})
	drawn, _ := json.Marshal(msgs)
	if !strings.Contains(string(drawn), `"path":"/b0"`) {
		t.Fatalf("the files of every project are read again for a change in a: %s", drawn)
	}
	if msgs := e.screenMessages("b", "main", map[string]any{"topic": "collection", "changed": "a"}); len(msgs) > 0 && msgs[0]["createSurface"] != nil {
		t.Fatal("a when over b's notes does not draw b whole for a change in a")
	}
}

// TestAWriteTellsTheReadersOfItsTable: a write of a table is an event of the topic its readers follow, in the
// project of the row, whichever command wrote it.
func TestAWriteTellsTheReadersOfItsTable(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("web", boardWebManifest)
	ev := e.live(t.Context())
	e.upload("web", "scan.txt", "text/plain", "a page")
	waitEvent(t, ev, events.Event{Topic: "files", Project: "web"})
}

// waitEvent waits for an event of the topic in the project.
func waitEvent(t *testing.T, ch <-chan events.Event, want events.Event) {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case got := <-ch:
			if got.Topic == want.Topic && got.Project == want.Project {
				return
			}
		case <-timeout:
			t.Fatalf("no %+v event", want)
		}
	}
}
