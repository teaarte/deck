package browser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// TestRealBrowserVideo plays a video and a sound of a module in the browser's own player, in place and in the
// lightbox of several files.
func TestRealBrowserVideo(t *testing.T) {
	t.Parallel()
	harness.Needs(t, "DECK_REAL_BROWSER")
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg makes the video")
	}
	e := harness.New(t)
	p := e.Project("films", strings.Replace(harness.VideosManifest, `"screens": [`, `"screens": [{"name": "scenes", "title": "Scenes", "blocks": [
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
	e.Must("deck.records_upsert", map[string]any{"project": "films", "collection": "videos", "records": harness.Jsonl(
		map[string]any{"slug": "eu-solar", "file": "eu-solar.webm"})}, nil)

	b := Open(t)
	b.call("browser_resize", map[string]any{"width": 1200, "height": 900})
	b.Open(e, "#/p/films/watch")
	b.until("the video in the browser's own player", `(() => { const v = document.querySelector('video[data-player="video"]'); return v && v.controls && v.readyState >= 1 && Math.abs(v.duration - 3) < 0.1 })()`)
	b.eval(`(document.querySelector('[data-player="video"]').muted = true, document.querySelector('[data-player="video"]').play(), '')`)
	b.until("the video playing", `!document.querySelector('[data-player="video"]').paused`)
	b.shot("video")
	e.Must("deck.records_upsert", map[string]any{"project": "films", "collection": "videos", "records": harness.Jsonl(
		map[string]any{"slug": "voice", "file": "voice.wav"})}, nil)
	b.Open(e, "#/p/films/scenes")
	b.eval(`(localStorage.removeItem('deck-files-view'), '')`)
	b.until("the files as tiles", `document.querySelectorAll('[data-tile]').length === 2`)
	b.eval(`([...document.querySelectorAll('[data-tile]')].find((x) => x.innerText.includes('voice')).click(), '')`)
	b.until("the sound in the lightbox", `(() => { const a = document.querySelector('[data-lightbox] audio[data-player="audio"]'); return a && a.controls && a.readyState >= 1 })()`)
	b.eval(`(document.querySelector('[data-lightbox-next]').click(), '')`)
	b.until("the video next in the lightbox", `document.querySelector('[data-lightbox] video[data-player="video"]')?.readyState >= 1`)
	b.shot("video-lightbox")
}
