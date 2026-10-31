package owner

import (
	"bufio"
	"context"
	"deck2/internal/app"
	"deck2/internal/events"
	"deck2/internal/registry"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/schemas"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestASlowDeviceHearsEveryChange: a device that reads its live channel slower than deck publishes still hears
// each topic of each project once; nothing is dropped.
func TestASlowDeviceHearsEveryChange(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phone := e.Events(ctx)
	for i := range 500 {
		e.App.Bus.Emit(events.Event{Topic: "collection", Project: fmt.Sprintf("p%d", i)})
	}
	heard := map[string]bool{}
	for len(heard) < 500 {
		heard[harness.Receive(t, fmt.Sprintf("change %d of 500", len(heard)+1), phone).Project] = true
	}
}

// TestAChangeOfAnotherProjectDoesNotRereadAScreen: a screen reads its blocks again only for a change in a project
// they read, its own or one its manifest reads.
func TestAChangeOfAnotherProjectDoesNotRereadAScreen(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	e.Project("a", harness.WebNotes)
	e.Project("b", harness.WebNotes)
	e.Project("c", strings.Replace(strings.Replace(harness.WebNotes, `"menu": ["main"]}`, `"menu": ["main"], "reads": [{"project": "a", "collections": ["notes"]}]}`, 1),
		`{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "table", "title": "Of a", "source": {"from": "deck.records_list", "input": {"project": "a", "collection": "notes"}}, "columns": [{"label": "Title", "bind": "data.title"}]},`, 1))
	data := func(project, changed string) string {
		var out []string
		for _, m := range e.ScreenMessages(project, "main", map[string]any{"topic": "collection", "changed": changed}) {
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

// TestABlockThatReadsEveryProjectFollowsThemAll: a block whose read names no project follows the changes of every
// project, and a when of another screen does not draw that screen whole for them.
func TestABlockThatReadsEveryProjectFollowsThemAll(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	e.Project("a", harness.WebNotes)
	e.Project("b", strings.Replace(harness.WebNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "list", "title": "All files", "source": {"from": "deck.files_list", "input": {"project": ""}}, "item": {"title": "name"}},
      {"type": "stat", "title": "Some", "when": "len(rows) > 0", "source": {"collection": "notes"}},`, 1))
	msgs := e.ScreenMessages("b", "main", map[string]any{"topic": "files", "changed": "a"})
	drawn, _ := json.Marshal(msgs)
	if !strings.Contains(string(drawn), `"path":"/b0"`) {
		t.Fatalf("the files of every project are read again for a change in a: %s", drawn)
	}
	if msgs := e.ScreenMessages("b", "main", map[string]any{"topic": "collection", "changed": "a"}); len(msgs) > 0 && msgs[0]["createSurface"] != nil {
		t.Fatal("a when over b's notes does not draw b whole for a change in a")
	}
}

// TestAWriteTellsTheReadersOfItsTable: a write of a table is an event of the topic its readers follow, in the
// project of the row, whichever command wrote it.
func TestAWriteTellsTheReadersOfItsTable(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("web", harness.WebNotes)
	ev := e.Events(t.Context())
	e.Upload("web", "scan.txt", "text/plain", "a page")
	harness.WaitEvent(t, ev, events.Event{Topic: "files", Project: "web"})
}

// TestADeckReadWithoutAProjectFollowsEveryProject: a block of a module screen that reads a deck command taking no
// project reads every project, so a change in any project reads it again.
func TestADeckReadWithoutAProjectFollowsEveryProject(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	e.Project("a", harness.WebNotes)
	e.Project("c", strings.Replace(harness.WebNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "table", "title": "Runs", "source": {"from": "deck.spend_daily"}, "columns": [{"label": "Day", "bind": "day"}]},`, 1))
	var paths []string
	for _, m := range e.ScreenMessages("c", "main", map[string]any{"topic": "processes", "changed": "a"}) {
		var dm struct{ Path string }
		if m["updateDataModel"] != nil && json.Unmarshal(m["updateDataModel"], &dm) == nil {
			paths = append(paths, dm.Path)
		}
	}
	if !slices.Contains(paths, "/b0") {
		t.Fatalf("a run in a reads the runs of every project again: %v", paths)
	}
}

// TestAFolderAndAnAssignmentTellOnlyTheirReaders: a folder or an assignment of a project is an event of its own
// topic, not a change of the project that draws its screens anew; a write that changes nothing tells no one.
func TestAFolderAndAnAssignmentTellOnlyTheirReaders(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("web", harness.WebNotes)
	e.Must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "2"}, nil)
	seen := func(write func()) []string {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		ev := e.Events(ctx)
		write()
		var topics []string
		for end := time.After(time.Second); ; {
			select {
			case got := <-ev:
				topics = append(topics, got.Topic)
			case <-end:
				return topics
			}
		}
	}
	if got := seen(func() { e.Must("deck.folder_add", map[string]any{"project": "web", "path": t.TempDir()}, nil) }); !slices.Contains(got, "folders") || slices.Contains(got, "projects") {
		t.Fatalf("a folder tells the readers of folders, not of projects: %v", got)
	}
	if got := seen(func() {
		e.Must("deck.assignment_set", map[string]any{"role": "agent", "project": "web", "models": []string{"fake"}}, nil)
	}); !slices.Contains(got, "assignments") || slices.Contains(got, "projects") {
		t.Fatalf("an assignment tells the readers of assignments, not of projects: %v", got)
	}
	if got := seen(func() { e.Must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "2"}, nil) }); len(got) != 0 {
		t.Fatalf("a write that changes nothing tells no one: %v", got)
	}
}

