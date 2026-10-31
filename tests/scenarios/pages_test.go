package scenarios

import (
	"bytes"
	"context"
	"deck2/tests/harness"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	gopng "image/png"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestATabNotShownIsReadWhenShown: a screen reads the blocks of the tab shown only, the one its address names or the
// first; a read of the block of another tab gives its blocks, as the client does when the owner shows it.
func TestATabNotShownIsReadWhenShown(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("notes", webManifest("http://127.0.0.1:1"))
	e.Must("deck.records_upsert", map[string]any{"project": "notes", "collection": "notes", "records": harness.Jsonl(map[string]any{"title": "alpha", "score": 7})}, nil)
	read := func(extra map[string]any) []string {
		return slices.Sorted(maps.Keys(harness.DataOf(t, e.ScreenMessages("notes", "main", extra))))
	}
	if got := read(nil); !slices.Contains(got, "b1-0-0") || slices.Contains(got, "b1-1-0") {
		t.Fatalf("the first tab is read, the other waits: %v", got)
	}
	if got := read(map[string]any{"tab": "Top"}); slices.Contains(got, "b1-0-0") || !slices.Contains(got, "b1-1-0") {
		t.Fatalf("the tab the address names is read instead: %v", got)
	}
	if got := read(map[string]any{"block": "b1-1"}); !slices.Equal(got, []string{"b1-1-0"}) {
		t.Fatalf("a read of the block of a tab gives its blocks: %v", got)
	}
	var tabs int
	all := harness.DataOf(t, e.ScreenMessages("", "spend", nil))
	whole := e.ScreenData("", "spend", nil)
	for id := range whole {
		if strings.HasPrefix(id, "b2-") {
			tabs++
		}
	}
	if len(whole)-len(all) < 3 || tabs == 0 {
		t.Fatalf("Spend reads its stats and the tab shown, not the other tabs: %d of %d", len(all), len(whole))
	}
}

// TestABlockWithoutALimitShowsAPage: a table or list without a limit shows a page and more by pages, details that
// repeat the cell are left out, a save names such a block on a collection past a page.
func TestABlockWithoutALimitShowsAPage(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	m := strings.Replace(harness.WebNotes, `"columns": [{"label": "Title", "bind": "title"}]`,
		`"columns": [{"label": "Title", "bind": "title", "details": "title"}, {"label": "Note", "bind": "source", "details": "note"}]`, 1)
	e.Project("web", m)
	var recs []map[string]any
	for i := range 60 {
		recs = append(recs, map[string]any{"title": fmt.Sprintf("note %02d", i), "source": "web", "note": "longer"})
	}
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": harness.Jsonl(recs...)}, nil)
	data := harness.DataOf(t, e.ScreenMessages("web", "main", nil))
	rows := harness.BlockData[[]harness.ScreenRow](t, data["b3"]["rows"])
	if len(rows) != 50 || string(data["b3"]["more"]) != "10" || len(harness.BlockData[[]map[string]any](t, data["b0"]["items"])) != 50 {
		t.Fatalf("a table and a list without a limit show a page and say how many more: %d rows, more %s", len(rows), data["b3"]["more"])
	}
	if d := rows[0].Details; len(d) != 2 || d[0] != nil || d[1] != "longer" {
		t.Fatalf("details that say what the cell says are left out: %v", d)
	}
	more := harness.DataOf(t, e.ScreenMessages("web", "main", map[string]any{"pages": `{"b3": 2}`}))
	if n := len(harness.BlockData[[]harness.ScreenRow](t, more["b3"]["rows"])); n != 60 {
		t.Fatalf("the next page shows the rest: %d", n)
	}
	var saved struct{ Warnings []string }
	e.Must("deck.manifest_save", map[string]any{"project": "web", "manifest": m}, &saved)
	if len(saved.Warnings) != 2 || !strings.Contains(saved.Warnings[0], `screen main block "Notes" has no limit and notes holds 60 records`) {
		t.Fatalf("a save names the blocks without a limit on a collection past a page: %v", saved.Warnings)
	}
	raw, _ := json.Marshal(saved)
	if strings.Contains(string(raw), "Chart") {
		t.Fatalf("a custom block keeps every row: %s", raw)
	}
}

// TestAPictureShowsSmallByItsLightCopy: a stored picture gets a light copy with the size and main color of the
// picture, a small one is its own; a picture of a data folder has one made when asked; the copies are no files.
func TestAPictureShowsSmallByItsLightCopy(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	p := e.Project("shorts", videosManifest)
	png := func(w, h int) string {
		img := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.Draw(img, img.Bounds(), &image.Uniform{color.RGBA{20, 120, 220, 255}}, image.Point{}, draw.Src)
		for i := range img.Pix {
			img.Pix[i] ^= byte(i * 7)
		}
		var b bytes.Buffer
		_ = gopng.Encode(&b, img)
		return b.String()
	}
	big := e.Upload("shorts", "big.png", "image/png", png(1600, 900))
	small := e.Upload("shorts", "small.png", "image/png", png(64, 36))
	if err := errors.Join(os.MkdirAll(e.App.Home.Data(p), 0o700), os.WriteFile(filepath.Join(e.App.Home.Data(p), "wide.png"), []byte(png(1500, 500)), 0o600)); err != nil {
		t.Fatal(err)
	}
	e.App.Files.Thumbs(context.Background())
	var list []struct {
		ID            int64
		Thumb, Color  string
		Width, Height int
	}
	e.Must("deck.files_list", map[string]any{"project": "shorts"}, &list)
	seen := map[int64]string{}
	for _, f := range list {
		seen[f.ID] = fmt.Sprintf("%s %dx%d %t", f.Thumb, f.Width, f.Height, f.Color != "")
	}
	if len(list) != 2 || seen[small] != fmt.Sprintf("/api/files/%d 64x36 true", small) || !strings.HasSuffix(seen[big], " 1600x900 true") ||
		strings.Contains(seen[big], fmt.Sprintf("/api/files/%d ", big)) {
		t.Fatalf("a picture has its light copy with its size, a small one is its own, and the copies are no files: %v", seen)
	}
	e.Must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": harness.Jsonl(
		map[string]any{"slug": "a", "file": fmt.Sprint(big)}, map[string]any{"slug": "b", "file": "wide.png"})}, nil)
	var files []struct {
		URL, Thumb    string
		Width, Height int
	}
	_ = json.Unmarshal(e.ScreenData("shorts", "watch", nil)["b0"]["files"], &files)
	if len(files) != 2 || files[0].Thumb == "" || files[0].Thumb == files[0].URL || files[1].Thumb != files[1].URL+"?thumb=1" || files[1].Width != 1500 {
		t.Fatalf("a media block gives the light copy and the size of each picture: %+v", files)
	}
	req, _ := http.NewRequest("GET", e.URL+files[1].Thumb, nil)
	e.Sign(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if cfg, err := jpeg.DecodeConfig(resp.Body); err != nil || cfg.Width != 720 {
		t.Fatalf("the light copy of a picture of a data folder is a JPEG as wide as a light copy: %+v %v", cfg, err)
	}
}

