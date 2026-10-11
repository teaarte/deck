package scenarios

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	deckhome "deck2/internal/home"
	"deck2/internal/provider"
)

// cascadeModule keeps videos, each with a folder of the data folder its field dir names, the scenes of each video,
// a script that renders by a record and an agent that watches one.
const cascadeModule = `{"version": 11,
  "collections": [
    {"name": "videos", "description": "A video.", "key": "slug", "display": "title", "folder": "'videos/' + dir",
      "schema": {"type": "object", "properties": {"slug": {"type": "string"}, "title": {"type": "string"}, "dir": {"type": "string"}}, "required": ["slug"]}},
    {"name": "scenes", "description": "A scene of a video.", "key": "video + '-' + string(n)", "parent": {"collection": "videos", "key": "video"},
      "schema": {"type": "object", "properties": {"video": {"type": "string"}, "n": {"type": "integer"}}, "required": ["video", "n"]}}],
  "commands": [
    {"name": "render", "title": "Render", "description": "Renders a video.", "class": "internal",
     "input": {"type": "object", "properties": {"record": {"type": "integer"}, "take": {"type": "integer"}}, "required": ["record"]},
     "result": {"type": "object"}, "executor": {"type": "script", "script": "render"}},
    {"name": "watch", "title": "Watch", "description": "Watches a video.", "class": "internal",
     "input": {"type": "object", "properties": {"record": {"type": "integer"}}, "required": ["record"]},
     "executor": {"type": "agent", "prompt": "Watch it."}}],
  "scripts": [{"name": "render", "file": "scripts/render.py", "runtime": "python"}]}`

// renderScript writes a frame of the record it renders and a log of its own run.
const renderScript = `import json, os, sys
inp = json.load(sys.stdin)
res = os.environ["DECK_RESULTS"]
open(os.path.join(res, "frame.png"), "w").write("png")
open(os.path.join(res, "log.txt"), "w").write("log")
print(json.dumps({"files": [{"path": "frame.png", "record": inp["record"]}, {"path": "log.txt"}]}))
`

// videos writes videos to the cascade module and gives the id of each by its slug.
func (e *Env) videos(recs ...map[string]any) map[string]int64 {
	e.T.Helper()
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(recs...)}, nil)
	var all []struct {
		ID  int64
		Key string
	}
	e.must("deck.records_list", map[string]any{"project": "shorts", "collection": "videos"}, &all)
	ids := map[string]int64{}
	for _, v := range all {
		ids[v.Key] = v.ID
	}
	return ids
}

// partNames are what record_parts lists for a record, as kind and name.
func (e *Env) partNames(record int64) string {
	e.T.Helper()
	var parts []struct{ Kind, Name string }
	e.must("deck.record_parts", map[string]any{"record": record}, &parts)
	var names []string
	for _, p := range parts {
		names = append(names, p.Kind+" "+p.Name)
	}
	return strings.Join(names, "|")
}

func exists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

