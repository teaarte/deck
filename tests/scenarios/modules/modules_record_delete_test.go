package modules

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/tests/harness"
)

// cascadeModule keeps videos, each with a folder of the data folder its field dir names, the scenes of each video,
// a script that renders by a record and an agent that watches one.
var cascadeModule = harness.Manifest(`{"collections": [
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
  "scripts": [{"name": "render", "file": "scripts/render.py", "runtime": "python"}]}`)

// renderScript writes a frame of the record it renders and a log of its own run.
const renderScript = `import json, os, sys
inp = json.load(sys.stdin)
res = os.environ["DECK_RESULTS"]
open(os.path.join(res, "frame.png"), "w").write("png")
open(os.path.join(res, "log.txt"), "w").write("log")
print(json.dumps({"files": [{"path": "frame.png", "record": inp["record"]}, {"path": "log.txt"}]}))
`

// videos writes videos to the cascade module and gives the id of each by its slug.
func videos(e *harness.Env, recs ...map[string]any) map[string]int64 {
	e.T.Helper()
	e.Must("deck.records_upsert", map[string]any{"project": "films", "collection": "videos", "records": harness.Jsonl(recs...)}, nil)
	var all []struct {
		ID  int64
		Key string
	}
	e.Must("deck.records_list", map[string]any{"project": "films", "collection": "videos"}, &all)
	ids := map[string]int64{}
	for _, v := range all {
		ids[v.Key] = v.ID
	}
	return ids
}