// TestAChatReadsItsPartsByTheirOwnTopics: a chat block reads the topic picked with its messages, the topics and the
// turn apart; a line of a log reads the turn alone, and only a line of the master's.
func TestAChatReadsItsPartsByTheirOwnTopics(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	paths := func(extra map[string]any) []string {
		var out []string
		for _, m := range e.ScreenMessages("", "chat", extra) {
			var dm struct{ Path string }
			if json.Unmarshal(m["updateDataModel"], &dm) == nil && dm.Path != "" {
				out = append(out, dm.Path)
			}
		}
		slices.Sort(out)
		return out
	}
	for _, c := range []struct {
		in   map[string]any
		want []string
	}{
		{nil, []string{"/b0", "/b0-topics", "/b0-turn"}},
		{map[string]any{"topic": "log", "changed": "master"}, []string{"/b0-turn"}},
		{map[string]any{"topic": "log", "changed": "notes"}, nil},
		{map[string]any{"topic": "chat"}, []string{"/b0", "/b0-topics"}},
		{map[string]any{"topic": "asks"}, []string{"/b0-turn"}},
		{map[string]any{"topic": "assignments"}, []string{"/b0-topics"}},
	} {
		if got := paths(c.in); !slices.Equal(got, c.want) {
			t.Errorf("%v reads %v, want %v", c.in, got, c.want)
		}
	}
}
