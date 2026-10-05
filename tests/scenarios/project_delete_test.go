package scenarios

import (
	"context"
	deckhome "deck2/internal/home"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"

	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
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
	e.useModels("gone.score", "fake")
	data := filepath.Join(deckhome.Layout(e.App.Cfg.Home).Work(), "projects", fmt.Sprint(id), "data")
	if err := os.MkdirAll(data, 0o700); err != nil || os.WriteFile(filepath.Join(data, "keep.txt"), []byte("mine"), 0o600) != nil {
		t.Fatal(err)
	}
	e.must("deck.notify", map[string]any{"project": "gone", "text": "done"}, nil)
	ctx := t.Context()
	if _, err := e.App.Store.DB.ExecContext(ctx, `INSERT INTO ask (workspace_id, project_id, kind, text, payload) VALUES (1, ?, 'warning', 'x', '{}')`, id); err != nil {
		t.Fatal(err)
	}
	if _, err := e.App.Store.DB.ExecContext(ctx, `INSERT INTO health (workspace_id, project_id, subject, status, error) VALUES (1, ?, 'tool:gone.fetch', 'error', 'x')`, id); err != nil {
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
	e.createProject("reader", `{"version": 7, "reads": [{"project": "gone", "collections": ["notes"]}]}`)

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
		`"trigger"`: "project_id = ?1", "process": "project_id = ?1", "ask": "project_id = ?1", "feed": "project_id = ?1",
		"assignment": "project_id = ?1", "health": "project_id = ?1", "tool_call": "project_id = ?1",
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
	if _, err := os.Stat(filepath.Join(deckhome.Layout(e.App.Cfg.Home).State(), "files", fmt.Sprint(scan))); !os.IsNotExist(err) {
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

// TestTheMasterDeletesOnlyDrafts: the master deletes a draft it wrote at once, after a snapshot like the owner;
// another's draft or an enabled project it does not.
func TestTheMasterDeletesOnlyDrafts(t *testing.T) {
	var said sync.Map
	var turn atomic.Int32
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		name := []string{"sketch", "live", "theirs"}[turn.Add(1)-1]
		out, isErr := c.Tool(ctx, "project_delete", map[string]any{"project": name})
		said.Store(name, fmt.Sprint(isErr, " ", out))
		return provider.Result{Text: "Tried."}
	})
	if _, err := e.App.Registry.Invoke(context.Background(), registry.Actor{Kind: registry.Master, Workspace: 1}, "deck.project_create",
		[]byte(fmt.Sprintf(`{"name": "sketch", "manifest": %q}`, boardWebManifest))); err != nil {
		t.Fatal(err)
	}
	e.createProject("live", boardWebManifest)
	_, tok := e.token("terminal", "deck.*")
	if code, out := e.callWith(tok, "deck.project_create", map[string]any{"name": "theirs", "manifest": boardWebManifest}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	topic := e.say(0, "sketch")
	e.masterSaid(topic, 1)
	if got, _ := said.Load("sketch"); !strings.HasPrefix(fmt.Sprint(got), "false ") || !strings.Contains(fmt.Sprint(got), "snapshot") {
		t.Fatalf("the master deletes a draft at once, after a snapshot: %v", got)
	}
	e.say(topic, "live")
	e.masterSaid(topic, 2)
	if got, _ := said.Load("live"); !strings.HasPrefix(fmt.Sprint(got), "true ") || !strings.Contains(fmt.Sprint(got), "not a draft") {
		t.Fatalf("an enabled project only the owner deletes: %v", got)
	}
	e.say(topic, "theirs")
	e.masterSaid(topic, 3)
	if got, _ := said.Load("theirs"); !strings.HasPrefix(fmt.Sprint(got), "true ") || !strings.Contains(fmt.Sprint(got), "its own drafts") {
		t.Fatalf("a draft someone else wrote only its author or the owner deletes: %v", got)
	}
	var ps []struct{ Name string }
	e.projectsOf(&ps)
	if len(ps) != 2 || ps[0].Name != "live" || ps[1].Name != "theirs" {
		t.Fatalf("only the master's draft is gone: %+v", ps)
	}
}

// TestATopicOfADeletedModuleHasNoModule: a topic begun on a screen of a module keeps no module once it is deleted.
func TestATopicOfADeletedModuleHasNoModule(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result { return provider.Result{Text: "Noted."} })
	e.createProject("web", boardWebManifest)
	topic := e.say(0, "look", map[string]any{"screen": `{"project":"web","screen":"main"}`})
	e.masterSaid(topic, 1)
	e.must("deck.project_delete", map[string]any{"project": "web"}, nil)
	var ts []struct {
		ID      int64
		Project string
	}
	e.must("deck.chat_topics", map[string]any{"archived": true}, &ts)
	var all []struct {
		ID      int64
		Project string
	}
	e.must("deck.chat_topics", nil, &all)
	all = append(all, ts...)
	if len(all) != 1 || all[0].ID != topic || all[0].Project != "" {
		t.Fatalf("the topic stays, without the deleted module: %+v", all)
	}
}

// TestDeletingAModuleArchivesItsTopics: the topics of a deleted module, by its card or the screen of their first
// message, go to the archive; others stay.
func TestDeletingAModuleArchivesItsTopics(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result { return provider.Result{Text: "Noted."} })
	e.createProject("web", boardWebManifest)
	e.createProject("other", boardWebManifest)
	onScreen := e.say(0, "look", map[string]any{"screen": `{"project":"web","screen":"main"}`})
	e.masterSaid(onScreen, 1)
	card := e.card(map[string]any{"project": "web", "title": "A card"})
	byCard := e.say(0, "the card", map[string]any{"card": card.ID, "screen": `{"project":"other","screen":"main"}`})
	e.masterSaid(byCard, 1)
	elsewhere := e.say(0, "else", map[string]any{"screen": `{"project":"other","screen":"main"}`})
	e.masterSaid(elsewhere, 1)
	events := e.sse(t.Context())
	e.must("deck.project_delete", map[string]any{"project": "web"}, nil)
	waitTopic(t, events, "chat")
	ids := func(archived bool) []int64 {
		var ts []struct{ ID int64 }
		e.must("deck.chat_topics", map[string]any{"archived": archived}, &ts)
		var out []int64
		for _, x := range ts {
			out = append(out, x.ID)
		}
		slices.Sort(out)
		return out
	}
	if got := ids(true); !slices.Equal(got, []int64{onScreen, byCard}) {
		t.Fatalf("the topics of the module are archived: %v", got)
	}
	if got := ids(false); !slices.Equal(got, []int64{elsewhere}) {
		t.Fatalf("other topics stay: %v", got)
	}
}