// partNames are what record_parts lists for a record, as kind and name.
func partNames(e *harness.Env, record int64) string {
	e.T.Helper()
	var parts []struct{ Kind, Name string }
	e.Must("deck.record_parts", map[string]any{"record": record}, &parts)
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
	e := harness.New(t)
	pid := e.Project("films", cascadeModule)
	data := deckhome.Layout(e.App.Cfg.Home).Data(pid)
	for _, d := range []string{"keep", "shared", "nest/inner"} {
		if err := os.MkdirAll(filepath.Join(data, "videos", d), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(data, "videos", d, "f.txt"), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	ids := videos(e, map[string]any{"slug": "empty", "dir": ""}, map[string]any{"slug": "up", "dir": "x/../.."}, map[string]any{"slug": "dot", "dir": "."},
		map[string]any{"slug": "c", "dir": "shared"}, map[string]any{"slug": "d", "dir": "shared"},
		map[string]any{"slug": "outer", "dir": "nest"}, map[string]any{"slug": "inner", "dir": "nest/inner"})
	for _, k := range []string{"empty", "up", "dot", "c", "outer"} {
		if got := partNames(e, ids[k]); strings.Contains(got, "folder") {
			t.Fatalf("%s takes no folder: %s", k, got)
		}
		e.Must("deck.record_delete", map[string]any{"record": ids[k]}, nil)
	}
	if !exists(filepath.Join(data, "videos", "keep", "f.txt")) || !exists(filepath.Join(data, "videos", "shared", "f.txt")) ||
		!exists(filepath.Join(data, "videos", "nest", "inner", "f.txt")) {
		t.Fatal("no folder another record holds, nor the folder of the collection or the data folder, goes")
	}
	// A folder in the outbox a crash left before deck got to it.
	e.SQL(`INSERT INTO outbox (kind, ref, path) VALUES ('folder', ?, 'videos/shared')`, pid)
	e.App.Sweep(context.Background(), time.Now())
	if !exists(filepath.Join(data, "videos", "shared", "f.txt")) {
		t.Fatal("the outbox leaves a folder a record holds by the time it gets to it")
	}
	if got := partNames(e, ids["d"]); !strings.Contains(got, "folder videos/shared/") {
		t.Fatalf("the last record that holds a folder takes it: %s", got)
	}
	e.Must("deck.record_delete", map[string]any{"record": ids["d"]}, nil)
	e.Must("deck.record_delete", map[string]any{"record": ids["inner"]}, nil)
	e.Drained()
	if exists(filepath.Join(data, "videos", "shared")) || exists(filepath.Join(data, "videos", "nest", "inner")) || !exists(filepath.Join(data, "videos", "keep")) {
		t.Fatal("a folder no other record holds goes with its record")
	}
}

// TestAnExpressionErrorStopsADeletion: a folder deck cannot work out stops record_parts and the deletion, which
// takes nothing.
func TestAnExpressionErrorStopsADeletion(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("films", cascadeModule)
	ids := videos(e, map[string]any{"slug": "nodir"})
	if code, out := e.Call("deck.record_parts", map[string]any{"record": ids["nodir"]}); code != 422 || !strings.Contains(string(out), "folder") {
		t.Fatalf("record_parts says the folder could not be worked out: %d %s", code, out)
	}
	if code, out := e.Call("deck.record_delete", map[string]any{"record": ids["nodir"]}); code != 422 || !strings.Contains(string(out), "folder") || !strings.Contains(string(out), "nodir") {
		t.Fatalf("the deletion stops: %d %s", code, out)
	}
	if code, _ := e.Call("deck.record_get", map[string]any{"record": ids["nodir"]}); code != 200 {
		t.Fatal("the record stays")
	}
}

// TestAProcessBelongsToARecordOnlyByItsRecordField: a process is of the record its input field record names, however
// large its id, and not of a record another field names.
func TestAProcessBelongsToARecordOnlyByItsRecordField(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	pid := e.Project("films", cascadeModule)
	e.SaveScript("films", "scripts/render.py", renderScript)
	low := videos(e, map[string]any{"slug": "low", "dir": "low"})["low"]
	// Record ids past a million, as a deck of long use has.
	e.SQL(`UPDATE sqlite_sequence SET seq = 999999 WHERE name = 'record'`)
	big := videos(e, map[string]any{"slug": "big", "dir": "big"})["big"]
	if big < 1000000 {
		t.Fatalf("a large record id: %d", big)
	}
	run := e.Start("films", "render", map[string]any{"record": big, "take": low})
	if got := e.WaitState(run, "done", "error"); got.State != "done" {
		t.Fatalf("the render runs: %+v", got)
	}
	runs := func(record int64) []int64 {
		var ps []struct{ ID int64 }
		e.Must("deck.processes_list", map[string]any{"project": "films", "record": record}, &ps)
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
	if got := partNames(e, low); strings.Contains(got, "file") {
		t.Fatalf("a record another field of a run names takes none of its files: %s", got)
	}
	if got := partNames(e, big); !strings.Contains(got, "file frame.png") || !strings.Contains(got, "file log.txt") {
		t.Fatalf("a large record takes the files of its run: %s", got)
	}
	e.Must("deck.record_delete", map[string]any{"record": low}, nil)
	var n int
	// Every file of the project, the trash too, which no list shows.
	_ = e.SQLRow(`SELECT count(*) FROM file WHERE project_id = ?`, pid).Scan(&n)
	if n != 2 {
		t.Fatalf("the files of a run of another record stay: %d", n)
	}
	e.Must("deck.record_delete", map[string]any{"record": big}, nil)
	// Every file of the project, the trash too, which no list shows.
	_ = e.SQLRow(`SELECT count(*) FROM file WHERE project_id = ?`, pid).Scan(&n)
	if n != 0 {
		t.Fatalf("the files of a large record's run go with it: %d", n)
	}
}

// TestDeletingARecordStopsItsOpenProcesses: a process still open for a record is listed and stopped with it.
func TestDeletingARecordStopsItsOpenProcesses(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		<-ctx.Done()
		return provider.Result{}
	}))

	e.Project("films", cascadeModule)
	ids := videos(e, map[string]any{"slug": "knights", "dir": "knights"}, map[string]any{"slug": "snails", "dir": "snails"})
	watch := e.Start("films", "watch", map[string]any{"record": ids["knights"]})
	other := e.Start("films", "watch", map[string]any{"record": ids["snails"]})
	e.WaitState(watch, "running")
	e.WaitState(other, "running")
	if got := partNames(e, ids["knights"]); !strings.Contains(got, "process watch") {
		t.Fatalf("the owner sees the open process the deletion stops: %s", got)
	}
	e.Must("deck.record_delete", map[string]any{"record": ids["knights"]}, nil)
	if got := e.WaitState(watch, "stopped", "done", "error"); got.State != "stopped" {
		t.Fatalf("the process of the record is stopped: %+v", got)
	}
	if got := e.Proc(other); got.State != "running" {
		t.Fatalf("the process of another record goes on: %+v", got)
	}
	e.Must("deck.process_stop", map[string]any{"process": other}, nil)
}

