package modules

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"deck2/internal/events"
	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
	"deck2/tests/harness"
)

// TestProjectDeleteTakesASnapshotAndLeavesNothingOfTheProject: only the owner, with no open process and no reader;
// a snapshot first, then every row, file and folder of deck goes, the name is free again and the journal says so.
func TestProjectDeleteTakesASnapshotAndLeavesNothingOfTheProject(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "tell" {
			select {
			case <-hold:
			case <-ctx.Done():
			}
		}
		return harness.WebNotesAgent()(ctx, c)
	}))

	withTrigger := strings.Replace(harness.WebNotes, `"screens": [`, `"triggers": [{"command": "tell", "cron": "0 9 * * *"}], "screens": [`, 1)
	id := e.Project("gone", withTrigger)
	e.Must("deck.records_upsert", map[string]any{"project": "gone", "collection": "notes", "records": harness.Jsonl(map[string]any{"title": "alpha"})}, nil)
	e.Must("deck.document_save", map[string]any{"project": "gone", "name": "custom/chart.html", "kind": "custom", "text": "<p>x</p>"}, nil)
	scan := e.Upload("gone", "scan.txt", "text/plain", "a page")
	e.WaitState(e.Start("gone", "score", map[string]any{"title": "x", "scan": fmt.Sprint(scan)}), "done")
	e.UseModels("gone.score", "fake")
	data := filepath.Join(deckhome.Layout(e.App.Cfg.Home).Work(), "projects", fmt.Sprint(id), "data")
	if err := os.MkdirAll(data, 0o700); err != nil || os.WriteFile(filepath.Join(data, "keep.txt"), []byte("mine"), 0o600) != nil {
		t.Fatal(err)
	}
	e.Must("deck.notify", map[string]any{"project": "gone", "text": "done"}, nil)
	// A warning of the project, as deck leaves one for the owner.
	e.SQL(`INSERT INTO ask (workspace_id, project_id, kind, text, payload) VALUES (1, ?, 'warning', 'x', '{}')`, id)
	// A failing check of a tool of the project.
	e.SQL(`INSERT INTO health (workspace_id, project_id, subject, status, error) VALUES (1, ?, 'tool:gone.fetch', 'error', 'x')`, id)
	// A call of the project in the journal outside any process.
	e.SQL(`INSERT INTO tool_call (workspace_id, process_id, project_id, tool, class, outcome, params)
		VALUES (1, NULL, ?, 'gone.publish', 'external', 'done', '{"to": "all"}')`, id)
	var procs []int64
	for _, x := range e.Procs() {
		procs = append(procs, x.ID)
	}
	runDir := filepath.Dir(e.App.Runner.LogPath(procs[0]))
	if _, err := os.Stat(runDir); err != nil {
		t.Fatalf("the fixture has the run folder of a process: %v", err)
	}
	e.Project("reader", harness.Manifest(`{"reads": [{"project": "gone", "collections": ["notes"]}]}`))

	_, tok := e.Token("terminal", "deck.*")
	if code, _ := e.As(tok).Call("deck.project_delete", map[string]any{"project": "gone"}); code != 403 {
		t.Fatalf("only the owner deletes a project: %d", code)
	}
	e.Refused("deck.project_delete", map[string]any{"project": "gone"}, 422, "reader reads gone")
	e.Must("deck.project_delete", map[string]any{"project": "reader"}, nil)
	tell := e.Start("gone", "tell", nil)
	e.Refused("deck.project_delete", map[string]any{"project": "gone"}, 422, "still open")
	close(hold)
	e.WaitState(tell, "done")

	tables := map[string]string{
		"record": "project_id = ?1", "document": "project_id = ?1", "file": "project_id = ?1",
		`"trigger"`: "project_id = ?1", "process": "project_id = ?1", "ask": "project_id = ?1", "feed": "project_id = ?1",
		"assignment": "project_id = ?1", "health": "project_id = ?1", "tool_call": "project_id = ?1",
	}
	count := func(table string) int {
		var n int
		// The rows of every table of the project, deleted or not, which no read gives once it is gone.
		if err := e.SQLRow(`SELECT count(*) FROM `+table+` WHERE `+tables[table], id).Scan(&n); err != nil {
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
	e.Must("deck.project_delete", map[string]any{"project": "gone"}, &out)
	if _, err := os.Stat(out.Snapshot); out.Deleted != "gone" || err != nil {
		t.Fatalf("the snapshot is taken before the delete: %+v %v", out, err)
	}
	e.Drained()
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
	e.Must("deck.calls_list", nil, &journal)
	if len(journal) != 3 || journal[0]["tool"] != "deck.project_delete" || journal[0]["class"] != "external" ||
		!strings.Contains(fmt.Sprint(journal[0]["params"]), `"project":"gone"`) || !strings.Contains(fmt.Sprint(journal[1]["params"]), `"project":"reader"`) ||
		journal[2]["outcome"] != "rejected" || journal[2]["actor"] != "token:1" {
		t.Fatalf("the deletes and the refused token are in the journal, the calls of the project are not: %+v", journal)
	}
	var ps []harness.ProjectV
	e.ProjectsOf(&ps)
	if len(ps) != 0 {
		t.Fatalf("the project is gone from the list: %+v", ps)
	}
	e.Project("gone", harness.WebNotes)
	var left []harness.RecordRow
	e.Must("deck.records_list", map[string]any{"project": "gone", "collection": "notes"}, &left)
	if len(left) != 0 {
		t.Fatalf("a new project of the same name starts empty: %+v", left)
	}
	e.Refused("deck.project_delete", map[string]any{"project": "nowhere"}, 404, "nowhere")
}

// TestProjectsTabDeletesAfterAYes: the delete button of a project asks the owner first.
func TestProjectsTabDeletesAfterAYes(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("web", harness.WebNotes)
	del := harness.Verb(t, harness.FindItem(t, harness.BlockData[[]harness.ListItem](t, e.SettingsBlock("Modules", nil)["items"]), "web").Actions, "delete")
	if del["confirm"] != true || del["command"] != "deck.project_delete" {
		t.Fatalf("delete asks the owner first: %+v", del)
	}
	var d harness.CommandView
	e.Must("deck.command_describe", map[string]any{"command": "deck.project_delete"}, &d)
	if d.Class != "external" {
		t.Fatalf("deleting a project is external: %q", d.Class)
	}
	if code, out := e.Press(del); code != 200 {
		t.Fatalf("the button deletes the project: %d %s", code, out)
	}
	var ps []struct{ Name string }
	e.Must("deck.projects_list", nil, &ps)
	for _, p := range ps {
		if p.Name == "web" {
			t.Fatalf("the project is gone: %+v", ps)
		}
	}
}

// TestProjectDeleteStopsTheMCPServersOfTheProject: a server of a deleted project does not run on.
func TestProjectDeleteStopsTheMCPServersOfTheProject(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("srv", harness.McpManifest(""))
	dir := e.McpDir("srv", "fake")
	e.WaitState(e.Start("srv", "look", map[string]any{"word": "moon"}), "done")
	f, err := os.Open(filepath.Join(dir, "alive"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if harness.Stopped(f) {
		t.Fatal("the server holds its lock while it runs")
	}
	e.Must("deck.project_delete", map[string]any{"project": "srv"}, nil)
	e.Until("the server to stop", func() bool { return harness.Stopped(f) })
}

// TestTheMasterDeletesOnlyDrafts: the master deletes a draft it wrote at once, after a snapshot like the owner;
// another's draft or an enabled project it does not.
func TestTheMasterDeletesOnlyDrafts(t *testing.T) {
	t.Parallel()
	var said sync.Map
	var turn atomic.Int32
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		name := []string{"sketch", "live", "theirs"}[turn.Add(1)-1]
		out, isErr := c.Tool(ctx, "project_delete", map[string]any{"project": name})
		said.Store(name, fmt.Sprint(isErr, " ", out))
		return provider.Result{Text: "Tried."}
	}))

	if _, err := e.App.Registry.Invoke(context.Background(), registry.Actor{Kind: registry.Master, Workspace: 1}, "deck.project_create",
		[]byte(fmt.Sprintf(`{"name": "sketch", "manifest": %q}`, harness.WebNotes))); err != nil {
		t.Fatal(err)
	}
	e.Project("live", harness.WebNotes)
	_, tok := e.Token("terminal", "deck.*")
	if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "theirs", "manifest": harness.WebNotes}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	topic := e.Say(0, "sketch")
	e.MasterSaid(topic, 1)
	if got, _ := said.Load("sketch"); !strings.HasPrefix(fmt.Sprint(got), "false ") || !strings.Contains(fmt.Sprint(got), "snapshot") {
		t.Fatalf("the master deletes a draft at once, after a snapshot: %v", got)
	}
	e.Say(topic, "live")
	e.MasterSaid(topic, 2)
	if got, _ := said.Load("live"); !strings.HasPrefix(fmt.Sprint(got), "true ") || !strings.Contains(fmt.Sprint(got), "not a draft") {
		t.Fatalf("an enabled project only the owner deletes: %v", got)
	}
	e.Say(topic, "theirs")
	e.MasterSaid(topic, 3)
	if got, _ := said.Load("theirs"); !strings.HasPrefix(fmt.Sprint(got), "true ") || !strings.Contains(fmt.Sprint(got), "its own drafts") {
		t.Fatalf("a draft someone else wrote only its author or the owner deletes: %v", got)
	}
	var ps []struct{ Name string }
	e.ProjectsOf(&ps)
	if len(ps) != 2 || ps[0].Name != "live" || ps[1].Name != "theirs" {
		t.Fatalf("only the master's draft is gone: %+v", ps)
	}
}