// TestARecordFolderGoesOnlyWhenNoOtherRecordHoldsIt: a folder an empty field, . or .. gives is no folder; one another
// record also gives, or one inside or around another record's, stays.
func TestARecordFolderGoesOnlyWhenNoOtherRecordHoldsIt(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	pid := e.createProject("shorts", cascadeModule)
	data := deckhome.Layout(e.App.Cfg.Home).Data(pid)
	for _, d := range []string{"keep", "shared", "nest/inner"} {
		if err := os.MkdirAll(filepath.Join(data, "videos", d), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(data, "videos", d, "f.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ids := e.videos(map[string]any{"slug": "empty", "dir": ""}, map[string]any{"slug": "up", "dir": "x/../.."}, map[string]any{"slug": "dot", "dir": "."},
		map[string]any{"slug": "c", "dir": "shared"}, map[string]any{"slug": "d", "dir": "shared"},
		map[string]any{"slug": "outer", "dir": "nest"}, map[string]any{"slug": "inner", "dir": "nest/inner"})
	for _, k := range []string{"empty", "up", "dot", "c", "outer"} {
		if got := e.partNames(ids[k]); strings.Contains(got, "folder") {
			t.Fatalf("%s takes no folder: %s", k, got)
		}
		e.must("deck.record_delete", map[string]any{"record": ids[k]}, nil)
	}
	if !exists(filepath.Join(data, "videos", "keep", "f.txt")) || !exists(filepath.Join(data, "videos", "shared", "f.txt")) ||
		!exists(filepath.Join(data, "videos", "nest", "inner", "f.txt")) {
		t.Fatal("no folder another record holds, nor the folder of the collection or the data folder, goes")
	}
	if _, err := e.App.Store.DB.Exec(`INSERT INTO outbox (kind, ref, path) VALUES ('folder', ?, 'videos/shared')`, pid); err != nil {
		t.Fatal(err)
	}
	e.App.Sweep(context.Background(), time.Now())
	if !exists(filepath.Join(data, "videos", "shared", "f.txt")) {
		t.Fatal("the outbox leaves a folder a record holds by the time it gets to it")
	}
	if got := e.partNames(ids["d"]); !strings.Contains(got, "folder videos/shared/") {
		t.Fatalf("the last record that holds a folder takes it: %s", got)
	}
	e.must("deck.record_delete", map[string]any{"record": ids["d"]}, nil)
	e.must("deck.record_delete", map[string]any{"record": ids["inner"]}, nil)
	if exists(filepath.Join(data, "videos", "shared")) || exists(filepath.Join(data, "videos", "nest", "inner")) || !exists(filepath.Join(data, "videos", "keep")) {
		t.Fatal("a folder no other record holds goes with its record")
	}
}

// TestAnExpressionErrorStopsADeletion: a folder deck cannot work out stops record_parts and the deletion, which
// takes nothing.
func TestAnExpressionErrorStopsADeletion(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("shorts", cascadeModule)
	ids := e.videos(map[string]any{"slug": "nodir"})
	if code, out := e.call("deck.record_parts", map[string]any{"record": ids["nodir"]}); code != 422 || !strings.Contains(string(out), "folder") {
		t.Fatalf("record_parts says the folder could not be worked out: %d %s", code, out)
	}
	if code, out := e.call("deck.record_delete", map[string]any{"record": ids["nodir"]}); code != 422 || !strings.Contains(string(out), "folder") || !strings.Contains(string(out), "nodir") {
		t.Fatalf("the deletion stops: %d %s", code, out)
	}
	if code, _ := e.call("deck.record_get", map[string]any{"record": ids["nodir"]}); code != 200 {
		t.Fatal("the record stays")
	}
}

// TestAProcessBelongsToARecordOnlyByItsRecordField: a process is of the record its input field record names, however
// large its id, and not of a record another field names.
func TestAProcessBelongsToARecordOnlyByItsRecordField(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	pid := e.createProject("shorts", cascadeModule)
	e.saveScript("shorts", "scripts/render.py", renderScript)
	low := e.videos(map[string]any{"slug": "low", "dir": "low"})["low"]
	if _, err := e.App.Store.DB.Exec(`UPDATE sqlite_sequence SET seq = 999999 WHERE name = 'record'`); err != nil {
		t.Fatal(err)
	}
	big := e.videos(map[string]any{"slug": "big", "dir": "big"})["big"]
	if big < 1000000 {
		t.Fatalf("a large record id: %d", big)
	}
	run := e.start("shorts", "render", map[string]any{"record": big, "take": low})
	if got := e.waitScript(run, "done", "error"); got.State != "done" {
		t.Fatalf("the render runs: %+v", got)
	}
	runs := func(record int64) []int64 {
		var ps []struct{ ID int64 }
		e.must("deck.processes_list", map[string]any{"project": "shorts", "record": record}, &ps)
		var out []int64
		for _, p := range ps {
			out = append(out, p.ID)
		}
		return out
	}
	if got := runs(low); len(got) != 0 {
		t.Fatalf("a record another field names has no run: %v", got)
	}
	if got := runs(big); len(got) != 1 || got[0] != run {
		t.Fatalf("a large record finds its run: %v", got)
	}
	if got := e.partNames(low); strings.Contains(got, "file") {
		t.Fatalf("a record another field of a run names takes none of its files: %s", got)
	}
	if got := e.partNames(big); !strings.Contains(got, "file frame.png") || !strings.Contains(got, "file log.txt") {
		t.Fatalf("a large record takes the files of its run: %s", got)
	}
	e.must("deck.record_delete", map[string]any{"record": low}, nil)
	var n int
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM file WHERE project_id = ?`, pid).Scan(&n)
	if n != 2 {
		t.Fatalf("the files of a run of another record stay: %d", n)
	}
	e.must("deck.record_delete", map[string]any{"record": big}, nil)
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM file WHERE project_id = ?`, pid).Scan(&n)
	if n != 0 {
		t.Fatalf("the files of a large record's run go with it: %d", n)
	}
}

// TestDeletingARecordStopsItsOpenProcesses: a process still open for a record is listed and stopped with it.
func TestDeletingARecordStopsItsOpenProcesses(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		<-ctx.Done()
		return provider.Result{}
	})
	e.createProject("shorts", cascadeModule)
	ids := e.videos(map[string]any{"slug": "knights", "dir": "knights"}, map[string]any{"slug": "snails", "dir": "snails"})
	watch := e.start("shorts", "watch", map[string]any{"record": ids["knights"]})
	other := e.start("shorts", "watch", map[string]any{"record": ids["snails"]})
	e.waitState(watch, "running")
	e.waitState(other, "running")
	if got := e.partNames(ids["knights"]); !strings.Contains(got, "process watch") {
		t.Fatalf("the owner sees the open process the deletion stops: %s", got)
	}
	e.must("deck.record_delete", map[string]any{"record": ids["knights"]}, nil)
	if got := e.waitState(watch, "stopped", "done", "error"); got.State != "stopped" {
		t.Fatalf("the process of the record is stopped: %+v", got)
	}
	if got := e.process(other); got.State != "running" {
		t.Fatalf("the process of another record goes on: %+v", got)
	}
	e.must("deck.process_stop", map[string]any{"process": other}, nil)
}

