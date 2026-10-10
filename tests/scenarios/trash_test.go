package scenarios

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	deckhome "deck2/internal/home"
)

// shortsModule keeps videos, each with a folder of its own in the data folder, and the scenes of each video.
const shortsModule = `{"version": 11,
  "collections": [
    {"name": "videos", "description": "A video.", "key": "slug", "display": "title", "folder": "'videos/' + slug",
      "schema": {"type": "object", "properties": {"slug": {"type": "string"}, "title": {"type": "string"}}, "required": ["slug"]}},
    {"name": "scenes", "description": "A scene of a video.", "key": "video + '-' + string(n)", "parent": {"collection": "videos", "key": "video"},
      "schema": {"type": "object", "properties": {"video": {"type": "string"}, "n": {"type": "integer"}}, "required": ["video", "n"]}}],
  "commands": [
    {"name": "render", "title": "Render", "description": "Renders a video.", "class": "internal",
     "input": {"type": "object", "properties": {"record": {"type": "integer"}}, "required": ["record"]},
     "result": {"type": "object"}, "executor": {"type": "script", "script": "render"}}],
  "scripts": [{"name": "render", "file": "scripts/render.py", "runtime": "python"}]}`

// TestDeletingARecordTakesWhatBelongsToItIntoTheTrash: a record goes with its child records, files, folder and the
// files of its processes; the owner sees the list first, and an undo within a day brings it all back.
func TestDeletingARecordTakesWhatBelongsToItIntoTheTrash(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	pid := e.createProject("shorts", shortsModule)
	e.saveScript("shorts", "scripts/render.py", `import json, os, sys
inp = json.load(sys.stdin)
res = os.environ["DECK_RESULTS"]
open(os.path.join(res, "frame.png"), "w").write("png")
open(os.path.join(res, "log.txt"), "w").write("log")
print(json.dumps({"files": [{"path": "frame.png", "record": inp["record"]}, {"path": "log.txt"}]}))
`)
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(
		map[string]any{"slug": "knights", "title": "Knights"}, map[string]any{"slug": "snails", "title": "Snails"})}, nil)
	var video int64
	var all []struct {
		ID  int64
		Key string
	}
	e.must("deck.records_list", map[string]any{"project": "shorts", "collection": "videos"}, &all)
	for _, v := range all {
		if v.Key == "knights" {
			video = v.ID
		}
	}
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "scenes", "records": jsonl(
		map[string]any{"video": "knights", "n": 1}, map[string]any{"video": "snails", "n": 1})}, nil)
	data := deckhome.Layout(e.App.Cfg.Home).Data(pid)
	if err := os.MkdirAll(filepath.Join(data, "videos", "knights"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "videos", "knights", "out.mp4"), []byte("mp4"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := e.waitScript(e.start("shorts", "render", map[string]any{"record": video}), "done", "error"); got.State != "done" {
		t.Fatalf("the render runs: %+v", got)
	}
	var parts []struct{ Kind, Name string }
	e.must("deck.record_parts", map[string]any{"record": video}, &parts)
	var names []string
	for _, p := range parts {
		names = append(names, p.Kind+" "+p.Name)
	}
	slices.Sort(names)
	if want := "file frame.png|file log.txt|folder videos/knights/|record scenes: knights-1|record videos: Knights"; strings.Join(names, "|") != want {
		t.Fatalf("the owner sees all a deletion takes: %v", names)
	}
	e.must("deck.record_delete", map[string]any{"record": video}, nil)
	count := func(q string, args ...any) int {
		var n int
		_ = e.App.Store.DB.QueryRow(q, args...).Scan(&n)
		return n
	}
	if _, err := os.Stat(filepath.Join(data, "videos", "knights")); !os.IsNotExist(err) || count(`SELECT count(*) FROM record WHERE project_id = ?`, pid) != 2 ||
		count(`SELECT count(*) FROM file WHERE owner_kind = 'trash'`) != 2 || count(`SELECT count(*) FROM feed WHERE command = 'deck.trash_restore'`) != 1 {
		t.Fatal("the record, its scene, files and folder went to the trash, and the feed has the undo")
	}
	e.must("deck.trash_restore", map[string]any{"record": video}, nil)
	if _, err := os.Stat(filepath.Join(data, "videos", "knights", "out.mp4")); err != nil || count(`SELECT count(*) FROM record WHERE project_id = ?`, pid) != 4 ||
		count(`SELECT count(*) FROM file WHERE owner_kind = 'record' AND owner_id = ?`, video) != 1 || count(`SELECT count(*) FROM file WHERE owner_kind = 'process'`) != 1 {
		t.Fatal("the undo brings back the records with their ids, the files to their owners and the folder")
	}
	if code, _ := e.call("deck.trash_restore", map[string]any{"record": video}); code == 200 {
		t.Fatal("what came back is no longer in the trash")
	}
}

// TestAFileOfADataFolderGoesToTheTrashForADay: the owner deletes a file of a data folder from its screen with an
// undo; after a day it is gone for good.
func TestAFileOfADataFolderGoesToTheTrashForADay(t *testing.T) {
	t.Parallel()
	clock := &Clock{now: t0}
	e := newEnvWith(t, t.TempDir(), &Fake{}, withClock(clock))
	pid := e.createProject("shorts", shortsModule)
	data := deckhome.Layout(e.App.Cfg.Home).Data(pid)
	if err := errors.Join(os.MkdirAll(filepath.Join(data, "cache"), 0o700), os.WriteFile(filepath.Join(data, "cache", "tiles.bin"), []byte("x"), 0o600)); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(e.screenMessages("", "data", map[string]any{"params": `{"project": "shorts", "path": "cache"}`}))
	if s := string(raw); !strings.Contains(s, `"tiles.bin"`) || !strings.Contains(s, `"..`) || !strings.Contains(s, `/api/files/data/shorts/cache/tiles.bin`) {
		t.Fatalf("the screen of a data folder lists its files and shows them: %s", s)
	}
	var del struct{ Trash int64 }
	e.must("deck.data_delete", map[string]any{"project": "shorts", "path": "cache/tiles.bin"}, &del)
	e.must("deck.trash_restore", map[string]any{"project": "shorts", "path": "cache/tiles.bin"}, nil)
	if _, err := os.Stat(filepath.Join(data, "cache", "tiles.bin")); err != nil {
		t.Fatalf("the undo brings the file back: %v", err)
	}
	e.must("deck.data_delete", map[string]any{"project": "shorts", "path": "cache"}, &del)
	trash := deckhome.Layout(e.App.Cfg.Home).Trash(del.Trash)
	e.App.Records.PurgeTrash(context.Background())
	if _, err := os.Stat(trash); err != nil {
		t.Fatalf("a deletion waits a day: %v", err)
	}
	clock.Add(25 * time.Hour)
	e.App.Records.PurgeTrash(context.Background())
	if _, err := os.Stat(trash); !os.IsNotExist(err) {
		t.Fatal("after a day it is gone for good")
	}
	if code, out := e.call("deck.trash_restore", map[string]any{"trash": del.Trash}); code == 200 {
		t.Fatalf("nothing is left to bring back: %s", out)
	}
}
