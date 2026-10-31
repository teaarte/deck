package owner

import (
	"bytes"
	"cmp"
	"context"
	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	"image/png"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestUploadedAndRecordFilesReachTheScriptReadOnly(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("docs", harness.FilesManifest())
	e.SaveScript("docs", "scripts/seed.py", harness.SeedScript)
	e.SaveScript("docs", "scripts/read.py", harness.ReadScript)
	seed := e.WaitState(e.Start("docs", "seed", nil), "done", "error")
	var s struct{ Record int64 }
	if json.Unmarshal(seed.Result, &s) != nil || s.Record == 0 {
		t.Fatalf("seed: %+v", seed)
	}
	doc := e.Upload("docs", "note.txt", "text/plain", "uploaded text")
	var photos []struct{ ID int64 }
	e.Must("deck.files_list", map[string]any{"record": s.Record}, &photos)
	if len(photos) != 1 {
		t.Fatalf("the seed record has its photo: %+v", photos)
	}

	p := e.WaitState(e.Start("docs", "read", map[string]any{"doc": fmt.Sprint(doc), "photo": fmt.Sprint(photos[0].ID), "rec": s.Record}), "done", "error")
	var out struct{ Checks map[string]bool }
	_ = json.Unmarshal(p.Result, &out)
	for _, c := range []string{"doc_read", "doc_not_writable", "record_file_read", "record_file_not_writable", "input_folder_not_writable"} {
		if !out.Checks[c] {
			t.Fatalf("the script reads the files of its input and cannot change them: %s failed in %+v", c, p)
		}
	}
	var files []struct {
		ID   int64
		Name string
	}
	e.Must("deck.files_list", map[string]any{"record": s.Record}, &files)
	if len(files) != 2 || files[1].ID != doc {
		t.Fatalf("the uploaded file the result names now belongs to the record: %+v", files)
	}

	other := e.Project("other", harness.FilesManifest())
	_ = other
	code, body := e.Call("docs.read", map[string]any{"doc": fmt.Sprint(e.Upload("other", "x.txt", "text/plain", "x")), "rec": s.Record})
	if code == 200 || !strings.Contains(string(body), "no file") {
		t.Fatalf("a file of another project is not an input: %d %s", code, body)
	}
}

func TestUploadsNeedTheProjectAndGoWhenNoCommandTookThem(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	e.Project("docs", harness.FilesManifest())
	e.SaveScript("docs", "scripts/read.py", "import sys; print('{}')")
	_, narrow := e.Token("narrow", "docs.read")
	_, wide := e.Token("wide", "docs.*")
	form := func(tok string) int {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		w, _ := mw.CreateFormFile("file", "a.txt")
		_, _ = io.WriteString(w, "a")
		_ = mw.Close()
		req, _ := http.NewRequest("POST", e.URL+"/api/files?project=docs", &buf)
		harness.Bearer(tok)(req)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := form(narrow); code != 403 {
		t.Fatalf("a token that may not call every command of a project does not upload for it: %d", code)
	}
	if code := form(wide); code != 200 {
		t.Fatalf("a token for the whole project uploads: %d", code)
	}
	kept := e.Upload("docs", "kept.txt", "text/plain", "k")
	e.WaitState(e.Start("docs", "read", map[string]any{"doc": fmt.Sprint(kept)}), "done", "error")
	e.App.Sweep(context.Background(), e.Clock.Add(25*time.Hour))
	var uploads, all int
	var listed []struct {
		OwnerKind string `json:"owner_kind"`
	}
	e.Must("deck.files_list", nil, &listed)
	for _, f := range listed {
		if f.OwnerKind == "upload" {
			uploads++
		}
	}
	// Every file, also those in the trash and mid-move that no list shows.
	_ = e.SQLRow(`SELECT count(*) FROM file`).Scan(&all)
	if uploads != 0 || all != 1 {
		t.Fatalf("an upload no command took is gone after a day, the one a process took stays: %d uploads, %d files", uploads, all)
	}
}

func TestResultFilesACrashLeftMidMoveAreNeverShownAndGoAfterADay(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	e.Project("keep", harness.ScriptManifest("take", "", ""))
	e.SaveScript("keep", "scripts/main.py", `import json, os
open(os.path.join(os.environ["DECK_RESULTS"], "a.txt"), "w").write("a")
print(json.dumps({"files": [{"path": "a.txt"}]}))
`)
	id := e.Start("keep", "take", nil)
	e.WaitState(id, "done", "error")
	var kept []struct{ ID int64 }
	e.Must("deck.files_list", map[string]any{"process": id}, &kept)
	if len(kept) != 1 {
		t.Fatalf("a result file is kept: %+v", kept)
	}
	var left int64
	// A file a crash left mid-move into a process.
	if err := e.SQLRow(`INSERT INTO file (project_id, owner_kind, owner_id, name, created_at)
		SELECT project_id, 'keeping', id, 'b.txt', ? FROM process WHERE id = ? RETURNING id`, e.Clock.Now().UTC().Format(time.RFC3339Nano), id).Scan(&left); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(deckhome.Layout(e.App.Cfg.Home).State(), "files", fmt.Sprint(left))
	_ = os.WriteFile(path, []byte("b"), 0o600)
	var shown []struct{ ID int64 }
	e.Must("deck.files_list", map[string]any{"process": id}, &shown)
	if len(shown) != 1 || shown[0].ID != kept[0].ID {
		t.Fatalf("a file still moving belongs to no one: %+v", shown)
	}
	e.App.Sweep(context.Background(), e.Clock.Add(25*time.Hour))
	e.Drained()
	var n int
	// The row of a file mid-move, which no list shows.
	_ = e.SQLRow(`SELECT count(*) FROM file WHERE id = ?`, left).Scan(&n)
	if _, err := os.Stat(path); n != 0 || err == nil {
		t.Fatalf("the daily sweep drops what a crash left mid-move: row %d, file %v", n, err)
	}
}

func TestAResultWithAMissingFileHandsOverNoInputFileEither(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("docs", harness.FilesManifest())
	e.SaveScript("docs", "scripts/seed.py", harness.SeedScript)
	e.SaveScript("docs", "scripts/read.py", `import json, os, sys
inp = json.load(sys.stdin)
given = int(os.path.basename(inp["doc"]).split("-")[0])
print(json.dumps({"files": [{"file": given, "record": inp["rec"]}, {"path": "missing.txt", "record": inp["rec"]}]}))
`)
	seed := e.WaitState(e.Start("docs", "seed", nil), "done", "error")
	var s struct{ Record int64 }
	_ = json.Unmarshal(seed.Result, &s)
	doc := e.Upload("docs", "note.txt", "text/plain", "uploaded text")
	id := e.Start("docs", "read", map[string]any{"doc": fmt.Sprint(doc), "rec": s.Record})
	if p := e.WaitState(id, "done", "error"); p.State != "error" || !strings.Contains(p.Error, "missing.txt") {
		t.Fatalf("a result naming a file that is not there fails: %+v", p)
	}
	var onRecord []struct{ ID int64 }
	e.Must("deck.files_list", map[string]any{"record": s.Record}, &onRecord)
	if slices.ContainsFunc(onRecord, func(f struct{ ID int64 }) bool { return f.ID == doc }) {
		t.Fatalf("the input file stays with the process when the result fails: %+v", onRecord)
	}
}

// TestAFileDownloadsAsAnAttachmentForWhoMaySeeItsProject: a file of the journal downloads as an attachment
// that never renders as a page of deck; a token that may not call every command of its project gets nothing.
func TestAFileDownloadsAsAnAttachmentForWhoMaySeeItsProject(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("docs", harness.FilesManifest())
	e.Project("other", harness.FilesManifest())
	id := e.Upload("docs", "отчёт <1>.html", "text/html", "<script>alert(1)</script>")
	_, narrow := e.Token("narrow", "docs.read")
	_, wide := e.Token("wide", "docs.*")
	_, stranger := e.Token("stranger", "other.*")
	get := func(sign func(*http.Request), path string) (*http.Response, string) {
		req, _ := http.NewRequest("GET", e.URL+path, nil)
		sign(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	resp, body := get(e.Sign, fmt.Sprintf("/api/files/%d", id))
	_, params, _ := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	if resp.StatusCode != 200 || body != "<script>alert(1)</script>" || params["filename"] != "отчёт <1>.html" ||
		!strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") || resp.Header.Get("X-Content-Type-Options") != "nosniff" ||
		resp.Header.Get("Content-Security-Policy") != "sandbox" || resp.Header.Get("Content-Type") != "text/html" {
		t.Fatalf("the owner downloads the file as an attachment in a sandbox: %d %q %v", resp.StatusCode, body, resp.Header)
	}
	if resp, _ := get(harness.Bearer(wide), fmt.Sprintf("/api/files/%d", id)); resp.StatusCode != 200 {
		t.Fatalf("a token for the whole project downloads its file: %d", resp.StatusCode)
	}
	for _, c := range []struct {
		sign func(*http.Request)
		path string
		code int
	}{{harness.Bearer(narrow), fmt.Sprintf("/api/files/%d", id), 403}, {harness.Bearer(stranger), fmt.Sprintf("/api/files/%d", id), 403},
		{e.Sign, "/api/files/999", 404}, {e.Sign, "/api/files/x", 400}, {harness.Bearer("deck_nope"), fmt.Sprintf("/api/files/%d", id), 401}} {
		if resp, body := get(c.sign, c.path); resp.StatusCode != c.code || strings.Contains(body, "alert") {
			t.Fatalf("%s with a token of other rights: want %d, got %d %q", c.path, c.code, resp.StatusCode, body)
		}
	}
	files := harness.BlockData[[]map[string]any](t, e.ScreenData("", "files", nil)["b0-0-1"]["files"])
	acts, _ := files[0]["actions"].([]any)
	if len(files) != 1 || files[0]["url"] != fmt.Sprintf("/api/files/%d", id) || len(acts) != 1 {
		t.Fatalf("a file of the files of deck opens from its address and has its delete: %+v", files)
	}
	del := acts[0].(map[string]any)
	e.Must(del["command"].(string), del["params"].(map[string]any), nil)
	if left := harness.BlockData[[]map[string]any](t, e.ScreenData("", "files", nil)["b0-0-1"]["files"]); len(left) != 0 {
		t.Fatalf("a deleted file leaves the list: %+v", left)
	}
	undo := del["undo"].(map[string]any)
	e.Must(undo["command"].(string), undo["params"].(map[string]any), nil)
	if back := harness.BlockData[[]map[string]any](t, e.ScreenData("", "files", nil)["b0-0-1"]["files"]); len(back) != 1 {
		t.Fatalf("the undo brings the file back: %+v", back)
	}
}

func TestAgentGetsACopyOfItsInputFile(t *testing.T) {
	t.Parallel()
	var same, read bool
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if m := harness.InputOf.FindStringSubmatch(c.Req.Prompt); m != nil {
			var in struct{ Doc string }
			_ = json.Unmarshal([]byte(m[1]), &in)
			a, errA := os.Stat(in.Doc)
			b, errB := os.Stat(filepath.Join(c.Req.Cwd, "..", "..", "..", "files", filepath.Base(in.Doc)[:strings.Index(filepath.Base(in.Doc), "-")]))
			same = errA == nil && errB == nil && os.SameFile(a, b)
			body, _ := os.ReadFile(in.Doc)
			read = string(body) == "hello"
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}))

	e.Project("docs", harness.Manifest(`{"commands": [{"name": "look", "title": "Look", "description": "Looks at a file.", "class": "internal",
	  "input": {"type": "object", "properties": {"doc": {"type": "string", "format": "file"}}}, "result": {"type": "object"},
	  "executor": {"type": "agent", "prompt": "Look."}}]}`))
	doc := e.Upload("docs", "a.txt", "text/plain", "hello")
	e.WaitState(e.Start("docs", "look", map[string]any{"doc": fmt.Sprint(doc)}), "done")
	if !read || same {
		t.Fatalf("an agent reads its input file from a copy, never the stored file itself: read %v, same %v", read, same)
	}
}

// TestTheMasterReadsTheDataFolderOfAProject: a file or a folder of the data folder of a project is read by a command,
// as data, and nothing outside the folder is reached.
func TestTheMasterReadsTheDataFolderOfAProject(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	p := e.Project("notes", harness.PlainManifest)
	data := deckhome.Layout(e.App.Cfg.Home).Data(p)
	_ = os.MkdirAll(filepath.Join(data, "cache"), 0o700)
	_ = os.WriteFile(filepath.Join(data, "state.json"), []byte(`{"last": 7}`), 0o600)
	_ = os.WriteFile(filepath.Join(filepath.Dir(data), "secret.txt"), []byte("outside"), 0o600)
	var dir struct {
		Entries []struct {
			Name string
			Dir  bool
		}
	}
	e.Must("deck.data_read", map[string]any{"project": "notes"}, &dir)
	if len(dir.Entries) != 2 || dir.Entries[0].Name != "cache" || !dir.Entries[0].Dir || dir.Entries[1].Name != "state.json" {
		t.Fatalf("the data folder lists its entries: %+v", dir)
	}
	var file struct{ Text string }
	e.Must("deck.data_read", map[string]any{"project": "notes", "path": "state.json"}, &file)
	if !strings.Contains(file.Text, `{"last": 7}`) {
		t.Fatalf("a file of the data folder is read as text: %+v", file)
	}
	_ = os.WriteFile(filepath.Join(data, "long.txt"), []byte("a"+strings.Repeat("ж", 40000)), 0o600)
	e.Must("deck.data_read", map[string]any{"project": "notes", "path": "long.txt"}, &file)
	if !strings.Contains(file.Text, "жжж") {
		t.Fatalf("a long text cut in the middle of a letter is still text: %d", len(file.Text))
	}
	if code, out := e.Call("deck.data_read", map[string]any{"project": "notes", "path": "../secret.txt"}); code == 200 && strings.Contains(string(out), "outside") {
		t.Fatalf("nothing outside the data folder is read: %d %s", code, out)
	}
}

// TestAFolderOfTheDataFolderShowsItsSteps: the file manager shows the steps of a folder of a data folder from its
// root, each opening its folder, the last being where it is.
func TestAFolderOfTheDataFolderShowsItsSteps(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	p := e.Project("notes", harness.PlainManifest)
	_ = os.MkdirAll(filepath.Join(deckhome.Layout(e.App.Cfg.Home).Data(p), "cache", "pages"), 0o700)
	var crumbs []struct {
		Label  string
		Action map[string]any
	}
	for _, d := range e.ScreenData("", "files", map[string]any{"params": `{"path": "notes/data/cache/pages"}`, "lang": "ru"}) {
		if raw, ok := d["crumbs"]; ok {
			_ = json.Unmarshal(raw, &crumbs)
		}
	}
	var labels []string
	for _, c := range crumbs {
		labels = append(labels, c.Label)
	}
	if strings.Join(labels, "/") != "Модули/notes/Папка данных/cache/pages" || crumbs[4].Action != nil {
		t.Fatalf("the steps of the folder from the root of the files, named by the read: %+v", crumbs)
	}
	for i, want := range []string{"", "notes", "notes/data", "notes/data/cache"} {
		params, _ := crumbs[i].Action["params"].(map[string]any)
		if crumbs[i].Action["screen"] != "files" || params["path"] != want {
			t.Fatalf("a step opens its folder: %+v", crumbs[i].Action)
		}
	}
}

// TestAModuleHasACoverForEachTheme: a cover is for a theme and scheme or every theme, kept with a light copy and main
// color; the menu gives the theme's, else the other scheme's, else every theme's, else any; remove takes one theme's.
func TestAModuleHasACoverForEachTheme(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("notes", harness.PlainManifest)
	type card struct {
		Project, Cover string
		CoverDark      string `json:"cover_dark"`
		CoverColor     string `json:"cover_color"`
	}
	menu := func() card {
		var m struct{ Projects []card }
		e.Must("deck.menu", nil, &m)
		for _, p := range m.Projects {
			if p.Project == "notes" {
				return p
			}
		}
		return card{}
	}
	picture := func(c color.RGBA) string {
		img := image.NewRGBA(image.Rect(0, 0, 1600, 900))
		draw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
		var b bytes.Buffer
		_ = png.Encode(&b, img)
		return b.String()
	}
	set := func(name string, c color.RGBA, in map[string]any) int64 {
		id := e.Upload("notes", name, "image/png", picture(c))
		in["project"], in["file"] = "notes", fmt.Sprint(id)
		e.Must("deck.project_cover", in, nil)
		return id
	}
	red := set("red.png", color.RGBA{220, 30, 30, 255}, map[string]any{})
	got := menu()
	if got.Cover == "" || got.Cover == fmt.Sprintf("/api/files/%d", red) || got.CoverDark != got.Cover || !strings.HasPrefix(got.CoverColor, "#d") {
		t.Fatalf("a cover for every theme shows its light copy in both schemes, with its main color: %+v", got)
	}
	var width int
	req, _ := http.NewRequest("GET", e.URL+got.Cover, nil)
	e.Sign(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	kind := resp.Header.Get("Content-Type")
	if cfg, err := jpeg.DecodeConfig(resp.Body); err == nil {
		width = cfg.Width
	}
	resp.Body.Close()
	if kind != "image/jpeg" || width != 720 || !strings.Contains(resp.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("the light copy is a JPEG of twice a card the browser keeps: %q %d %q", kind, width, resp.Header.Get("Cache-Control"))
	}

	cur := themeLook{}
	e.Must("deck.theme", map[string]any{}, &cur)
	set("blue.png", color.RGBA{30, 30, 220, 255}, map[string]any{"theme": cur.Name, "scheme": "dark"})
	got = menu()
	if got.CoverDark != got.Cover || !strings.HasPrefix(got.CoverColor, "#1") {
		t.Fatalf("the cover of the theme in use goes first, in its scheme and in the other: %+v", got)
	}
	e.Example("pixel")
	e.Must("deck.theme_use", map[string]any{"name": "pixel"}, nil)
	if got = menu(); !strings.HasPrefix(got.CoverColor, "#d") {
		t.Fatalf("another theme shows the cover for every theme: %+v", got)
	}
	e.Refused("deck.project_cover", map[string]any{"project": "notes", "file": fmt.Sprint(red), "theme": "nowhere"}, 404, "theme")
	text := e.Upload("notes", "a.txt", "text/plain", "hello")
	e.Refused("deck.project_cover", map[string]any{"project": "notes", "file": fmt.Sprint(text)}, 422, "picture")
	e.Refused("deck.project_cover", map[string]any{"project": "notes"}, 422, "remove")

	var rows []struct {
		Theme, Scheme string
		Shown         bool
	}
	e.Must("deck.project_covers", map[string]any{"project": "notes"}, &rows)
	if len(rows) != 2 || rows[0].Theme != "" || !rows[0].Shown || rows[1].Theme != cur.Name || rows[1].Shown {
		t.Fatalf("the covers of a module by theme, and which the card shows: %+v", rows)
	}
	e.Must("deck.project_cover", map[string]any{"project": "notes", "remove": true}, nil)
	if got = menu(); !strings.HasPrefix(got.CoverColor, "#1") {
		t.Fatalf("remove takes off the covers for every theme alone; the card shows any other: %+v", got)
	}
	var files int
	var listed []struct {
		OwnerKind string `json:"owner_kind"`
	}
	e.Must("deck.files_list", map[string]any{"project": "notes"}, &listed)
	for _, f := range listed {
		if f.OwnerKind == "picture" {
			files++
		}
	}
	if files != 2 {
		t.Fatalf("a picture taken off takes its files with it: %d", files)
	}
}

// TestAReplacedCoverStaysTheCoverOfItsTheme: a cover for every theme that a new one replaces becomes the cover of the
// theme in use when it was set; one that has no place left, or a theme's own replaced, waits in the trash.
func TestAReplacedCoverStaysTheCoverOfItsTheme(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("notes", harness.PlainManifest)
	picture := func(c color.RGBA) string {
		img := image.NewRGBA(image.Rect(0, 0, 64, 36))
		draw.Draw(img, img.Bounds(), &image.Uniform{c}, image.Point{}, draw.Src)
		var b bytes.Buffer
		_ = png.Encode(&b, img)
		return b.String()
	}
	type row struct {
		Theme string
		File  int64
	}
	shown := func() []row {
		var rows []row
		e.Must("deck.project_covers", map[string]any{"project": "notes"}, &rows)
		return rows
	}
	// set gives the light copy a card shows and the picture itself.
	set := func(name string, c color.RGBA, in map[string]any) (int64, int64) {
		var out struct{ Cover int64 }
		in["project"], in["file"] = "notes", fmt.Sprint(e.Upload("notes", name, "image/png", picture(c)))
		e.Must("deck.project_cover", in, &out)
		for _, r := range shown() {
			if r.Theme == fmt.Sprint(cmp.Or(in["theme"], any(""))) {
				return r.File, out.Cover
			}
		}
		return 0, out.Cover
	}
	covers := func() string {
		var out []string
		for _, r := range shown() {
			out = append(out, fmt.Sprintf("%s=%d", r.Theme, r.File))
		}
		slices.Sort(out)
		return strings.Join(out, " ")
	}
	trashed := func() []int64 {
		var ids []int64
		// The files in the trash, which no list shows.
		rows := e.SQLRows(`SELECT id FROM file WHERE owner_kind = 'trash' ORDER BY id`)
		defer rows.Close()
		for rows.Next() {
			var id int64
			_ = rows.Scan(&id)
			ids = append(ids, id)
		}
		return ids
	}
	first := themeLook{}
	e.Must("deck.theme", map[string]any{}, &first)
	red, _ := set("red.png", color.RGBA{220, 30, 30, 255}, map[string]any{})
	e.Example("pixel")
	e.Must("deck.theme_use", map[string]any{"name": "pixel"}, nil)
	green, greenFile := set("green.png", color.RGBA{30, 220, 30, 255}, map[string]any{})
	if got, want := covers(), fmt.Sprintf("=%d %s=%d", green, first.Name, red); got != want {
		t.Fatalf("the cover replaced stays the cover of the theme it was set in: %s, want %s", got, want)
	}
	blue, blueFile := set("blue.png", color.RGBA{30, 30, 220, 255}, map[string]any{})
	if got, want := covers(), fmt.Sprintf("=%d %s=%d pixel=%d", blue, first.Name, red, green); got != want {
		t.Fatalf("each replaced cover keeps to its theme: %s, want %s", got, want)
	}
	gray, _ := set("gray.png", color.RGBA{90, 90, 90, 255}, map[string]any{})
	if got, want := covers(), fmt.Sprintf("=%d %s=%d pixel=%d", gray, first.Name, red, green); got != want || !slices.Equal(trashed(), []int64{blueFile}) {
		t.Fatalf("a replaced cover whose theme has its own waits in the trash: %s, want %s; trash %v", got, want, trashed())
	}
	set("white.png", color.RGBA{250, 250, 250, 255}, map[string]any{"theme": "pixel"})
	if !slices.Equal(trashed(), []int64{min(blueFile, greenFile), max(blueFile, greenFile)}) {
		t.Fatalf("a theme's own cover replaced waits in the trash too: %v", trashed())
	}
	e.Must("deck.file_restore", map[string]any{"file": greenFile, "owner_kind": "upload"}, nil)

	e.Project("old", harness.PlainManifest)
	old := e.Upload("old", "old.png", "image/png", picture(color.RGBA{10, 10, 10, 255}))
	// A cover a deck before covers by theme kept.
	e.SQL(`INSERT INTO picture (project_id, slot, scheme, file_id) SELECT id, 'cover', 'light', ? FROM project WHERE name = 'old'`, old)
	var out struct{ Cover int64 }
	e.Must("deck.project_cover", map[string]any{"project": "old", "file": fmt.Sprint(e.Upload("old", "new.png", "image/png", picture(color.RGBA{200, 200, 10, 255})))}, &out)
	var oldCovers []row
	e.Must("deck.project_covers", map[string]any{"project": "old"}, &oldCovers)
	if !slices.Contains(oldCovers, row{"pixel", old}) {
		t.Fatalf("a cover kept before deck knew its theme goes to the theme in use: %+v", oldCovers)
	}
}

// TestAPictureWithoutALightCopyGetsOne: a cover deck kept before light copies gets one in the sweep, transparent
// parts on white.
func TestAPictureWithoutALightCopyGetsOne(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("notes", harness.PlainManifest)
	img := image.NewRGBA(image.Rect(0, 0, 300, 200))
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	id := e.Upload("notes", "old.png", "image/png", b.String())
	// A cover a deck before light copies kept.
	e.SQL(`INSERT INTO picture (project_id, slot, scheme, file_id) SELECT id, 'cover', 'light', ? FROM project WHERE name = 'notes'`, id)
	e.App.Sweep(context.Background(), time.Now())
	var covers []struct{ File int64 }
	e.Must("deck.project_covers", map[string]any{"project": "notes"}, &covers)
	var m struct {
		Projects []struct {
			Project    string
			CoverColor string `json:"cover_color"`
		}
	}
	e.Must("deck.menu", nil, &m)
	c := ""
	for _, p := range m.Projects {
		if p.Project == "notes" {
			c = p.CoverColor
		}
	}
	if len(covers) != 1 || covers[0].File == id || covers[0].File == 0 || c != "#ffffff" {
		t.Fatalf("the sweep makes the light copy and the color: %+v %q", covers, c)
	}
}

// TestTheMasterDrawsCoversInTheMoodOfTheTheme: deck tells the master what to draw: the module, the theme's mood, the
// wish and the model, a free one by default; without a module, every module without a cover of the theme in use.
func TestTheMasterDrawsCoversInTheMoodOfTheTheme(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("notes", harness.PlainManifest)
	e.Must("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": "http://127.0.0.1:9/v1", "local": true}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "http://127.0.0.1:9/api/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "painter", "kind": "image", "call_usd": 0.04}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "media", "model": "z-image", "name": "local-image", "kind": "image", "free": true}, nil)

	var out struct{ Topic int64 }
	e.Must("deck.covers_draw", map[string]any{"project": "notes", "wish": "a fox with a notebook"}, &out)
	said := e.ChatMessages(out.Topic)
	var mood themeLook
	e.Must("deck.theme", map[string]any{}, &mood)
	if len(said) == 0 || !strings.Contains(said[0].Text, "module notes") || !strings.Contains(said[0].Text, mood.Mood) ||
		!strings.Contains(said[0].Text, "a fox with a notebook") || !strings.Contains(said[0].Text, "kind image on local-image") ||
		!strings.Contains(said[0].Text, "scheme light or dark") {
		t.Fatalf("the master is told what to draw: %+v", said)
	}

	e.Example("pixel")
	e.Must("deck.theme_use", map[string]any{"name": "pixel"}, nil)
	e.Must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
	e.Must("deck.covers_draw", map[string]any{"model": "openrouter/painter"}, &out)
	said = e.ChatMessages(out.Topic)
	if len(said) == 0 || !strings.Contains(said[0].Text, "Нарисуй обложки модулей notes, у которых их нет в теме deck «Пиксели»") ||
		!strings.Contains(said[0].Text, "тема pixel") ||
		!strings.Contains(said[0].Text, "8-bit pixel art") || !strings.Contains(said[0].Text, "вида image на openrouter/painter") {
		t.Fatalf("every cover again for the new theme, in the owner's language: %+v", said)
	}
}

// TestTheFileManagerHoldsEveryModuleInOnePlace: Files opens the folders of the modules, in each its files, documents
// and data folder, by steps of its own; the master is a folder with what it holds, and a search finds a file by name.
func TestTheFileManagerHoldsEveryModuleInOnePlace(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	p := e.Project("inbox", harness.FilesManifest())
	e.Upload("inbox", "invoice-october.pdf", "application/pdf", "%PDF-1.4")
	e.Upload("inbox", "notes.txt", "text/plain", "hello")
	data := e.App.Home.Data(p)
	if err := errors.Join(os.MkdirAll(filepath.Join(data, "cache", "empty"), 0o700), os.WriteFile(filepath.Join(data, "cache", "seen.json"), []byte("{}"), 0o600)); err != nil {
		t.Fatal(err)
	}
	type line struct {
		ID         any
		Title      string
		Meta, Icon string
		Actions    []map[string]any
	}
	type crumb struct{ Label string }
	at := func(path string) ([]line, []crumb, string) {
		b := e.ScreenBlock("", "files", "Modules", map[string]any{"lang": "ru", "params": fmt.Sprintf(`{"path": %q}`, path)})
		var ls []line
		var cs []crumb
		_ = json.Unmarshal(b["files"], &ls)
		_ = json.Unmarshal(b["crumbs"], &cs)
		var state string
		_ = json.Unmarshal(b["state"], &state)
		return ls, cs, state
	}
	root, _, _ := at("")
	i := slices.IndexFunc(root, func(l line) bool { return l.Title == "inbox" })
	m := slices.IndexFunc(root, func(l line) bool { return l.Title == "Деки" || l.Title == "master" })
	if i < 0 || !strings.Contains(root[i].Meta, "файлов: 2") || m < 0 || !strings.Contains(root[m].Meta, "Память Деки") {
		t.Fatalf("the root holds the folders of the modules, the master's says what it holds: %+v", root)
	}
	inside, crumbs, _ := at("inbox")
	names := []string{}
	for _, l := range inside {
		names = append(names, l.Title)
	}
	if !slices.Equal(names, []string{"Файлы", "Документы", "Папка данных"}) || len(crumbs) != 2 || crumbs[1].Label != "inbox" {
		t.Fatalf("a module holds its files, documents and data folder, its step named as the module: %v %+v", names, crumbs)
	}
	files, crumbs, _ := at("inbox/files")
	if len(files) != 2 || len(crumbs) != 3 || crumbs[2].Label != "Файлы" {
		t.Fatalf("its files, the steps by their names: %+v %+v", files, crumbs)
	}
	docs, _, _ := at("inbox/documents")
	d := slices.IndexFunc(docs, func(l line) bool { return l.Title == "manifest.json" })
	if d < 0 || !slices.ContainsFunc(docs[d].Actions, func(a map[string]any) bool { return a["screen"] == "document" && a["project"] == "inbox" }) {
		t.Fatalf("a document opens on its page in the module: %+v", docs)
	}
	folder, crumbs, _ := at("inbox/data/cache")
	if len(folder) != 2 || len(crumbs) != 4 || crumbs[3].Label != "cache" || slices.ContainsFunc(folder, func(l line) bool { return l.Title == ".." }) {
		t.Fatalf("the data folder by its folders, the way up by its steps alone: %+v %+v", folder, crumbs)
	}
	if _, crumbs, state := at("inbox/data/cache/empty"); state != "empty" || len(crumbs) != 5 {
		t.Fatalf("an empty folder keeps its steps back: %s %+v", state, crumbs)
	}
	id, _ := e.FindBlock("", "files", "All files")
	found := e.ScreenData("", "files", map[string]any{"filter": `{"` + id + `": {"~": "invoice oct"}}`})[id]
	var hits []line
	_ = json.Unmarshal(found["files"], &hits)
	if len(hits) != 1 || hits[0].Title != "invoice-october.pdf" {
		t.Fatalf("the search finds a file by the words of its name: %+v", hits)
	}
	var all []struct{ Name string }
	e.Must("deck.files_list", map[string]any{"search": "INVOICE pdf"}, &all)
	if len(all) != 1 || all[0].Name != "invoice-october.pdf" {
		t.Fatalf("files_list looks for the words through every file: %+v", all)
	}
	if mod := e.ScreenData("inbox", "documents", map[string]any{"params": `{"path": "files"}`})["b0"]; !strings.Contains(string(mod["files"]), "invoice-october.pdf") {
		t.Fatalf("the files of a module are the same manager inside it: %s", mod["files"])
	}
}