// TestTheSettingsOfAModuleFollowItsFolders: the settings of a module show its folders, so a new folder reads them
// again.
func TestTheSettingsOfAModuleFollowItsFolders(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("web", harness.WebNotes)
	dir := t.TempDir()
	e.Must("deck.folder_add", map[string]any{"project": "web", "path": dir}, nil)
	drawn, _ := json.Marshal(e.ScreenMessages("web", "settings", map[string]any{"topic": "folders", "changed": "web"}))
	if !strings.Contains(string(drawn), filepath.Base(dir)) {
		t.Fatalf("the settings of the module are read again with the new folder: %s", drawn)
	}
}

// TestTheLiveChannelSaysItIsThere: a channel with nothing to tell pings, so a client finds one that went dead.
func TestTheLiveChannelSaysItIsThere(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.URL+"/api/events", nil)
	e.Sign(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		if sc.Text() == "event: ping" {
			return
		}
	}
	t.Fatal("no ping within 25 s")
}

// TestAChangeOfAnotherWorkspaceStaysThere: a row of a workspace without a project, and the rows a project of another
// workspace takes along when it goes, tell only the readers of that workspace.
func TestAChangeOfAnotherWorkspaceStaysThere(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	ctx := t.Context()
	// A second workspace with a project and its failing check; deck makes no workspace but the owner's.
	e.SQL(`INSERT INTO workspace (id, name) VALUES (2, 'other');
		INSERT INTO project (workspace_id, name, status) VALUES (2, 'theirs', 'enabled');
		INSERT INTO health (workspace_id, project_id, subject, status) VALUES (2, (SELECT id FROM project WHERE name = 'theirs'), 'tool:theirs.get', 'error')`)
	var theirs int64
	// The project of another workspace, which no reader of this one lists.
	if err := e.SQLRow(`SELECT id FROM project WHERE workspace_id = 2 AND name = 'theirs'`).Scan(&theirs); err != nil {
		t.Fatal(err)
	}
	live := e.Events(ctx)
	err := e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.SetHealth(ctx, db.SetHealthParams{WorkspaceID: store.ID(2), Subject: "push", Status: "error", Error: "their pushes"})
		return err
	})
	if err == nil {
		err = e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			_, err := q.DeleteProject(ctx, theirs)
			return err
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	e.Must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
	for ev := range live {
		switch ev.Topic {
		case "settings":
			return
		case "health", "projects":
			t.Fatalf("a change of another workspace reaches this one: %+v", ev)
		}
	}
}

// TestSavingTheSameManifestTellsNoOne: a save that leaves the manifest as it was publishes nothing, so no screen and
// no session of an agent reads its project again.
func TestSavingTheSameManifestTellsNoOne(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("work", harness.PlainManifest)
	var m struct{ Manifest string }
	e.Must("deck.manifest_get", map[string]any{"project": "work"}, &m)
	live := e.Events(t.Context())
	e.Must("deck.manifest_save", map[string]any{"project": "work", "manifest": m.Manifest}, nil)
	e.Must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
	for ev := range live {
		switch ev.Topic {
		case "settings":
			return
		case "projects", "documents":
			t.Fatalf("a save that changes nothing tells: %+v", ev)
		}
	}
}

// TestTheTopicsOfReadsAreTheOnesDeckPublishes: the topics the catalog names are those deck publishes, and a read that
// follows another one keeps deck from starting, so a typo cannot turn off a live screen.
func TestTheTopicsOfReadsAreTheOnesDeckPublishes(t *testing.T) {
	t.Parallel()
	var catalog struct{ Topics []string }
	if err := json.Unmarshal(schemas.Catalog, &catalog); err != nil {
		t.Fatal(err)
	}
	slices.Sort(catalog.Topics)
	if got := store.Topics(); !slices.Equal(got, catalog.Topics) {
		t.Fatalf("the catalog names the topics deck publishes:\n%v\n%v", got, catalog.Topics)
	}
	typo := registry.Read("typo", "Typo", "Follows a topic deck does not publish.", registry.OwnerSide,
		func(context.Context, registry.Actor, struct{}) ([]harness.ProbeRow, error) { return nil, nil }).On("colection")
	defer func() {
		if p := recover(); p == nil || !strings.Contains(fmt.Sprint(p), "colection") {
			t.Fatalf("a read of a topic deck does not publish keeps deck from starting: %v", p)
		}
	}()
	harness.New(t, harness.WithConfig(func(c *app.Config) { c.Commands = append(c.Commands, typo) }))
}
