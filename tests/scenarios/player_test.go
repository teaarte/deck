package scenarios

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
)

// playerManifest is a module of short videos: a media block plays each video with its marks, and its mark sends
// a second of a video back with a note.
const playerManifest = `{"version": 8,
  "collections": [{"name": "videos", "description": "Videos", "key": "slug", "display": "slug",
    "schema": {"type": "object", "properties": {"slug": {"type": "string"}, "file": {"type": "string"}, "marks": {"type": "array"}}, "required": ["slug"]}}],
  "commands": [{"name": "note", "title": "Note", "description": "Sends a second of a video back for rework.", "class": "internal",
    "input": {"type": "object", "properties": {"video": {"type": "string"}, "file": {"type": "string"}, "at": {"type": "number"}, "note": {"type": "string"}},
      "required": ["video", "file", "at"]},
    "executor": {"type": "agent", "prompt": "Rework the scene."}, "result": {"type": "object", "properties": {"n": {"type": "integer"}}}}],
  "screens": [{"name": "watch", "title": "Watch", "params": {"t": "number"}, "blocks": [
    {"type": "media", "title": "Videos", "span": "full", "source": {"collection": "videos", "sort": "slug"},
      "item": {"file": "file", "title": "slug", "marks": "marks"},
      "mark": {"verb": "mark", "command": "note", "params": {"video": "=slug"}, "input": "note"}}]}],
  "menu": ["watch"]}`

// TestAPlayerShowsMarksAndMarksASecond: a video of the data folder plays with its marks in order of time; the mark
// of the player names its command with the video, which takes the file, the second and the note.
func TestAPlayerShowsMarksAndMarksASecond(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	check := newCatalogCheck(t)
	p := e.createProject("shorts", playerManifest)
	if err := errors.Join(os.MkdirAll(e.App.Home.Data(p), 0o700), os.WriteFile(filepath.Join(e.App.Home.Data(p), "eu-solar.webm"), []byte("webm"), 0o600)); err != nil {
		t.Fatal(err)
	}
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(
		map[string]any{"slug": "eu-solar", "file": "eu-solar.webm", "marks": []any{map[string]any{"at": 12.5, "text": "scene 4: the map is late"}, 3}})}, nil)
	check.messages(t, e.screenMessages("shorts", "watch", nil))
	var files []struct {
		Type, File, URL string
		Marks           []struct {
			At   float64
			Text string
		}
		Mark struct {
			Command, Input string
			Params         map[string]any
		}
	}
	_ = json.Unmarshal(e.screenData("shorts", "watch", nil)["b0"]["files"], &files)
	if len(files) != 1 || files[0].Type != "video/webm" || files[0].File != "eu-solar.webm" || files[0].URL == "" {
		t.Fatalf("the video plays from its address: %+v", files)
	}
	if m := files[0].Marks; len(m) != 2 || m[0].At != 3 || m[1].At != 12.5 || m[1].Text != "scene 4: the map is late" {
		t.Fatalf("the marks come in order of time: %+v", m)
	}
	mark := files[0].Mark
	if mark.Command != "shorts.note" || mark.Input != "note" || mark.Params["video"] != "eu-solar" {
		t.Fatalf("the mark calls the command of the module with the video: %+v", mark)
	}
	in := map[string]any{"file": files[0].File, "at": 12.5, "note": "the map comes late"}
	for k, v := range mark.Params {
		in[k] = v
	}
	var started struct{ Process int64 }
	e.must(mark.Command, in, &started)
	e.waitState(started.Process, "done")
	got, _ := e.App.Store.Q.GetProcess(context.Background(), started.Process)
	if !strings.Contains(got.Input, `"at":12.5`) || !strings.Contains(got.Input, "the map comes late") {
		t.Fatalf("the command gets the second and the note: %s", got.Input)
	}
	for _, tc := range []struct{ from, to, want string }{
		{`"at": {"type": "number"}, `, ``, `has no input field "at"`},
		{`"mark": {"verb": "mark", "command": "note"`, `"mark": {"verb": "mark", "command": "gone"`, `unknown command "gone"`},
		{`"marks": "marks"}`, `"marks": "marks +"}`, `item:`},
	} {
		bad := strings.Replace(playerManifest, tc.from, tc.to, 1)
		if code, out := e.call("deck.manifest_save", map[string]any{"project": "shorts", "manifest": bad}); code != 422 || !strings.Contains(string(out), tc.want) {
			t.Fatalf("%s: %d %s", tc.want, code, out)
		}
	}
}