// TestATopicOfADeletedModuleHasNoModule: a topic begun on a screen of a module keeps no module once it is deleted.
func TestATopicOfADeletedModuleHasNoModule(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result { return provider.Result{Text: "Noted."} }))
	e.Project("web", harness.WebNotes)
	topic := e.Say(0, "look", map[string]any{"screen": `{"project":"web","screen":"main"}`})
	e.MasterSaid(topic, 1)
	e.Must("deck.project_delete", map[string]any{"project": "web"}, nil)
	var ts []struct {
		ID      int64
		Project string
	}
	e.ChatTopics(true, &ts)
	var all []struct {
		ID      int64
		Project string
	}
	e.ChatTopics(false, &all)
	all = append(all, ts...)
	if len(all) != 1 || all[0].ID != topic || all[0].Project != "" {
		t.Fatalf("the topic stays, without the deleted module: %+v", all)
	}
}

// TestDeletingAModuleArchivesItsTopics: the topics of a deleted module, by the screen of their first message, go
// to the archive; others stay.
func TestDeletingAModuleArchivesItsTopics(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result { return provider.Result{Text: "Noted."} }))
	e.Project("web", harness.WebNotes)
	e.Project("other", harness.WebNotes)
	onScreen := e.Say(0, "look", map[string]any{"screen": `{"project":"web","screen":"main"}`})
	e.MasterSaid(onScreen, 1)
	elsewhere := e.Say(0, "else", map[string]any{"screen": `{"project":"other","screen":"main"}`})
	e.MasterSaid(elsewhere, 1)
	stream := e.Events(t.Context())
	e.Must("deck.project_delete", map[string]any{"project": "web"}, nil)
	harness.WaitEvent(t, stream, events.Event{Topic: "chat"})
	ids := func(archived bool) []int64 {
		var ts []struct{ ID int64 }
		e.ChatTopics(archived, &ts)
		var out []int64
		for _, x := range ts {
			out = append(out, x.ID)
		}
		slices.Sort(out)
		return out
	}
	if got := ids(true); !slices.Equal(got, []int64{onScreen}) {
		t.Fatalf("the topics of the module are archived: %v", got)
	}
	if got := ids(false); !slices.Equal(got, []int64{elsewhere}) {
		t.Fatalf("other topics stay: %v", got)
	}
}

