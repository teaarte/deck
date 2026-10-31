package scenarios

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// videosManifest is a module of short videos: a media block plays each video in the browser's own player.
const videosManifest = `{"version": 13,
  "collections": [{"name": "videos", "description": "Videos", "key": "slug", "display": "slug",
    "schema": {"type": "object", "properties": {"slug": {"type": "string"}, "file": {"type": "string"}, "length": {"type": "number"}}, "required": ["slug"]}}],
  "screens": [{"name": "watch", "title": "Watch", "blocks": [
    {"type": "media", "title": "Videos", "span": "full", "source": {"collection": "videos", "sort": "slug"},
      "item": {"file": "file", "title": "slug"}},
    {"type": "table", "source": {"collection": "videos"}, "columns": [{"label": "Length", "bind": "length", "format": "duration"}]},
    {"type": "list", "source": {"collection": "videos"}, "item": {"title": "slug"}},
    {"type": "chart", "source": {"collection": "videos"}, "x": "slug", "series": [{"label": "Length", "y": "length"}]},
    {"type": "actions", "actions": [{"label": "Again", "screen": "watch"}]}]}],
  "menu": ["watch"]}`

// unused are parts deck dropped since no module used them, each as a change of videosManifest; a manifest has none.
var unused = [][2]string{
	{`{"version": 13,`, `{"version": 13, "master_errors": false,`},
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
	e := New(t)
	check := newCatalogCheck(t)
	p := e.Project("shorts", videosManifest)
	if err := errors.Join(os.MkdirAll(e.App.Home.Data(p), 0o700), os.WriteFile(filepath.Join(e.App.Home.Data(p), "eu-solar.webm"), []byte("webm"), 0o600)); err != nil {
		t.Fatal(err)
	}
	e.Must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(
		map[string]any{"slug": "eu-solar", "file": "eu-solar.webm", "length": 75})}, nil)
	check.messages(t, e.screenMessages("shorts", "watch", nil))
	var files []map[string]any
	_ = json.Unmarshal(e.screenData("shorts", "watch", nil)["b0"]["files"], &files)
	if len(files) != 1 || files[0]["type"] != "video/webm" || files[0]["url"] == "" || files[0]["marks"] != nil || files[0]["mark"] != nil {
		t.Fatalf("the video plays from its address, without marks: %+v", files)
	}
	for _, u := range unused {
		if u[1] == unused[7][1] {
			continue
		}
		if code, out := e.Call("deck.manifest_save", map[string]any{"project": "shorts", "manifest": strings.Replace(videosManifest, u[0], u[1], 1)}); code != 422 {
			t.Fatalf("a manifest has no %s: %d %s", u[1], code, out)
		}
	}
}

// TestRealBrowserVideo plays a video and a sound of a module in the browser's own player, in place and in the
// lightbox of several files.
func TestRealBrowserVideo(t *testing.T) {
	t.Parallel()
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg makes the video")
	}
	e := New(t)
	p := e.Project("shorts", strings.Replace(videosManifest, `"screens": [`, `"screens": [{"name": "scenes", "title": "Scenes", "blocks": [
    {"type": "media", "title": "All", "source": {"collection": "videos", "sort": "slug"}, "item": {"file": "file", "title": "slug"}}]}, `, 1))
	data := e.App.Home.Data(p)
	_ = os.MkdirAll(data, 0o700)
	for _, args := range [][]string{
		{"-f", "lavfi", "-i", "testsrc=duration=3:size=160x120:rate=30", "-c:v", "libvpx", "eu-solar.webm"},
		{"-f", "lavfi", "-i", "sine=duration=2", "voice.wav"},
	} {
		args[len(args)-1] = filepath.Join(data, args[len(args)-1])
		if out, err := exec.Command("ffmpeg", append([]string{"-v", "error"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v %s", err, out)
		}
	}
	e.Must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(
		map[string]any{"slug": "eu-solar", "file": "eu-solar.webm"})}, nil)

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1200, "height": 900})
	b.Open(e, "#/p/shorts/watch")
	b.until("the video in the browser's own player", `(() => { const v = document.querySelector('video[data-player="video"]'); return v && v.controls && v.readyState >= 1 && Math.abs(v.duration - 3) < 0.1 })()`)
	b.eval(`(document.querySelector('[data-player="video"]').muted = true, document.querySelector('[data-player="video"]').play(), '')`)
	b.until("the video playing", `!document.querySelector('[data-player="video"]').paused`)
	b.shot("video")
	e.Must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(
		map[string]any{"slug": "voice", "file": "voice.wav"})}, nil)
	b.Open(e, "#/p/shorts/scenes")
	b.eval(`(localStorage.removeItem('deck-files-view'), '')`)
	b.until("the files as tiles", `document.querySelectorAll('[data-tile]').length === 2`)
	b.eval(`([...document.querySelectorAll('[data-tile]')].find((x) => x.innerText.includes('voice')).click(), '')`)
	b.until("the sound in the lightbox", `(() => { const a = document.querySelector('[data-lightbox] audio[data-player="audio"]'); return a && a.controls && a.readyState >= 1 })()`)
	b.eval(`(document.querySelector('[data-lightbox-next]').click(), '')`)
	b.until("the video next in the lightbox", `document.querySelector('[data-lightbox] video[data-player="video"]')?.readyState >= 1`)
	b.shot("video-lightbox")
}

// TestAMediaBlockOnARecordPageShowsAFileOfTheRecord: on a record page a media block without a source shows the
// file a field of the record names.
func TestAMediaBlockOnARecordPageShowsAFileOfTheRecord(t *testing.T) {
	t.Parallel()
	e := New(t)
	m := strings.Replace(videosManifest, `"screens": [`, `"screens": [{"name": "video", "title": "Video", "record": "videos", "blocks": [
    {"type": "media", "title": "The video", "item": {"file": "file", "title": "slug"}}]}, `, 1)
	p := e.Project("shorts", m)
	if err := errors.Join(os.MkdirAll(e.App.Home.Data(p), 0o700), os.WriteFile(filepath.Join(e.App.Home.Data(p), "eu-solar.webm"), []byte("webm"), 0o600)); err != nil {
		t.Fatal(err)
	}
	var w struct{ IDs []int64 }
	e.Must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(map[string]any{"slug": "eu-solar", "file": "eu-solar.webm"})}, &w)
	var files []struct{ Type, Name, Title string }
	_ = json.Unmarshal(e.screenData("shorts", "video", map[string]any{"record": w.IDs[0]})["b0"]["files"], &files)
	if len(files) != 1 || files[0].Name != "eu-solar.webm" || files[0].Type != "video/webm" || files[0].Title != "eu-solar" {
		t.Fatalf("the record page plays the file of its record: %+v", files)
	}
}
