package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"deck2/internal/provider"
)

// TestProjectDeleteTakesASnapshotAndLeavesNothingOfTheProject: only the owner, with no open process and no reader;
// a snapshot first, then every row, file and folder of deck goes, the name is free again and the journal says so.
func TestProjectDeleteTakesASnapshotAndLeavesNothingOfTheProject(t *testing.T) {
	hold := make(chan struct{})
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "tell" {
			select {
			case <-hold:
			case <-ctx.Done():
			}
		}
		return boardWebAgent()(ctx, c)
	})
	withTrigger := strings.Replace(boardWebManifest, `"board": {`, `"triggers": [{"command": "tell", "cron": "0 9 * * *"}], "board": {`, 1)
	id := e.createProject("gone", withTrigger)
	e.must("deck.records_upsert", map[string]any{"project": "gone", "collection": "notes", "records": jsonl(map[string]any{"title": "alpha"})}, nil)
	e.card(map[string]any{"project": "gone", "title": "A card"})
	e.must("deck.document_save", map[string]any{"project": "gone", "name": "custom/chart.html", "kind": "custom", "text": "<p>x</p>"}, nil)
	scan := e.upload("gone", "scan.txt", "text/plain", "a page")
	e.waitState(e.start("gone", "score", map[string]any{"title": "x", "scan": fmt.Sprint(scan)}), "done")
	e.must("deck.provider_set", map[string]any{"command": "gone.score", "provider": "fake"}, nil)
	data := filepath.Join(e.App.Cfg.Home, "projects", fmt.Sprint(id), "data")
	if err := os.MkdirAll(data, 0o700); err != nil || os.WriteFile(filepath.Join(data, "keep.txt"), []byte("mine"), 0o600) != nil {
		t.Fatal(err)
	}
	e.must("deck.notify", map[string]any{"project": "gone", "text": "done"}, nil)
	ctx := t.Context()
	if _, err := e.App.Store.DB.ExecContext(ctx, `INSERT INTO ask (workspace_id, project_id, kind, text, payload) VALUES (1, ?, 'warning', 'x', '{}')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.App.Store.DB.ExecContext(ctx, `INSERT INTO health (subject, status, error) VALUES ('tool:gone.fetch', 'error', 'x')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.App.Store.DB.ExecContext(ctx, `INSERT INTO tool_call (workspace_id, process_id, project_id, tool, class, outcome, params)
		VALUES (1, NULL, ?, 'gone.publish', 'external', 'done', '{"to": "all"}')`, id); err != nil {
		t.Fatal(err)
	}
	var procs []int64
	for _, x := range e.processes() {
		procs = append(procs, x.ID)
	}
	runDir := filepath.Dir(e.App.Runner.LogPath(procs[0]))
	if _, err := os.Stat(runDir); err != nil {
		t.Fatalf("the fixture has the run folder of a process: %v", err)
	}
	e.createProject("reader", `{"version": 5, "reads": [{"project": "gone", "collections": ["notes"]}]}`)

	_, tok := e.token("terminal", "deck.*")
	if code, _ := e.callWith(tok, "deck.project_delete", map[string]any{"project": "gone"}); code != 403 {
		t.Fatalf("only the owner deletes a project: %d", code)
	}
	e.refused("deck.project_delete", map[string]any{"project": "gone"}, 422, "reader reads gone")
	e.must("deck.project_delete", map[string]any{"project": "reader"}, nil)
	tell := e.start("gone", "tell", nil)
	e.refused("deck.project_delete", map[string]any{"project": "gone"}, 422, "still open")
	close(hold)
	e.waitState(tell, "done")

	tables := map[string]string{
		"record": "project_id = ?1", "card": "project_id = ?1", "document": "project_id = ?1", "file": "project_id = ?1",
		`"trigger"`: "project_id = ?1", "process": "project_id = ?1", "ask": "project_id = ?1", "notification": "project_id = ?1",
		"command_provider": "project_id = ?1", "health": "subject LIKE '%:gone.%' AND ?1 > 0", "tool_call": "project_id = ?1",
	}
	count := func(table string) int {
		var n int
		if err := e.App.Store.DB.QueryRow(`SELECT count(*) FROM `+table+` WHERE `+tables[table], id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for table := range tables {
		if count(table) == 0 {
			t.Fatalf("the fixture has rows of the project in %s", table)
		}
	}
	var out struct{ Deleted, Snapshot string }
	e.must("deck.project_delete", map[string]any{"project": "gone"}, &out)
	if _, err := os.Stat(out.Snapshot); out.Deleted != "gone" || err != nil {
		t.Fatalf("the snapshot is taken before the delete: %+v %v", out, err)
	}
	for table := range tables {
		if n := count(table); n != 0 {
			t.Fatalf("nothing of the project stays in %s: %d", table, n)
		}
	}
	if _, err := os.Stat(filepath.Join(e.App.Cfg.Home, "files", fmt.Sprint(scan))); !os.IsNotExist(err) {
		t.Fatalf("the files of the project go: %v", err)
	}
	for _, dir := range []string{data, runDir} {
		if _, err := os.Stat(dir); !os.IsNotExist(err) {
			t.Fatalf("deck's folders of the project go, since its ids may come again: %s %v", dir, err)
		}
	}
	var journal []map[string]any
	e.must("deck.calls_list", nil, &journal)
	if len(journal) != 3 || journal[0]["tool"] != "deck.project_delete" || journal[0]["class"] != "external" ||
		!strings.Contains(fmt.Sprint(journal[0]["params"]), `"project":"gone"`) || !strings.Contains(fmt.Sprint(journal[1]["params"]), `"project":"reader"`) ||
		journal[2]["outcome"] != "rejected" || journal[2]["actor"] != "token:1" {
		t.Fatalf("the deletes and the refused token are in the journal, the calls of the project are not: %+v", journal)
	}
	var ps []projectV
	e.projectsOf(&ps)
	if len(ps) != 0 {
		t.Fatalf("the project is gone from the list: %+v", ps)
	}
	e.createProject("gone", boardWebManifest)
	if n := len(e.board("gone", false).Lanes[0].Cards); n != 0 {
		t.Fatalf("a new project of the same name starts empty: %d cards", n)
	}
	e.refused("deck.project_delete", map[string]any{"project": "nowhere"}, 404, "nowhere")
}

// TestProjectsTabDeletesAfterAYes: the delete button of a project asks the owner first.
func TestProjectsTabDeletesAfterAYes(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("web", boardWebManifest)
	del := verb(t, findItem(t, blockData[[]listItem](t, e.settingsBlock("Projects", nil)["items"]), "web").Actions, "delete")
	if del["confirm"] != true || del["command"] != "deck.project_delete" {
		t.Fatalf("delete asks the owner first: %+v", del)
	}
	var c struct {
		Class string
	}
	var cmds []struct{ Name, Class string }
	e.must("deck.menu", nil, nil)
	_, raw := e.do("GET", "/api/commands", nil)
	_ = json.Unmarshal(raw, &cmds)
	for _, x := range cmds {
		if x.Name == "deck.project_delete" {
			c.Class = x.Class
		}
	}
	if c.Class != "external" {
		t.Fatalf("deleting a project is external: %q", c.Class)
	}
}

// TestProjectDeleteStopsTheMCPServersOfTheProject: a server of a deleted project does not run on.
func TestProjectDeleteStopsTheMCPServersOfTheProject(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("srv", mcpManifest(""))
	dir := e.mcpDir("srv", "fake")
	e.waitState(e.start("srv", "look", map[string]any{"word": "moon"}), "done")
	pid, _ := os.ReadFile(filepath.Join(dir, "pid"))
	var n int
	fmt.Sscan(string(pid), &n)
	if n == 0 {
		t.Fatalf("the server writes its pid: %q", pid)
	}
	if syscall.Kill(n, 0) != nil {
		t.Skip("the server runs in a pid namespace of its sandbox here; its pid is not one of this system")
	}
	e.must("deck.project_delete", map[string]any{"project": "srv"}, nil)
	e.waitFor("the server to stop", func() bool { return syscall.Kill(n, 0) != nil })
}
