package modules

import (
	"deck2/tests/harness"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// unused are parts deck dropped since no module used them, each as a change of videosManifest; a manifest has none.
var unused = [][2]string{
	{harness.Manifest(`{`), harness.Manifest(`{"master_errors": false,`)},
	{`"display": "slug",`, `"display": "slug", "file_retention_days": 10,`},
	{`"item": {"file": "file", "title": "slug"}}`, `"item": {"file": "file", "title": "slug", "marks": "marks"},
      "mark": {"verb": "mark", "command": "note", "params": {"video": "=slug"}, "input": "note"}}`},
	{`"format": "duration"`, `"format": "timecode"`},
	{`"item": {"title": "slug"}}`, `"item": {"title": "slug", "image": "file", "markdown": true}}`},
	{`{"type": "chart",`, `{"type": "chart", "kind": "area",`},
	{`[{"label": "Again", "screen": "watch"}]`, `[{"label": "Again", "screen": "watch"}, {"label": "Get", "file": "=slug"}]`},
	{`"menu": ["watch"]}`, `"menu": ["watch"], "reads": [{"project": "other", "collections": ["items"]}]}`},
	{`{"type": "actions",`, `{"type": "columns", "blocks": [{"type": "table", "source": {"project": "other", "collection": "items"},
      "columns": [{"label": "N", "bind": "n"}]}]}, {"type": "actions",`},
}

// TestAManifestRefusesWhatNoModuleUsed: a video of a module plays from its address without marks, and a manifest
// with any of unused is refused.
func TestAManifestRefusesWhatNoModuleUsed(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	check := harness.NewCatalogCheck(t)
	p := e.Project("films", harness.VideosManifest)
	if err := errors.Join(os.MkdirAll(e.App.Home.Data(p), 0o700), os.WriteFile(filepath.Join(e.App.Home.Data(p), "eu-solar.webm"), []byte("webm"), 0o600)); err != nil {
		t.Fatal(err)
	}
	e.Must("deck.records_upsert", map[string]any{"project": "films", "collection": "videos", "records": harness.Jsonl(
		map[string]any{"slug": "eu-solar", "file": "eu-solar.webm", "length": 75})}, nil)
	check.Messages(t, e.ScreenMessages("films", "watch", nil))
	var files []map[string]any
	_ = json.Unmarshal(e.ScreenData("films", "watch", nil)["b0"]["files"], &files)
	if len(files) != 1 || files[0]["type"] != "video/webm" || files[0]["url"] == "" || files[0]["marks"] != nil || files[0]["mark"] != nil {
		t.Fatalf("the video plays from its address, without marks: %+v", files)
	}
	for _, u := range unused {
		if u[1] == unused[7][1] {
			continue
		}
		if code, out := e.Call("deck.manifest_save", map[string]any{"project": "films", "manifest": strings.Replace(harness.VideosManifest, u[0], u[1], 1)}); code != 422 {
			t.Fatalf("a manifest has no %s: %d %s", u[1], code, out)
		}
	}
}

// TestAMediaBlockOnARecordPageShowsAFileOfTheRecord: on a record page a media block without a source shows the
// file a field of the record names.
func TestAMediaBlockOnARecordPageShowsAFileOfTheRecord(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	m := strings.Replace(harness.VideosManifest, `"screens": [`, `"screens": [{"name": "video", "title": "Video", "record": "videos", "blocks": [
    {"type": "media", "title": "The video", "item": {"file": "file", "title": "slug"}}]}, `, 1)
	p := e.Project("films", m)
	if err := errors.Join(os.MkdirAll(e.App.Home.Data(p), 0o700), os.WriteFile(filepath.Join(e.App.Home.Data(p), "eu-solar.webm"), []byte("webm"), 0o600)); err != nil {
		t.Fatal(err)
	}
	var w struct{ IDs []int64 }
	e.Must("deck.records_upsert", map[string]any{"project": "films", "collection": "videos", "records": harness.Jsonl(map[string]any{"slug": "eu-solar", "file": "eu-solar.webm"})}, &w)
	var files []struct{ Type, Name, Title string }
	_ = json.Unmarshal(e.ScreenData("films", "video", map[string]any{"record": w.IDs[0]})["b0"]["files"], &files)
	if len(files) != 1 || files[0].Name != "eu-solar.webm" || files[0].Type != "video/webm" || files[0].Title != "eu-solar" {
		t.Fatalf("the record page plays the file of its record: %+v", files)
	}
}

// TestALineOfMediaSaysWhyItHasNoFileInWords: an expression of a line that fails names itself and the fault,
// without the drawing of where in it.
func TestALineOfMediaSaysWhyItHasNoFileInWords(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("coin", strings.Replace(harness.EstimateManifest, `"item": {"file": "file", "title": "title"}`, `"item": {"file": "int(file)", "title": "title"}`, 1))
	e.Must("deck.records_upsert", map[string]any{"project": "coin", "collection": "attachments",
		"records": harness.Jsonl(map[string]any{"title": "a", "file": "image-1b946491.png"})}, nil)
	raw, _ := json.Marshal(e.ScreenMessages("coin", "files", nil))
	if s := string(raw); !strings.Contains(s, `the file of this line: int(file): `) || strings.Contains(s, `| ^`) || strings.Contains(s, `(1:1)`) {
		t.Fatalf("the line says in words why it has no file: %s", s)
	}
}
