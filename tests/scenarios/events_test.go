package scenarios

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/events"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// TestASlowDeviceHearsEveryChange: a device that reads its live channel slower than deck publishes still hears
// each topic of each project once; nothing is dropped.
func TestASlowDeviceHearsEveryChange(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phone := e.live(ctx)
	for i := range 500 {
		e.App.Bus.Emit(events.Event{Topic: "collection", Project: fmt.Sprintf("p%d", i)})
	}
	heard := map[string]bool{}
	timeout := time.After(within(10 * time.Second))
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
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	e.createProject("a", webNotes)
	e.createProject("b", webNotes)
	e.createProject("c", strings.Replace(strings.Replace(webNotes, `"menu": ["main"]}`, `"menu": ["main"], "reads": [{"project": "a", "collections": ["notes"]}]}`, 1),
		`{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "table", "title": "Of a", "source": {"from": "deck.records_list", "input": {"project": "a", "collection": "notes"}}, "columns": [{"label": "Title", "bind": "data.title"}]},`, 1))
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

// TestABlockThatReadsEveryProjectFollowsThemAll: a block whose read names no project follows the changes of every
// project, and a when of another screen does not draw that screen whole for them.
func TestABlockThatReadsEveryProjectFollowsThemAll(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	e.createProject("a", webNotes)
	e.createProject("b", strings.Replace(webNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
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
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("web", webNotes)
	ev := e.live(t.Context())
	e.upload("web", "scan.txt", "text/plain", "a page")
	waitEvent(t, ev, events.Event{Topic: "files", Project: "web"})
}

// waitEvent waits for an event of the topic in the project.
func waitEvent(t *testing.T, ch <-chan events.Event, want events.Event) {
	t.Helper()
	timeout := time.After(within(5 * time.Second))
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

// TestADeckReadWithoutAProjectFollowsEveryProject: a block of a module screen that reads a deck command taking no
// project reads every project, so a change in any project reads it again.
func TestADeckReadWithoutAProjectFollowsEveryProject(t *testing.T) {
	t.Parallel()
	e := newEnv(t, webNotesAgent())
	e.createProject("a", webNotes)
	e.createProject("c", strings.Replace(webNotes, `{"name": "main", "title": "Web", "blocks": [`, `{"name": "main", "title": "Web", "blocks": [
      {"type": "table", "title": "Runs", "source": {"from": "deck.spend_daily"}, "columns": [{"label": "Day", "bind": "day"}]},`, 1))
	var paths []string
	for _, m := range e.screenMessages("c", "main", map[string]any{"topic": "processes", "changed": "a"}) {
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
	e := newEnv(t, nil)
	e.createProject("web", webNotes)
	e.must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "2"}, nil)
	seen := func(write func()) []string {
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		ev := e.live(ctx)
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
	if got := seen(func() { e.must("deck.folder_add", map[string]any{"project": "web", "path": t.TempDir()}, nil) }); !slices.Contains(got, "folders") || slices.Contains(got, "projects") {
		t.Fatalf("a folder tells the readers of folders, not of projects: %v", got)
	}
	if got := seen(func() {
		e.must("deck.assignment_set", map[string]any{"role": "agent", "project": "web", "models": []string{"fake"}}, nil)
	}); !slices.Contains(got, "assignments") || slices.Contains(got, "projects") {
		t.Fatalf("an assignment tells the readers of assignments, not of projects: %v", got)
	}
	if got := seen(func() { e.must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "2"}, nil) }); len(got) != 0 {
		t.Fatalf("a write that changes nothing tells no one: %v", got)
	}
}

// TestTheSettingsOfAModuleFollowItsFolders: the settings of a module show its folders, so a new folder reads them
// again.
func TestTheSettingsOfAModuleFollowItsFolders(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("web", webNotes)
	dir := t.TempDir()
	e.must("deck.folder_add", map[string]any{"project": "web", "path": dir}, nil)
	drawn, _ := json.Marshal(e.screenMessages("web", "settings", map[string]any{"topic": "folders", "changed": "web"}))
	if !strings.Contains(string(drawn), filepath.Base(dir)) {
		t.Fatalf("the settings of the module are read again with the new folder: %s", drawn)
	}
}

// TestTheLiveChannelSaysItIsThere: a channel with nothing to tell pings, so a client finds one that went dead.
func TestTheLiveChannelSaysItIsThere(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, "GET", e.URL+"/api/events", nil)
	req.Header.Set("Authorization", "Bearer "+token)
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
	e := newEnv(t, nil)
	ctx := t.Context()
	if _, err := e.App.Store.DB.ExecContext(ctx, `INSERT INTO workspace (id, name) VALUES (2, 'other')`); err != nil {
		t.Fatal(err)
	}
	var theirs db.Project
	err := e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		var err error
		if theirs, err = q.CreateProject(ctx, db.CreateProjectParams{WorkspaceID: 2, Name: "theirs", Status: "enabled"}); err != nil {
			return err
		}
		_, err = q.SetHealth(ctx, db.SetHealthParams{WorkspaceID: store.ID(2), ProjectID: store.ID(theirs.ID), Subject: "tool:theirs.get", Status: "error"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	live := e.live(ctx)
	err = e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		_, err := q.SetHealth(ctx, db.SetHealthParams{WorkspaceID: store.ID(2), Subject: "push", Status: "error", Error: "their pushes"})
		return err
	})
	if err == nil {
		err = e.App.Store.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
			_, err := q.DeleteProject(ctx, theirs.ID)
			return err
		})
	}
	if err != nil {
		t.Fatal(err)
	}
	e.must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
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
	e := newEnv(t, nil)
	e.createProject("work", plainManifest)
	var m struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "work"}, &m)
	live := e.live(t.Context())
	e.must("deck.manifest_save", map[string]any{"project": "work", "manifest": m.Manifest}, nil)
	e.must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
	for ev := range live {
		switch ev.Topic {
		case "settings":
			return
		case "projects", "documents":
			t.Fatalf("a save that changes nothing tells: %+v", ev)
		}
	}
}