// TestTheMasterAsksTheOwnerBeforeDeletingWhatAModuleMade: the master's own record with a file and folder of the
// module waits for the owner, who sees what goes; one with a child record of the owner is refused.
func TestTheMasterAsksTheOwnerBeforeDeletingWhatAModuleMade(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	said := map[string]string{}
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		_, text, found := strings.Cut(c.Req.Prompt, "new message in chat topic")
		if !found {
			return provider.Result{Text: "Noted."}
		}
		text = strings.Split(text, "\n")[1]
		slug, _ := strings.CutPrefix(text, "write ")
		if slug != text {
			out, _ := c.Tool(ctx, "records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(map[string]any{"slug": slug, "dir": slug})})
			mu.Lock()
			said["write "+slug] = out
			mu.Unlock()
			return provider.Result{Text: "Written."}
		}
		var id int64
		if _, err := fmt.Sscanf(text, "delete %d", &id); err == nil {
			out, _ := c.Tool(ctx, "record_delete", map[string]any{"record": id})
			mu.Lock()
			said[text] = out
			mu.Unlock()
		}
		return provider.Result{Text: "Done."}
	})
	pid := e.createProject("shorts", cascadeModule)
	e.saveScript("shorts", "scripts/render.py", renderScript)
	topic := e.say(0, "write knights")
	e.masterSaid(topic, 1)
	e.say(topic, "write snails")
	e.masterSaid(topic, 2)
	ids := e.videos()
	if got := e.waitScript(e.start("shorts", "render", map[string]any{"record": ids["knights"]}), "done", "error"); got.State != "done" {
		t.Fatalf("the render runs: %+v", got)
	}
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "scenes", "records": jsonl(map[string]any{"video": "snails", "n": 1})}, nil)

	e.say(topic, fmt.Sprintf("delete %d", ids["snails"]))
	e.masterSaid(topic, 3)
	mu.Lock()
	refused := said[fmt.Sprintf("delete %d", ids["snails"])]
	mu.Unlock()
	if !strings.Contains(refused, "only those its own processes wrote") {
		t.Fatalf("the master does not delete a record with a child record of the owner: %s", refused)
	}

	e.say(topic, fmt.Sprintf("delete %d", ids["knights"]))
	e.masterSaid(topic, 4)
	mu.Lock()
	waiting := said[fmt.Sprintf("delete %d", ids["knights"])]
	mu.Unlock()
	if !strings.Contains(waiting, `"waiting"`) {
		t.Fatalf("the master's deletion of what the module made waits for the owner: %s", waiting)
	}
	if code, _ := e.call("deck.record_get", map[string]any{"record": ids["knights"]}); code != 200 {
		t.Fatal("nothing goes before the owner says yes")
	}
	a := e.waitAsk()
	for _, want := range []string{"frame.png", "log.txt", "videos/knights/", "Delete record"} {
		if !strings.Contains(a.Text+fmt.Sprint(e.askWhat(a.ID)), want) {
			t.Fatalf("the owner sees all the deletion takes (%s): %+v", want, a)
		}
	}
	e.answer(a.ID, "allow")
	if code, _ := e.call("deck.record_get", map[string]any{"record": ids["knights"]}); code != 404 {
		t.Fatal("the owner's yes deletes the record")
	}
	var n int
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM file WHERE project_id = ?`, pid).Scan(&n)
	if n != 0 {
		t.Fatalf("with its files: %d", n)
	}

	e.say(topic, "write rats")
	var rats int64
	e.waitFor("the master's record", func() bool { rats = e.videos()["rats"]; return rats != 0 })
	e.say(topic, fmt.Sprintf("delete %d", rats))
	e.waitFor("the ask of the deletion", func() bool { a = e.waitAsk(); return strings.Contains(a.Text, "rats") })
	e.must("deck.record_delete", map[string]any{"record": rats}, nil)
	e.answer(a.ID, "allow")
	var left []askView
	e.must("deck.asks_list", nil, &left)
	if len(left) != 0 {
		t.Fatalf("a yes to a record already gone closes the ask: %+v", left)
	}
}

// TestTheMasterDeletesARecordWithTheFilesItsOwnRunMade: a file a script the master started gave a record is the
// master's, so it deletes the record alone; one the owner's run gave waits for the owner.
func TestTheMasterDeletesARecordWithTheFilesItsOwnRunMade(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	said := map[string]string{}
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		_, text, found := strings.Cut(c.Req.Prompt, "new message in chat topic")
		if !found {
			return provider.Result{Text: "Noted."}
		}
		text = strings.Split(text, "\n")[1]
		var id int64
		var out string
		if slug, ok := strings.CutPrefix(text, "write "); ok {
			out, _ = c.Tool(ctx, "records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(map[string]any{"slug": slug})})
		} else if _, err := fmt.Sscanf(text, "render %d", &id); err == nil {
			out, _ = c.Tool(ctx, "shorts__render", map[string]any{"record": id})
		} else if _, err := fmt.Sscanf(text, "delete %d", &id); err == nil {
			out, _ = c.Tool(ctx, "record_delete", map[string]any{"record": id})
		}
		mu.Lock()
		said[text] = out
		mu.Unlock()
		return provider.Result{Text: "Done."}
	})
	pid := e.createProject("shorts", strings.Replace(cascadeModule, `"folder": "'videos/' + dir",`, "", 1))
	e.saveScript("shorts", "scripts/render.py", renderScript)
	topic := e.say(0, "write knights")
	e.masterSaid(topic, 1)
	e.say(topic, "write snails")
	e.masterSaid(topic, 2)
	ids := e.videos()
	e.say(topic, fmt.Sprintf("render %d", ids["knights"]))
	e.masterSaid(topic, 3)
	if got := e.waitScript(e.start("shorts", "render", map[string]any{"record": ids["snails"]}), "done", "error"); got.State != "done" {
		t.Fatalf("the owner's render runs: %+v", got)
	}
	var n int
	e.waitFor("the frames of both", func() bool {
		_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM file WHERE project_id = ? AND owner_kind = 'record'`, pid).Scan(&n)
		return n == 2
	})

	e.say(topic, fmt.Sprintf("delete %d", ids["knights"]))
	e.masterSaid(topic, 4)
	mu.Lock()
	mine := said[fmt.Sprintf("delete %d", ids["knights"])]
	mu.Unlock()
	if !strings.Contains(mine, `"ok":true`) {
		t.Fatalf("the master deletes its record with the frame its own run made: %s", mine)
	}
	e.say(topic, fmt.Sprintf("delete %d", ids["snails"]))
	e.masterSaid(topic, 5)
	mu.Lock()
	theirs := said[fmt.Sprintf("delete %d", ids["snails"])]
	mu.Unlock()
	if !strings.Contains(theirs, `"waiting"`) || !strings.Contains(theirs, "the file frame.png") {
		t.Fatalf("a frame the owner's run made waits for the owner, and the master reads why: %s", theirs)
	}
}