// TestTheMasterAsksTheOwnerBeforeDeletingWhatAModuleMade: the master's own record with a file and folder of the
// module waits for the owner, who sees what goes; one with a child record of the owner is refused.
func TestTheMasterAsksTheOwnerBeforeDeletingWhatAModuleMade(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	said := map[string]string{}
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		_, text, found := strings.Cut(c.Req.Prompt, "new message in chat topic")
		if !found {
			return provider.Result{Text: "Noted."}
		}
		text = strings.Split(text, "\n")[1]
		slug, _ := strings.CutPrefix(text, "write ")
		if slug != text {
			out, _ := c.Tool(ctx, "records_upsert", map[string]any{"project": "films", "collection": "videos", "records": harness.Jsonl(map[string]any{"slug": slug, "dir": slug})})
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
	}))

	pid := e.Project("films", cascadeModule)
	e.SaveScript("films", "scripts/render.py", renderScript)
	topic := e.Say(0, "write knights")
	e.MasterSaid(topic, 1)
	e.Say(topic, "write snails")
	e.MasterSaid(topic, 2)
	ids := videos(e)
	if got := e.WaitState(e.Start("films", "render", map[string]any{"record": ids["knights"]}), "done", "error"); got.State != "done" {
		t.Fatalf("the render runs: %+v", got)
	}
	e.Must("deck.records_upsert", map[string]any{"project": "films", "collection": "scenes", "records": harness.Jsonl(map[string]any{"video": "snails", "n": 1})}, nil)

	e.Say(topic, fmt.Sprintf("delete %d", ids["snails"]))
	e.MasterSaid(topic, 3)
	mu.Lock()
	refused := said[fmt.Sprintf("delete %d", ids["snails"])]
	mu.Unlock()
	if !strings.Contains(refused, "only those its own processes wrote") {
		t.Fatalf("the master does not delete a record with a child record of the owner: %s", refused)
	}

	e.Say(topic, fmt.Sprintf("delete %d", ids["knights"]))
	e.MasterSaid(topic, 4)
	mu.Lock()
	waiting := said[fmt.Sprintf("delete %d", ids["knights"])]
	mu.Unlock()
	if !strings.Contains(waiting, `"waiting"`) {
		t.Fatalf("the master's deletion of what the module made waits for the owner: %s", waiting)
	}
	if code, _ := e.Call("deck.record_get", map[string]any{"record": ids["knights"]}); code != 200 {
		t.Fatal("nothing goes before the owner says yes")
	}
	a := e.WaitAsk()
	for _, want := range []string{"frame.png", "log.txt", "videos/knights/", "Delete record"} {
		if !strings.Contains(a.Text+fmt.Sprint(askWhat(e, a.ID)), want) {
			t.Fatalf("the owner sees all the deletion takes (%s): %+v", want, a)
		}
	}
	e.Answer(a.ID, "allow")
	if code, _ := e.Call("deck.record_get", map[string]any{"record": ids["knights"]}); code != 404 {
		t.Fatal("the owner's yes deletes the record")
	}
	var n int
	// Every file of the project, the trash too, which no list shows.
	_ = e.SQLRow(`SELECT count(*) FROM file WHERE project_id = ?`, pid).Scan(&n)
	if n != 0 {
		t.Fatalf("with its files: %d", n)
	}

	e.Say(topic, "write rats")
	var rats int64
	e.Until("the master's record", func() bool { rats = videos(e)["rats"]; return rats != 0 })
	e.Say(topic, fmt.Sprintf("delete %d", rats))
	e.Until("the ask of the deletion", func() bool { a = e.WaitAsk(); return strings.Contains(a.Text, "rats") })
	e.Must("deck.record_delete", map[string]any{"record": rats}, nil)
	e.Answer(a.ID, "allow")
	var left []harness.Ask
	e.Must("deck.asks_list", nil, &left)
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
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		_, text, found := strings.Cut(c.Req.Prompt, "new message in chat topic")
		if !found {
			return provider.Result{Text: "Noted."}
		}
		text = strings.Split(text, "\n")[1]
		var id int64
		var out string
		if slug, ok := strings.CutPrefix(text, "write "); ok {
			out, _ = c.Tool(ctx, "records_upsert", map[string]any{"project": "films", "collection": "videos", "records": harness.Jsonl(map[string]any{"slug": slug})})
		} else if _, err := fmt.Sscanf(text, "render %d", &id); err == nil {
			out, _ = c.Tool(ctx, "films__render", map[string]any{"record": id})
		} else if _, err := fmt.Sscanf(text, "delete %d", &id); err == nil {
			out, _ = c.Tool(ctx, "record_delete", map[string]any{"record": id})
		}
		mu.Lock()
		said[text] = out
		mu.Unlock()
		return provider.Result{Text: "Done."}
	}))

	e.Project("films", strings.Replace(cascadeModule, `"folder": "'videos/' + dir",`, "", 1))
	e.SaveScript("films", "scripts/render.py", renderScript)
	topic := e.Say(0, "write knights")
	e.MasterSaid(topic, 1)
	e.Say(topic, "write snails")
	e.MasterSaid(topic, 2)
	ids := videos(e)
	e.Say(topic, fmt.Sprintf("render %d", ids["knights"]))
	e.MasterSaid(topic, 3)
	if got := e.WaitState(e.Start("films", "render", map[string]any{"record": ids["snails"]}), "done", "error"); got.State != "done" {
		t.Fatalf("the owner's render runs: %+v", got)
	}
	e.Until("the frames of both", func() bool {
		var fs, more []struct{ ID int64 }
		e.Must("deck.files_list", map[string]any{"record": ids["knights"]}, &fs)
		e.Must("deck.files_list", map[string]any{"record": ids["snails"]}, &more)
		return len(fs)+len(more) == 2
	})

	e.Say(topic, fmt.Sprintf("delete %d", ids["knights"]))
	e.MasterSaid(topic, 4)
	mu.Lock()
	mine := said[fmt.Sprintf("delete %d", ids["knights"])]
	mu.Unlock()
	if !strings.Contains(mine, `"ok":true`) {
		t.Fatalf("the master deletes its record with the frame its own run made: %s", mine)
	}
	e.Say(topic, fmt.Sprintf("delete %d", ids["snails"]))
	e.MasterSaid(topic, 5)
	mu.Lock()
	theirs := said[fmt.Sprintf("delete %d", ids["snails"])]
	mu.Unlock()
	if !strings.Contains(theirs, `"waiting"`) || !strings.Contains(theirs, "the file frame.png") {
		t.Fatalf("a frame the owner's run made waits for the owner, and the master reads why: %s", theirs)
	}
}

