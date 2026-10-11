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
const videosManifest = `{"version": 12,
  "collections": [{"name": "videos", "description": "Videos", "key": "slug", "display": "slug",
    "schema": {"type": "object", "properties": {"slug": {"type": "string"}, "file": {"type": "string"}, "length": {"type": "number"}}, "required": ["slug"]}}],
  "screens": [{"name": "watch", "title": "Watch", "blocks": [
    {"type": "media", "title": "Videos", "span": "full", "source": {"collection": "videos", "sort": "slug"},
      "item": {"file": "file", "title": "slug"}},
    {"type": "table", "source": {"collection": "videos"}, "columns": [{"label": "Length", "bind": "length", "format": "duration"}]}]}],
  "menu": ["watch"]}`

// TestAStoredPlayerWithMarksIsReadWithout: a manifest of format 11 stored with the marks, the mark button of a
// media block and the format timecode is read raised, without them, and its screen shows the video.
func TestAStoredPlayerWithMarksIsReadWithout(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	check := newCatalogCheck(t)
	p := e.createProject("shorts", videosManifest)
	if err := errors.Join(os.MkdirAll(e.App.Home.Data(p), 0o700), os.WriteFile(filepath.Join(e.App.Home.Data(p), "eu-solar.webm"), []byte("webm"), 0o600)); err != nil {
		t.Fatal(err)
	}
	old := strings.NewReplacer(`{"version": 12,`, `{"version": 11,`,
		`"item": {"file": "file", "title": "slug"}}`, `"item": {"file": "file", "title": "slug", "marks": "marks"},
      "mark": {"verb": "mark", "command": "note", "params": {"video": "=slug"}, "input": "note"}}`,
		`"format": "duration"`, `"format": "timecode"`,
		`"screens": [{"name": "watch", "title": "Watch", "blocks"`, `"screens": [{"name": "watch", "title": "Watch", "params": {"t": "number"}, "blocks"`).Replace(videosManifest)
	if _, err := e.App.Store.DB.Exec(`UPDATE document_revision SET text = ?
		WHERE document_id IN (SELECT id FROM document WHERE kind = 'manifest' AND project_id = ?)`, old, p); err != nil {
		t.Fatal(err)
	}
	var got struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "shorts"}, &got)
	if !strings.Contains(got.Manifest, `"version": 12`) || strings.Contains(got.Manifest, "marks") || strings.Contains(got.Manifest, `"mark"`) ||
		strings.Contains(got.Manifest, "timecode") || !strings.Contains(got.Manifest, `"format": "duration"`) {
		t.Fatalf("format 11 comes up to 12 without the marks of a player:\n%s", got.Manifest)
	}
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(
		map[string]any{"slug": "eu-solar", "file": "eu-solar.webm", "length": 75})}, nil)
	check.messages(t, e.screenMessages("shorts", "watch", map[string]any{"params": `{"t": "12"}`}))
	var files []map[string]any
	_ = json.Unmarshal(e.screenData("shorts", "watch", nil)["b0"]["files"], &files)
	if len(files) != 1 || files[0]["type"] != "video/webm" || files[0]["url"] == "" || files[0]["marks"] != nil || files[0]["mark"] != nil {
		t.Fatalf("the video plays from its address, without marks: %+v", files)
	}
	for _, field := range []string{`"marks": "marks"`, `"format": "timecode"`} {
		bad := strings.Replace(videosManifest, `"title": "slug"}`, `"title": "slug", `+field+`}`, 1)
		if field == `"format": "timecode"` {
			bad = strings.Replace(videosManifest, `"format": "duration"`, field, 1)
		}
		if code, out := e.call("deck.manifest_save", map[string]any{"project": "shorts", "manifest": bad}); code != 422 {
			t.Fatalf("format 12 has no %s: %d %s", field, code, out)
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
	e := newEnv(t, nil)
	p := e.createProject("shorts", strings.Replace(videosManifest, `"screens": [`, `"screens": [{"name": "scenes", "title": "Scenes", "blocks": [
    {"type": "list", "title": "Scenes", "source": {"collection": "videos", "sort": "slug"}, "item": {"title": "slug", "image": "'frame.png'"}},
    {"type": "media", "title": "All", "source": {"collection": "videos", "sort": "slug"}, "item": {"file": "file", "title": "slug"}}]}, `, 1))
	data := e.App.Home.Data(p)
	_ = os.MkdirAll(data, 0o700)
	for _, args := range [][]string{
		{"-f", "lavfi", "-i", "testsrc=duration=3:size=160x120:rate=30", "-c:v", "libvpx", "eu-solar.webm"},
		{"-f", "lavfi", "-i", "testsrc=size=64x48", "-frames:v", "1", "frame.png"},
		{"-f", "lavfi", "-i", "sine=duration=2", "voice.wav"},
	} {
		args[len(args)-1] = filepath.Join(data, args[len(args)-1])
		if out, err := exec.Command("ffmpeg", append([]string{"-v", "error"}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg: %v %s", err, out)
		}
	}
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(
		map[string]any{"slug": "eu-solar", "file": "eu-solar.webm"})}, nil)

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1200, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/p/shorts/watch")
	b.until("the video in the browser's own player", `(() => { const v = document.querySelector('video[data-player="video"]'); return v && v.controls && v.readyState >= 1 && Math.abs(v.duration - 3) < 0.1 })()`)
	b.eval(`(document.querySelector('[data-player="video"]').muted = true, document.querySelector('[data-player="video"]').play(), '')`)
	b.until("the video playing", `!document.querySelector('[data-player="video"]').paused`)
	b.shot("video")
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(
		map[string]any{"slug": "voice", "file": "voice.wav"})}, nil)
	b.open(e.URL + "/?token=" + token + "#/p/shorts/scenes")
	b.until("the picture of a line of a list", `document.querySelector('[data-line-image]')?.naturalWidth === 64`)
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
	e := newEnv(t, nil)
	m := strings.Replace(videosManifest, `"screens": [`, `"screens": [{"name": "video", "title": "Video", "record": "videos", "blocks": [
    {"type": "media", "title": "The video", "item": {"file": "file", "title": "slug"}}]}, `, 1)
	p := e.createProject("shorts", m)
	if err := errors.Join(os.MkdirAll(e.App.Home.Data(p), 0o700), os.WriteFile(filepath.Join(e.App.Home.Data(p), "eu-solar.webm"), []byte("webm"), 0o600)); err != nil {
		t.Fatal(err)
	}
	var w struct{ IDs []int64 }
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(map[string]any{"slug": "eu-solar", "file": "eu-solar.webm"})}, &w)
	var files []struct{ Type, Name, Title string }
	_ = json.Unmarshal(e.screenData("shorts", "video", map[string]any{"record": w.IDs[0]})["b0"]["files"], &files)
	if len(files) != 1 || files[0].Name != "eu-solar.webm" || files[0].Type != "video/webm" || files[0].Title != "eu-solar" {
		t.Fatalf("the record page plays the file of its record: %+v", files)
	}
}