// askWhat is what the owner reads an ask is about.
func (e *Env) askWhat(id int64) string {
	e.T.Helper()
	var as []struct {
		ID   int64
		What string
	}
	e.must("deck.asks_list", nil, &as)
	for _, a := range as {
		if a.ID == id {
			return a.What
		}
	}
	return ""
}

// TestRetentionAndCollectionDeleteTakeWhatBelongsToARecord: a record that expires, and every record of a deleted
// collection, go with their child records and folders.
func TestRetentionAndCollectionDeleteTakeWhatBelongsToARecord(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, nil)
	pid := e.createProject("shorts", strings.Replace(cascadeModule, `"folder": "'videos/' + dir",`, `"folder": "'videos/' + dir", "retention_days": 30,`, 1))
	data := deckhome.Layout(e.App.Cfg.Home).Data(pid)
	count := func(collection string) int {
		var n int
		_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM record WHERE project_id = ? AND collection = ?`, pid, collection).Scan(&n)
		return n
	}
	e.videos(map[string]any{"slug": "old", "dir": "old"})
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "scenes", "records": jsonl(map[string]any{"video": "old", "n": 1})}, nil)
	if err := os.MkdirAll(filepath.Join(data, "videos", "old"), 0o700); err != nil {
		t.Fatal(err)
	}
	e.Clock.Add(31 * 24 * time.Hour)
	e.videos(map[string]any{"slug": "new", "dir": "new"})
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "scenes", "records": jsonl(map[string]any{"video": "new", "n": 1})}, nil)
	if err := os.MkdirAll(filepath.Join(data, "videos", "new"), 0o700); err != nil {
		t.Fatal(err)
	}
	e.sweep()
	if count("videos") != 1 || count("scenes") != 1 || exists(filepath.Join(data, "videos", "old")) || !exists(filepath.Join(data, "videos", "new")) {
		t.Fatalf("an expired record goes with its scene and folder: %d videos, %d scenes", count("videos"), count("scenes"))
	}
	e.must("deck.collection_delete", map[string]any{"project": "shorts", "collection": "videos"}, nil)
	if count("videos") != 0 || count("scenes") != 0 || exists(filepath.Join(data, "videos", "new")) {
		t.Fatalf("a deleted collection takes the scenes and folders of its records: %d videos, %d scenes", count("videos"), count("scenes"))
	}
}

// TestAFolderThatWillNotGoIsANoteOfTheFeed: a folder of a deleted record deck could not remove is told in the feed.
func TestAFolderThatWillNotGoIsANoteOfTheFeed(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	pid := e.createProject("shorts", cascadeModule)
	data := deckhome.Layout(e.App.Cfg.Home).Data(pid)
	if err := os.MkdirAll(filepath.Join(data, "videos", "knights", "deep"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(data, "videos", "knights"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(data, "videos", "knights"), 0o700) })
	ids := e.videos(map[string]any{"slug": "knights", "dir": "knights"})
	e.must("deck.record_delete", map[string]any{"record": ids["knights"]}, nil)
	e.waitNote("videos/knights")
}

// TestTheFolderOfADeletedProjectGoesEvenAfterACrash: a project whose row went while deck did not remove its folder
// loses the folder on the next round of deck.
func TestTheFolderOfADeletedProjectGoesEvenAfterACrash(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	pid := e.createProject("shorts", cascadeModule)
	dir := deckhome.Layout(e.App.Cfg.Home).Project(pid)
	if err := os.MkdirAll(filepath.Join(dir, "data", "videos"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := e.App.Store.DB.Exec(`DELETE FROM project WHERE id = ?`, pid); err != nil {
		t.Fatal(err)
	}
	e.App.Sweep(context.Background(), time.Now())
	if exists(dir) {
		t.Fatal("the folder of a deleted project goes")
	}
}

// TestARecordDeckCannotPlanKeepsRetentionRedInHealth: a folder deck cannot work out stops retention of the project,
// which is red in its health once, and the files past their retention still go.
func TestARecordDeckCannotPlanKeepsRetentionRedInHealth(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, nil)
	e.createProject("shorts", strings.Replace(cascadeModule, `"folder": "'videos/' + dir",`, `"folder": "'videos/' + dir", "retention_days": 30, "file_retention_days": 10,`, 1))
	ids := e.videos(map[string]any{"slug": "old", "dir": "old"}, map[string]any{"slug": "nodir"})
	e.must("deck.file_upload", map[string]any{"project": "shorts", "name": "a.txt", "content": "YQ=="}, nil)
	if _, err := e.App.Store.DB.Exec(`UPDATE file SET owner_kind = 'record', owner_id = ?`, ids["old"]); err != nil {
		t.Fatal(err)
	}
	e.Clock.Add(31 * 24 * time.Hour)
	e.sweep()
	var n int
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM health WHERE subject = 'retention:shorts' AND status = 'error'`).Scan(&n)
	var files int
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM file`).Scan(&files)
	if n != 1 || files != 0 {
		t.Fatalf("retention is red in health and the files past theirs go: %d rows, %d files", n, files)
	}
	if code, _ := e.call("deck.record_get", map[string]any{"record": ids["old"]}); code != 200 {
		t.Fatal("the expired record stays until deck can plan it")
	}
}

// TestRetentionOfEachProjectIsRedAndReadApart: two projects that cannot plan their expired records are each red in
// health, naming the record and why, and the owner reads each apart.
func TestRetentionOfEachProjectIsRedAndReadApart(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, nil)
	module := strings.Replace(cascadeModule, `"folder": "'videos/' + dir",`, `"folder": "'videos/' + dir", "retention_days": 30,`, 1)
	for _, name := range []string{"shorts", "clips"} {
		e.createProject(name, module)
		e.must("deck.records_upsert", map[string]any{"project": name, "collection": "videos", "records": jsonl(map[string]any{"slug": "nodir-" + name})}, nil)
	}
	e.Clock.Add(31 * 24 * time.Hour)
	e.sweep()
	why := map[string]string{}
	rows, _ := e.App.Store.DB.Query(`SELECT p.name, h.subject, h.error FROM health h JOIN project p ON p.id = h.project_id WHERE h.status = 'error'`)
	for rows.Next() {
		var p, subject, text string
		_ = rows.Scan(&p, &subject, &text)
		why[p+" "+subject] = text
	}
	_ = rows.Close()
	for _, name := range []string{"shorts", "clips"} {
		if text := why[name+" retention:"+name]; !strings.Contains(text, "nodir-"+name) || !strings.Contains(text, "videos") || !strings.Contains(text, "folder") {
			t.Fatalf("retention of %s is red, naming the record and why: %v", name, why)
		}
	}
	e.must("deck.health_seen", map[string]any{"subject": "retention:clips", "project": "clips"}, nil)
	if seen, _ := e.healthSeen("clips", "retention:clips"); !seen {
		t.Fatal("the owner reads the retention of one project")
	}
	if seen, failing := e.healthSeen("shorts", "retention:shorts"); seen || !failing {
		t.Fatal("the retention of another project stays unread")
	}
	if code, _ := e.call("deck.health_seen", map[string]any{"subject": "retention:shorts", "project": "clips"}); code == 200 {
		t.Fatal("a check is read only in its own project")
	}
}

// TestRetentionLeavesTheFrozenCallsOfTheProcessesItStops: a process of an expired record stops, and the call it
// froze stays the owner's to allow.
func TestRetentionLeavesTheFrozenCallsOfTheProcessesItStops(t *testing.T) {
	t.Parallel()
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{"id": "ext-1"}`) })
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		c.Tool(ctx, "shorts__post", map[string]any{"id": "a", "text": "hi"})
		<-ctx.Done()
		return provider.Result{}
	}, withTTL)
	e.must("deck.secret_set", map[string]any{"name": "svc", "value": "k"}, nil)
	module := strings.Replace(cascadeModule, `"folder": "'videos/' + dir",`, `"folder": "'videos/' + dir", "retention_days": 30,`, 1)
	post := postManifest(svc.URL, "")
	module = strings.Replace(module, `"scripts": [`, post[strings.Index(post, `"tools"`):len(post)-1]+`, "scripts": [`, 1)
	pid := e.createProject("shorts", module)
	id := e.videos(map[string]any{"slug": "old", "dir": "old"})["old"]
	watch := e.start("shorts", "watch", map[string]any{"record": id})
	a := e.waitAsk()
	if _, err := e.App.Store.DB.Exec(`UPDATE record SET created_at = '2000-01-01T00:00:00.000Z' WHERE project_id = ?`, pid); err != nil {
		t.Fatal(err)
	}
	e.App.Sweep(context.Background(), time.Now())
	if got := e.waitState(watch, "stopped", "done", "error"); got.State != "stopped" {
		t.Fatalf("the process of the expired record is stopped: %+v", got)
	}
	e.settle()
	if code, out := e.call("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}); code != 200 || len(svc.requests()) != 1 {
		t.Fatalf("the owner may still allow the call the process froze: %d %s", code, out)
	}
}