// askWhat is what the owner reads an ask is about.
func askWhat(e *harness.Env, id int64) string {
	e.T.Helper()
	var as []struct {
		ID   int64
		What string
	}
	e.Must("deck.asks_list", nil, &as)
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
	e := harness.New(t, harness.WithClock())
	pid := e.Project("films", strings.Replace(cascadeModule, `"folder": "'videos/' + dir",`, `"folder": "'videos/' + dir", "retention_days": 30,`, 1))
	data := deckhome.Layout(e.App.Cfg.Home).Data(pid)
	count := func(collection string) int {
		var rs []struct{ ID int64 }
		e.Must("deck.records_list", map[string]any{"project": "films", "collection": collection}, &rs)
		return len(rs)
	}
	videos(e, map[string]any{"slug": "old", "dir": "old"})
	e.Must("deck.records_upsert", map[string]any{"project": "films", "collection": "scenes", "records": harness.Jsonl(map[string]any{"video": "old", "n": 1})}, nil)
	if err := os.MkdirAll(filepath.Join(data, "videos", "old"), 0o700); err != nil {
		t.Fatal(err)
	}
	e.Clock.Add(31 * 24 * time.Hour)
	videos(e, map[string]any{"slug": "new", "dir": "new"})
	e.Must("deck.records_upsert", map[string]any{"project": "films", "collection": "scenes", "records": harness.Jsonl(map[string]any{"video": "new", "n": 1})}, nil)
	if err := os.MkdirAll(filepath.Join(data, "videos", "new"), 0o700); err != nil {
		t.Fatal(err)
	}
	e.Sweep()
	e.Drained()
	if count("videos") != 1 || count("scenes") != 1 || exists(filepath.Join(data, "videos", "old")) || !exists(filepath.Join(data, "videos", "new")) {
		t.Fatalf("an expired record goes with its scene and folder: %d videos, %d scenes", count("videos"), count("scenes"))
	}
	e.Must("deck.collection_delete", map[string]any{"project": "films", "collection": "videos"}, nil)
	e.Drained()
	if count("videos") != 0 || count("scenes") != 0 || exists(filepath.Join(data, "videos", "new")) {
		t.Fatalf("a deleted collection takes the scenes and folders of its records: %d videos, %d scenes", count("videos"), count("scenes"))
	}
}

