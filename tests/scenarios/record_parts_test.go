package scenarios

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	deckhome "deck2/internal/home"
)

// shortsModule keeps videos, each with a folder of its own in the data folder, and the scenes of each video.
const shortsModule = `{"version": 13,
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

// TestDeletingARecordTakesWhatBelongsToIt: a record goes with its child records, files, folder and the files of its
// processes, which the owner sees listed first.
func TestDeletingARecordTakesWhatBelongsToIt(t *testing.T) {
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
	e.drained()
	count := func(q string, args ...any) int {
		var n int
		_ = e.App.Store.DB.QueryRow(q, args...).Scan(&n)
		return n
	}
	if _, err := os.Stat(filepath.Join(data, "videos", "knights")); !os.IsNotExist(err) || count(`SELECT count(*) FROM record WHERE project_id = ?`, pid) != 2 ||
		count(`SELECT count(*) FROM file WHERE project_id = ?`, pid) != 0 {
		t.Fatal("the record, its scene, its files, the files of its process and its folder are gone")
	}
}

// TestTheDataFolderOfAProjectShowsByItsFolders: a data folder is a file manager, the way up in its steps and folders
// first as tiles that open them, then files; the modules at the root and the files of a module are tiles too.
func TestTheDataFolderOfAProjectShowsByItsFolders(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	pid := e.createProject("shorts", shortsModule)
	data := deckhome.Layout(e.App.Cfg.Home).Data(pid)
	if err := errors.Join(os.MkdirAll(filepath.Join(data, "cache", "zz"), 0o700), os.WriteFile(filepath.Join(data, "cache", "tiles.bin"), []byte("x"), 0o600)); err != nil {
		t.Fatal(err)
	}
	type tile struct {
		Title, Icon, URL, Error string
		Actions                 []map[string]any
	}
	block := e.screenBlock("", "files", "Modules", map[string]any{"params": `{"path": "shorts/data/cache"}`})
	tiles := blockData[[]tile](t, block["files"])
	if len(tiles) != 2 || tiles[0].Title != "zz" || tiles[0].Icon != "folder" ||
		tiles[1].Title != "tiles.bin" || tiles[1].Icon != "" || !strings.Contains(tiles[1].URL, "/api/files/data/shorts/cache/tiles.bin") {
		t.Fatalf("the folders come first as tiles, then the files: %+v", tiles)
	}
	if crumbs := string(block["crumbs"]); !strings.Contains(crumbs, `"path":"shorts/data"`) {
		t.Fatalf("the way up is in the steps of the folder: %s", crumbs)
	}
	params, _ := verb(t, tiles[0].Actions, "open")["params"].(map[string]any)
	if open := verb(t, tiles[0].Actions, "open"); open["primary"] != true || open["screen"] != "files" || params["path"] != "shorts/data/cache/zz" {
		t.Fatalf("a folder tile opens the folder: %+v", open)
	}
	if len(tiles[1].Actions) != 0 {
		t.Fatalf("a file has no button to open a folder: %+v", tiles[1].Actions)
	}
	own := blockData[[]tile](t, e.screenData("shorts", "documents", map[string]any{"params": `{"path": "data/cache"}`})["b0"]["files"])
	if len(own) != 2 || own[1].Error != "" || !strings.Contains(own[1].URL, "/api/files/data/shorts/cache/tiles.bin") {
		t.Fatalf("the files screen of a module shows its data folder inside it: %+v", own)
	}

	raw, _ := json.Marshal(e.screenBlock("", "files", "Modules", nil))
	if s := string(raw); !strings.Contains(s, `"path":"shorts"`) || !strings.Contains(s, `"title":"Shorts"`) && !strings.Contains(s, `"title":"shorts"`) {
		t.Fatalf("the modules are folder tiles at the root: %s", s)
	}
	e.must("deck.file_upload", map[string]any{"project": "shorts", "name": "frame.png", "content": base64.StdEncoding.EncodeToString([]byte("png"))}, nil)
	raw, _ = json.Marshal(e.screenData("shorts", "documents", map[string]any{"params": `{"path": "files"}`}))
	if s := string(raw); !strings.Contains(s, `"name":"frame.png"`) {
		t.Fatalf("the files screen of a module shows its files: %s", s)
	}
}