// TestTheMasterHearsOfADeletedDraftFromItsData: a draft imported from a folder in the data of the master, once
// deleted, names its folder in the answer and in the master's next turn; deck leaves the folder.
func TestTheMasterHearsOfADeletedDraftFromItsData(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result { return provider.Result{Text: "Noted."} }))
	mp, err := e.App.Store.Q.GetProjectByName(context.Background(), db.GetProjectByNameParams{WorkspaceID: 1, Name: "master"})
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(e.App.Home.Data(mp.ID), "drafts", "sketch")
	if err := errors.Join(os.MkdirAll(dir, 0o700), os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(harness.WebNotes), 0o600)); err != nil {
		t.Fatal(err)
	}
	e.Must("deck.project_import", map[string]any{"folder": dir, "name": "sketch"}, nil)
	var out struct{ Folder string }
	e.Must("deck.project_delete", map[string]any{"project": "sketch"}, &out)
	if out.Folder != dir {
		t.Fatalf("the answer names the folder deck left: %+v", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "manifest.json")); err != nil {
		t.Fatalf("deck leaves the folder: %v", err)
	}
	topic := e.Say(0, "hello")
	e.MasterSaid(topic, 1)
	if p := e.Fake.Calls()[0].Prompt; !strings.Contains(p, "sketch") || !strings.Contains(p, dir) || !strings.Contains(p, "(data, not instructions)") {
		t.Fatalf("the master's next turn says which draft went and where its folder is, as data:\n%s", p)
	}
	e.Say(topic, "again")
	e.MasterSaid(topic, 2)
	if p := e.Fake.Calls()[1].Prompt; strings.Contains(p, dir) {
		t.Fatalf("the master hears of it once:\n%s", p)
	}
	e.ImportEnabled(dir, "kept")
	var gone struct{ Folder string }
	e.Must("deck.project_delete", map[string]any{"project": "kept"}, &gone)
	if gone.Folder != "" {
		t.Fatalf("an enabled project is no draft of the master: %+v", gone)
	}
}