// TestAFolderThatWillNotGoIsTheHealthOfTheOutbox: a folder of a deleted record deck could not remove in three tries is
// red in the health of the outbox, which turns green once it goes.
func TestAFolderThatWillNotGoIsTheHealthOfTheOutbox(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	pid := e.Project("films", cascadeModule)
	data := deckhome.Layout(e.App.Cfg.Home).Data(pid)
	if err := os.MkdirAll(filepath.Join(data, "videos", "knights", "deep"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(data, "videos", "knights"), 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(data, "videos", "knights"), 0o700) })
	ids := videos(e, map[string]any{"slug": "knights", "dir": "knights"})
	e.Must("deck.record_delete", map[string]any{"record": ids["knights"]}, nil)
	e.Drained()
	e.App.Sweep(context.Background(), time.Now())
	e.App.Sweep(context.Background(), time.Now())
	if h := e.HealthRowOf("outbox"); h == nil || h["status"] != "error" || !strings.Contains(fmt.Sprint(h["error"]), "videos/knights") {
		t.Fatalf("the folder that will not go is red in the health of the outbox: %v", h)
	}
	_ = os.Chmod(filepath.Join(data, "videos", "knights"), 0o700)
	e.App.Sweep(context.Background(), time.Now())
	if h := e.HealthRowOf("outbox"); h != nil {
		t.Fatalf("once it goes the outbox is well: %v", h)
	}
}

// TestTheFolderOfADeletedProjectGoesEvenAfterACrash: a project whose row went while deck did not remove its folder
// loses the folder on the next round of deck.
func TestTheFolderOfADeletedProjectGoesEvenAfterACrash(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	pid := e.Project("films", cascadeModule)
	dir := deckhome.Layout(e.App.Cfg.Home).Project(pid)
	if err := os.MkdirAll(filepath.Join(dir, "data", "videos"), 0o700); err != nil {
		t.Fatal(err)
	}
	// A project whose row went in a crash before deck removed its folder.
	e.SQL(`DELETE FROM project WHERE id = ?`, pid)
	e.App.Sweep(context.Background(), time.Now())
	if exists(dir) {
		t.Fatal("the folder of a deleted project goes")
	}
}

