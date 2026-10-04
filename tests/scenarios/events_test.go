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