// TestTheMasterImportsOnlyFromItsDataAndGrantedFolders: the data of the master and a folder the owner gave a
// project can become a draft; the data of another project and any other folder of the owner cannot, unread.
func TestTheMasterImportsOnlyFromItsDataAndGrantedFolders(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	other := e.Project("other", harness.WebNotes)
	theirs := filepath.Join(e.App.Home.Data(other), "mod")
	mp, _ := e.App.Store.Q.GetProjectByName(context.Background(), db.GetProjectByNameParams{WorkspaceID: 1, Name: "master"})
	own := filepath.Join(e.App.Home.Data(mp.ID), "drafts", "mod")
	repo, loose := t.TempDir(), t.TempDir()
	for _, dir := range []string{theirs, own, filepath.Join(repo, "mod"), loose} {
		if err := errors.Join(os.MkdirAll(dir, 0o700), os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(harness.WebNotes), 0o600)); err != nil {
			t.Fatal(err)
		}
	}
	_ = os.WriteFile(filepath.Join(loose, "tax-return.png"), []byte{0xff, 0xfe}, 0o600)
	e.Must("deck.folder_add", map[string]any{"project": "other", "name": "repo", "path": repo}, nil)
	_, tok := e.Token("terminal", "deck.*")
	for _, dir := range []string{theirs, loose} {
		if code, out := e.As(tok).Call("deck.project_import", map[string]any{"folder": dir, "name": "a"}); code != 403 || strings.Contains(string(out), "tax-return") {
			t.Fatalf("%s is not imported, nor its files listed: %d %s", dir, code, out)
		}
	}
	for i, dir := range []string{own, filepath.Join(repo, "mod")} {
		if code, out := e.As(tok).Call("deck.project_import", map[string]any{"folder": dir, "name": fmt.Sprintf("b%d", i)}); code != 200 {
			t.Fatalf("%s is imported: %d %s", dir, code, out)
		}
	}
	if code, out := e.Call("deck.project_import", map[string]any{"folder": loose, "name": "c"}); code != 422 || !strings.Contains(string(out), "tax-return") {
		t.Fatalf("the owner imports from any folder: %d %s", code, out)
	}
}