// TestARecordDeckCannotPlanKeepsRetentionRedInHealth: a folder deck cannot work out stops retention of the project,
// which is red in its health once.
func TestARecordDeckCannotPlanKeepsRetentionRedInHealth(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	e.Project("films", strings.Replace(cascadeModule, `"folder": "'videos/' + dir",`, `"folder": "'videos/' + dir", "retention_days": 30,`, 1))
	ids := videos(e, map[string]any{"slug": "old", "dir": "old"}, map[string]any{"slug": "nodir"})
	e.Must("deck.file_upload", map[string]any{"project": "films", "name": "a.txt", "content": "YQ=="}, nil)
	// A file of the record, as a run of it leaves one.
	e.SQL(`UPDATE file SET owner_kind = 'record', owner_id = ?`, ids["old"])
	e.Clock.Add(31 * 24 * time.Hour)
	e.Sweep()
	n := 0
	for _, h := range e.Health("films") {
		if h.Subject == "retention:films" && h.Status == "error" {
			n++
		}
	}
	var fs []struct{ ID int64 }
	e.Must("deck.files_list", nil, &fs)
	if files := len(fs); n != 1 || files != 1 {
		t.Fatalf("retention is red in health and the record keeps its file: %d rows, %d files", n, files)
	}
	if code, _ := e.Call("deck.record_get", map[string]any{"record": ids["old"]}); code != 200 {
		t.Fatal("the expired record stays until deck can plan it")
	}
}

// TestRetentionOfEachProjectIsRedAndReadApart: two projects that cannot plan their expired records are each red in
// health, naming the record and why, and the owner reads each apart.
func TestRetentionOfEachProjectIsRedAndReadApart(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	module := strings.Replace(cascadeModule, `"folder": "'videos/' + dir",`, `"folder": "'videos/' + dir", "retention_days": 30,`, 1)
	for _, name := range []string{"films", "clips"} {
		e.Project(name, module)
		e.Must("deck.records_upsert", map[string]any{"project": name, "collection": "videos", "records": harness.Jsonl(map[string]any{"slug": "nodir-" + name})}, nil)
	}
	e.Clock.Add(31 * 24 * time.Hour)
	e.Sweep()
	why := map[string]string{}
	var hs []struct{ Project, Subject, Status, Error string }
	e.Must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if h.Status == "error" && h.Project != "" {
			why[h.Project+" "+h.Subject] = h.Error
		}
	}
	for _, name := range []string{"films", "clips"} {
		if text := why[name+" retention:"+name]; !strings.Contains(text, "nodir-"+name) || !strings.Contains(text, "videos") || !strings.Contains(text, "folder") {
			t.Fatalf("retention of %s is red, naming the record and why: %v", name, why)
		}
	}
	e.Must("deck.health_seen", map[string]any{"subject": "retention:clips", "project": "clips"}, nil)
	if seen, _ := e.HealthSeen("clips", "retention:clips"); !seen {
		t.Fatal("the owner reads the retention of one project")
	}
	if seen, failing := e.HealthSeen("films", "retention:films"); seen || !failing {
		t.Fatal("the retention of another project stays unread")
	}
	if code, _ := e.Call("deck.health_seen", map[string]any{"subject": "retention:films", "project": "clips"}); code == 200 {
		t.Fatal("a check is read only in its own project")
	}
}