// TestRealBrowserPlayer plays a video of a module in the real browser: it opens at t, steps by a frame, seeks to
// a mark and marks a second with a note.
func TestRealBrowserPlayer(t *testing.T) {
	if os.Getenv("DECK_REAL_BROWSER") == "" {
		t.Skip("set DECK_REAL_BROWSER=1 to run against Playwright MCP")
	}
	checkBuiltClient(t)
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg makes the video")
	}
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	p := e.createProject("shorts", strings.Replace(playerManifest, `"screens": [`, `"screens": [{"name": "scenes", "title": "Scenes", "blocks": [
    {"type": "list", "title": "Scenes", "source": {"collection": "videos", "sort": "slug"}, "item": {"title": "slug", "image": "'frame.png'"}}]}, `, 1))
	data := e.App.Home.Data(p)
	_ = os.MkdirAll(data, 0o700)
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=duration=3:size=160x120:rate=30", "-c:v", "libvpx",
		filepath.Join(data, "eu-solar.webm")).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	if out, err := exec.Command("ffmpeg", "-v", "error", "-f", "lavfi", "-i", "testsrc=size=64x48", "-frames:v", "1",
		filepath.Join(data, "frame.png")).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v %s", err, out)
	}
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(
		map[string]any{"slug": "eu-solar", "file": "eu-solar.webm", "marks": []any{map[string]any{"at": 2.5, "text": "scene 4"}, 0.5}})}, nil)

	b := newPlaywright(t)
	b.call("browser_resize", map[string]any{"width": 1200, "height": 900})
	b.open(e.URL + "/?token=" + token + "#/p/shorts/watch?t=1")
	b.until("the video opened at t", `(() => { const v = document.querySelector('[data-player="video"]'); return v && v.readyState >= 1 && Math.abs(v.currentTime - 1) < 0.05 })()`)
	b.until("the marks on the timeline and in the list", `document.querySelectorAll('[data-tick]').length === 2 && document.querySelectorAll('[data-mark-at]').length === 2`)
	b.until("the timecode", `document.querySelector('[data-timecode]').innerText.startsWith('0:01')`)
	b.eval(`(document.querySelector('[data-frame="forward"]').click(), '')`)
	b.until("a frame forward", `(() => { const v = document.querySelector('[data-player="video"]'); return Math.abs(v.currentTime - (1 + 1 / 30)) < 0.01 })()`)
	b.eval(`(document.querySelector('[data-mark-at="2.5"]').click(), '')`)
	b.until("the player at the mark", `Math.abs(document.querySelector('[data-player="video"]').currentTime - 2.5) < 0.05 && document.querySelector('[data-timecode]').innerText.startsWith('0:02.50')`)
	b.until("the second it stopped at in the address", `location.hash.includes('t=2.5')`)
	b.eval(`(document.querySelector('[data-player="video"]').dataset.same = 'yes', document.querySelector('[data-player="video"]').muted = true, document.querySelector('[data-player="video"]').loop = true, document.querySelector('[data-player="video"]').play(), '')`)
	b.until("the video playing", `!document.querySelector('[data-player="video"]').paused`)
	b.eval(`(document.querySelector('[data-mark]').click(), '')`)
	time.Sleep(time.Second)
	b.until("a mark pressed while it plays keeps its input and the player stays the same", `document.querySelector('[data-player="video"]')?.dataset.same === 'yes' && !!document.querySelector('[data-media="video"] input') && /t=\d/.test(location.hash)`)
	b.eval(`([...document.querySelectorAll('[data-media="video"] button')].find((x) => x.innerText === 'Cancel').click(), '')`)
	b.eval(`(document.querySelector('[data-player="video"]').loop = false, document.querySelector('[data-mark-at="2.5"]').click(), '')`)
	b.until("the player back at the mark", `(() => { const v = document.querySelector('[data-player="video"]'); return v.paused && Math.abs(v.currentTime - 2.5) < 0.05 })()`)
	b.until("the marks beside the player with a scroll of their own", `(() => { const v = document.querySelector('[data-player="video"]').getBoundingClientRect(), m = document.querySelector('[data-marks]').getBoundingClientRect(); return m.left >= v.right - 1 && getComputedStyle(document.querySelector('[data-marks]')).overflowY === 'auto' })()`)
	b.shot("player")
	b.eval(`(document.querySelector('[data-mark]').click(), '')`)
	b.type_(`document.querySelector('[data-media="video"] input')`, "the map comes late")
	b.eval(`([...document.querySelectorAll('[data-media="video"] button')].find((x) => x.innerText === 'Send').click(), '')`)
	e.waitFor("the mark sent to the module", func() bool {
		for _, pr := range e.processes() {
			if pr.Command == "note" {
				got, _ := e.App.Store.Q.GetProcess(context.Background(), pr.ID)
				return strings.Contains(got.Input, `"at":2.5`) && strings.Contains(got.Input, "the map comes late") && strings.Contains(got.Input, `"video":"eu-solar"`)
			}
		}
		return false
	})
	b.open(e.URL + "/?token=" + token + "#/p/shorts/scenes")
	b.until("the picture of a line of a list", `document.querySelector('[data-line-image]')?.naturalWidth === 64`)
}

// TestAMediaBlockOnARecordPageShowsAFileOfTheRecord: on a record page a media block without a source shows the
// file a field of the record names.
func TestAMediaBlockOnARecordPageShowsAFileOfTheRecord(t *testing.T) {
	e := newEnv(t, nil)
	m := strings.Replace(playerManifest, `"screens": [`, `"screens": [{"name": "video", "title": "Video", "record": "videos", "blocks": [
    {"type": "media", "title": "The video", "item": {"file": "file", "title": "slug"}}]}, `, 1)
	p := e.createProject("shorts", m)
	if err := errors.Join(os.MkdirAll(e.App.Home.Data(p), 0o700), os.WriteFile(filepath.Join(e.App.Home.Data(p), "eu-solar.webm"), []byte("webm"), 0o600)); err != nil {
		t.Fatal(err)
	}
	var w struct{ IDs []int64 }
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(map[string]any{"slug": "eu-solar", "file": "eu-solar.webm"})}, &w)
	var files []struct{ Type, File, Title string }
	_ = json.Unmarshal(e.screenData("shorts", "video", map[string]any{"record": w.IDs[0]})["b0"]["files"], &files)
	if len(files) != 1 || files[0].File != "eu-solar.webm" || files[0].Type != "video/webm" || files[0].Title != "eu-solar" {
		t.Fatalf("the record page plays the file of its record: %+v", files)
	}
}

// TestALineOfAListShowsAPicture: item.image of a list names a picture of a line; a file that is no picture is left
// out of the line.
func TestALineOfAListShowsAPicture(t *testing.T) {
	e := newEnv(t, nil)
	m := strings.Replace(playerManifest, `"screens": [`, `"screens": [{"name": "scenes", "title": "Scenes", "blocks": [
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