// TestALineOfAListShowsAPicture: item.image of a list names a picture of a line; a file that is no picture is left
// out of the line.
func TestALineOfAListShowsAPicture(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	m := strings.Replace(videosManifest, `"screens": [`, `"screens": [{"name": "scenes", "title": "Scenes", "blocks": [
    {"type": "list", "title": "Scenes", "source": {"collection": "videos", "sort": "slug"}, "item": {"title": "slug", "image": "file"}}]}, `, 1)
	p := e.createProject("shorts", m)
	_ = os.MkdirAll(e.App.Home.Data(p), 0o700)
	_ = os.WriteFile(filepath.Join(e.App.Home.Data(p), "a.png"), []byte("\x89PNG\r\n\x1a\n"), 0o600)
	_ = os.WriteFile(filepath.Join(e.App.Home.Data(p), "b.webm"), []byte("webm"), 0o600)
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(
		map[string]any{"slug": "a", "file": "a.png"}, map[string]any{"slug": "b", "file": "b.webm"})}, nil)
	var items []struct{ Title, Image string }
	_ = json.Unmarshal(e.screenData("shorts", "scenes", nil)["b0"]["items"], &items)
	if len(items) != 2 || !strings.Contains(items[0].Image, "a.png") || items[1].Image != "" {
		t.Fatalf("a line shows its picture and nothing for a file that is none: %+v", items)
	}
}