// TestRetentionLeavesTheFrozenCallsOfTheProcessesItStops: a process of an expired record stops, and the call it
// froze stays the owner's to allow.
func TestRetentionLeavesTheFrozenCallsOfTheProcessesItStops(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"id": "ext-1"}`) })
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		c.Tool(ctx, "films__post", map[string]any{"id": "a", "text": "hi"})
		<-ctx.Done()
		return provider.Result{}
	}), harness.WithConfig(

		harness.WithTTL))

	e.Secret("films", "svc", "k")
	module := strings.Replace(cascadeModule, `"folder": "'videos/' + dir",`, `"folder": "'videos/' + dir", "retention_days": 30,`, 1)
	post := harness.PostManifest(svc.URL, "")
	module = strings.Replace(module, `"scripts": [`, post[strings.Index(post, `"tools"`):len(post)-1]+`, "scripts": [`, 1)
	pid := e.Project("films", module)
	id := videos(e, map[string]any{"slug": "old", "dir": "old"})["old"]
	watch := e.Start("films", "watch", map[string]any{"record": id})
	a := e.WaitAsk()
	// Records made long ago, past their retention.
	e.SQL(`UPDATE record SET created_at = '2000-01-01T00:00:00.000Z' WHERE project_id = ?`, pid)
	e.App.Sweep(context.Background(), time.Now())
	if got := e.WaitState(watch, "stopped", "done", "error"); got.State != "stopped" {
		t.Fatalf("the process of the expired record is stopped: %+v", got)
	}
	e.Settle()
	if code, out := e.Call("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}); code != 200 || len(svc.Requests()) != 1 {
		t.Fatalf("the owner may still allow the call the process froze: %d %s", code, out)
	}
}

// filmsModule keeps videos, each with a folder of its own in the data folder, and the scenes of each video.
var filmsModule = harness.Manifest(`{"collections": [
    {"name": "videos", "description": "A video.", "key": "slug", "display": "title", "folder": "'videos/' + slug",
      "schema": {"type": "object", "properties": {"slug": {"type": "string"}, "title": {"type": "string"}}, "required": ["slug"]}},
    {"name": "scenes", "description": "A scene of a video.", "key": "video + '-' + string(n)", "parent": {"collection": "videos", "key": "video"},
      "schema": {"type": "object", "properties": {"video": {"type": "string"}, "n": {"type": "integer"}}, "required": ["video", "n"]}}],
  "commands": [
    {"name": "render", "title": "Render", "description": "Renders a video.", "class": "internal",
     "input": {"type": "object", "properties": {"record": {"type": "integer"}}, "required": ["record"]},
     "result": {"type": "object"}, "executor": {"type": "script", "script": "render"}}],
  "scripts": [{"name": "render", "file": "scripts/render.py", "runtime": "python"}]}`)

// TestDeletingARecordTakesWhatBelongsToIt: a record goes with its child records, files, folder and the files of its
// processes, which the owner sees listed first.
func TestDeletingARecordTakesWhatBelongsToIt(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	pid := e.Project("films", filmsModule)
	e.SaveScript("films", "scripts/render.py", `import json, os, sys
inp = json.load(sys.stdin)
res = os.environ["DECK_RESULTS"]
open(os.path.join(res, "frame.png"), "w").write("png")
open(os.path.join(res, "log.txt"), "w").write("log")
print(json.dumps({"files": [{"path": "frame.png", "record": inp["record"]}, {"path": "log.txt"}]}))
`)
	e.Must("deck.records_upsert", map[string]any{"project": "films", "collection": "videos", "records": harness.Jsonl(
		map[string]any{"slug": "knights", "title": "Knights"}, map[string]any{"slug": "snails", "title": "Snails"})}, nil)
	var video int64
	var all []struct {
		ID  int64
		Key string
	}
	e.Must("deck.records_list", map[string]any{"project": "films", "collection": "videos"}, &all)
	for _, v := range all {
		if v.Key == "knights" {
			video = v.ID
		}
	}
	e.Must("deck.records_upsert", map[string]any{"project": "films", "collection": "scenes", "records": harness.Jsonl(
		map[string]any{"video": "knights", "n": 1}, map[string]any{"video": "snails", "n": 1})}, nil)
	data := deckhome.Layout(e.App.Cfg.Home).Data(pid)
	if err := os.MkdirAll(filepath.Join(data, "videos", "knights"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "videos", "knights", "out.mp4"), []byte("mp4"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := e.WaitState(e.Start("films", "render", map[string]any{"record": video}), "done", "error"); got.State != "done" {
		t.Fatalf("the render runs: %+v", got)
	}
	var parts []struct{ Kind, Name string }
	e.Must("deck.record_parts", map[string]any{"record": video}, &parts)
	var names []string
	for _, p := range parts {
		names = append(names, p.Kind+" "+p.Name)
	}
	slices.Sort(names)
	if want := "file frame.png|file log.txt|folder videos/knights/|record scenes: knights-1|record videos: Knights"; strings.Join(names, "|") != want {
		t.Fatalf("the owner sees all a deletion takes: %v", names)
	}
	e.Must("deck.record_delete", map[string]any{"record": video}, nil)
	e.Drained()
	var videos, scenes []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "films", "collection": "videos"}, &videos)
	e.Must("deck.records_list", map[string]any{"project": "films", "collection": "scenes"}, &scenes)
	var files int
	// Every file of the project, the trash too, which no list shows.
	_ = e.SQLRow(`SELECT count(*) FROM file WHERE project_id = ?`, pid).Scan(&files)
	if _, err := os.Stat(filepath.Join(data, "videos", "knights")); !os.IsNotExist(err) || len(videos)+len(scenes) != 2 || files != 0 {
		t.Fatal("the record, its scene, its files, the files of its process and its folder are gone")
	}
}

// TestTheDataFolderOfAProjectShowsByItsFolders: a data folder is a file manager, the way up in its steps and folders
// first as tiles that open them, then files; the modules at the root and the files of a module are tiles too.
func TestTheDataFolderOfAProjectShowsByItsFolders(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	pid := e.Project("films", filmsModule)
	data := deckhome.Layout(e.App.Cfg.Home).Data(pid)
	if err := errors.Join(os.MkdirAll(filepath.Join(data, "cache", "zz"), 0o700), os.WriteFile(filepath.Join(data, "cache", "tiles.bin"), []byte("x"), 0o600)); err != nil {
		t.Fatal(err)
	}
	type tile struct {
		Title, Icon, URL, Error string
		Actions                 []map[string]any
	}
	block := e.ScreenBlock("", "files", "Modules", map[string]any{"params": `{"path": "films/data/cache"}`})
	tiles := harness.BlockData[[]tile](t, block["files"])
	if len(tiles) != 2 || tiles[0].Title != "zz" || tiles[0].Icon != "folder" ||
		tiles[1].Title != "tiles.bin" || tiles[1].Icon != "" || !strings.Contains(tiles[1].URL, "/api/files/data/films/cache/tiles.bin") {
		t.Fatalf("the folders come first as tiles, then the files: %+v", tiles)
	}
	if crumbs := string(block["crumbs"]); !strings.Contains(crumbs, `"path":"films/data"`) {
		t.Fatalf("the way up is in the steps of the folder: %s", crumbs)
	}
	params, _ := harness.Verb(t, tiles[0].Actions, "open")["params"].(map[string]any)
	if open := harness.Verb(t, tiles[0].Actions, "open"); open["primary"] != true || open["screen"] != "files" || params["path"] != "films/data/cache/zz" {
		t.Fatalf("a folder tile opens the folder: %+v", open)
	}
	if len(tiles[1].Actions) != 0 {
		t.Fatalf("a file has no button to open a folder: %+v", tiles[1].Actions)
	}
	own := harness.BlockData[[]tile](t, e.Screen("films", "documents", map[string]any{"params": `{"path": "data/cache"}`})["b0"]["files"])
	if len(own) != 2 || own[1].Error != "" || !strings.Contains(own[1].URL, "/api/files/data/films/cache/tiles.bin") {
		t.Fatalf("the files screen of a module shows its data folder inside it: %+v", own)
	}

	raw, _ := json.Marshal(e.ScreenBlock("", "files", "Modules", nil))
	if s := string(raw); !strings.Contains(s, `"path":"films"`) || !strings.Contains(s, `"title":"Films"`) && !strings.Contains(s, `"title":"films"`) {
		t.Fatalf("the modules are folder tiles at the root: %s", s)
	}
	e.Must("deck.file_upload", map[string]any{"project": "films", "name": "frame.png", "content": base64.StdEncoding.EncodeToString([]byte("png"))}, nil)
	raw, _ = json.Marshal(e.Screen("films", "documents", map[string]any{"params": `{"path": "files"}`}))
	if s := string(raw); !strings.Contains(s, `"name":"frame.png"`) {
		t.Fatalf("the files screen of a module shows its files: %s", s)
	}
}