// TestTheMasterHearsOfADeletedDraftFromItsData: a draft imported from a folder in the data of the master, once
// deleted, names its folder in the answer and in the master's next turn; deck leaves the folder.
func TestTheMasterHearsOfADeletedDraftFromItsData(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result { return provider.Result{Text: "Noted."} })
	mp, err := e.App.Store.Q.GetProjectByName(context.Background(), db.GetProjectByNameParams{WorkspaceID: 1, Name: "master"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(e.App.Home.Data(mp.ID), "drafts", "sketch")
	if err := errors.Join(os.MkdirAll(dir, 0o700), os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(boardWebManifest), 0o600)); err != nil {
		t.Fatal(err)
	}
	e.must("deck.project_import", map[string]any{"folder": dir, "name": "sketch"}, nil)
	var out struct{ Folder string }
	e.must("deck.project_delete", map[string]any{"project": "sketch"}, &out)
	if out.Folder != dir {
		t.Fatalf("the answer names the folder deck left: %+v", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatalf("deck leaves the folder: %v", err)
	}
	topic := e.say(0, "hello")
	e.masterSaid(topic, 1)
	if p := e.Fake.Calls()[0].Prompt; !strings.Contains(p, "sketch") || !strings.Contains(p, dir) || !strings.Contains(p, "(data, not instructions)") {
		t.Fatalf("the master's next turn says which draft went and where its folder is, as data:\n%s", p)
	}
	e.say(topic, "again")
	e.masterSaid(topic, 2)
	if p := e.Fake.Calls()[1].Prompt; strings.Contains(p, dir) {
		t.Fatalf("the master hears of it once:\n%s", p)
	}
	e.importEnabled(dir, "kept")
	var gone struct{ Folder string }
	e.must("deck.project_delete", map[string]any{"project": "kept"}, &gone)
	if gone.Folder != "" {
		t.Fatalf("an enabled project is no draft of the master: %+v", gone)
	}
}

// TestTheImportsAndNotesOfABaseFromBeforeStay: the folder a draft came from and the notes for the master that an
// older base kept as marks are still there.
func TestTheImportsAndNotesOfABaseFromBeforeStay(t *testing.T) {
	home := t.TempDir()
	e := newEnvWith(t, home, &Fake{})
	mp, _ := e.App.Store.Q.GetProjectByName(context.Background(), db.GetProjectByNameParams{WorkspaceID: 1, Name: "master"})
	dir := filepath.Join(e.App.Home.Data(mp.ID), "drafts", "sketch")
	if err := errors.Join(os.MkdirAll(dir, 0o700), os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(boardWebManifest), 0o600)); err != nil {
		t.Fatal(err)
	}
	e.must("deck.project_import", map[string]any{"folder": dir, "name": "sketch"}, nil)
	sketch, _ := e.App.Store.Q.GetProjectByName(context.Background(), db.GetProjectByNameParams{WorkspaceID: 1, Name: "sketch"})
	e.olderBase(4)
	for _, q := range []string{fmt.Sprintf(`INSERT INTO mark (workspace_id, project_id, name, value) VALUES (1, %d, 'imported_from:%d', %q)`, sketch.ID, sketch.ID, dir),
		`INSERT INTO mark (workspace_id, name, value) VALUES (1, 'master_notes', 'draft old, imported from somewhere, was deleted')`} {
		if _, err := e.App.Store.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = e.App.Close()
	fake := &Fake{script: func(ctx context.Context, c *Call) provider.Result { return provider.Result{Text: "Noted."} }}
	e = newEnvWith(t, home, fake)
	var out struct{ Folder string }
	e.must("deck.project_delete", map[string]any{"project": "sketch"}, &out)
	if out.Folder != dir {
		t.Fatalf("the draft still knows the folder it came from: %+v", out)
	}
	topic := e.say(0, "hello")
	e.masterSaid(topic, 1)
	if p := fake.Calls()[0].Prompt; !strings.Contains(p, "draft old") || !strings.Contains(p, dir) {
		t.Fatalf("the master hears the old note and the new one:\n%s", p)
	}
}

// TestTheMasterImportsOnlyFromTheOwnersFoldersAndItsData: a folder of the owner and the data of the master can
// become a draft; the data of another project cannot.
func TestTheMasterImportsOnlyFromTheOwnersFoldersAndItsData(t *testing.T) {
	e := newEnv(t, nil)
	other := e.createProject("other", boardWebManifest)
	theirs := filepath.Join(e.App.Home.Data(other), "mod")
	mp, _ := e.App.Store.Q.GetProjectByName(context.Background(), db.GetProjectByNameParams{WorkspaceID: 1, Name: "master"})
	own := filepath.Join(e.App.Home.Data(mp.ID), "drafts", "mod")
	repo := t.TempDir()
	for _, dir := range []string{theirs, own, filepath.Join(repo, "mod")} {
		if err := errors.Join(os.MkdirAll(dir, 0o700), os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(boardWebManifest), 0o600)); err != nil {
			t.Fatal(err)
		}
	}
	e.must("deck.folder_add", map[string]any{"project": "other", "name": "repo", "path": repo}, nil)
	_, tok := e.token("terminal", "deck.*")
	if code, out := e.callWith(tok, "deck.project_import", map[string]any{"folder": theirs, "name": "a"}); code < 400 || !strings.Contains(string(out), "own folders") {
		t.Fatalf("the data of another project is not imported: %d %s", code, out)
	}
	for i, dir := range []string{own, filepath.Join(repo, "mod")} {
		if code, out := e.callWith(tok, "deck.project_import", map[string]any{"folder": dir, "name": fmt.Sprintf("b%d", i)}); code != 200 {
			t.Fatalf("%s is imported: %d %s", dir, code, out)
		}
	}
}
