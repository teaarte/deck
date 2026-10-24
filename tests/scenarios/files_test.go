package scenarios

import (
	"bytes"
	"context"
	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"encoding/json"
	"fmt"
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

// upload sends a file to deck as a form would and returns its id.
func (e *Env) upload(project, name, ctype, content string) int64 {
	e.T.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="file"; filename=%q`, name)}
	h["Content-Type"] = []string{ctype}
	w, _ := mw.CreatePart(h)
	_, _ = io.WriteString(w, content)
	_ = mw.Close()
	req, _ := http.NewRequest("POST", e.URL+"/api/files?project="+project, &buf)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.T.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	var files []struct{ ID int64 }
	if resp.StatusCode != 200 || json.Unmarshal(out, &files) != nil || len(files) != 1 {
		e.T.Fatalf("upload: %d %s", resp.StatusCode, out)
	}
	return files[0].ID
}

func filesManifest() string {
	return `{"version": 13,
  "collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}],
  "commands": [
    {"name": "seed", "title": "Seed", "description": "Makes a record with a file.", "class": "internal",
     "result": {"type": "object"}, "executor": {"type": "script", "script": "seed"}},
    {"name": "read", "title": "Read", "description": "Reads the files of its input.", "class": "internal",
     "input": {"type": "object", "properties": {"doc": {"type": "string", "format": "file"}, "photo": {"type": "string", "format": "file"},
       "rec": {"type": "integer", "format": "record"}}},
     "result": {"type": "object"}, "executor": {"type": "script", "script": "read"}}],
  "scripts": [{"name": "seed", "file": "scripts/seed.py", "runtime": "python"},
    {"name": "read", "file": "scripts/read.py", "runtime": "python"}]}`
}

const seedScript = pythonLib + `
st, out = deck("deck.records_upsert", {"collection": "items", "records": json.dumps({"name": "a"})})
with open(os.path.join(os.environ["DECK_RESULTS"], "photo.jpg"), "w") as f:
    f.write("record photo")
print(json.dumps({"record": out["ids"][0], "files": [{"path": "photo.jpg", "record": out["ids"][0]}]}))
`

const readScript = pythonLib + `
inp = json.load(sys.stdin)
doc, photo = inp["doc"], inp.get("photo", "")
checks = {
    "doc_read": open(doc).read() == "uploaded text",
    "doc_not_writable": not can_write(doc),
    "record_file_read": photo == "" or open(photo).read() == "record photo",
    "record_file_not_writable": photo == "" or not can_write(photo),
    "input_folder_not_writable": not can_write(os.path.join(os.path.dirname(doc), "new.txt")),
}
files = [{"file": int(os.path.basename(doc).split("-")[0]), "record": inp["rec"]}] if inp.get("rec") else []
print(json.dumps({"checks": checks, "files": files}))
`

func TestUploadedAndRecordFilesReachTheScriptReadOnly(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("docs", filesManifest())
	e.saveScript("docs", "scripts/seed.py", seedScript)
	e.saveScript("docs", "scripts/read.py", readScript)
	seed := e.waitScript(e.start("docs", "seed", nil), "done", "error")
	var s struct{ Record int64 }
	if json.Unmarshal(seed.Result, &s) != nil || s.Record == 0 {
		t.Fatalf("seed: %+v", seed)
	}
	doc := e.upload("docs", "note.txt", "text/plain", "uploaded text")
	var photos []struct{ ID int64 }
	e.must("deck.files_list", map[string]any{"record": s.Record}, &photos)
	if len(photos) != 1 {
		t.Fatalf("the seed record has its photo: %+v", photos)
	}

	p := e.waitScript(e.start("docs", "read", map[string]any{"doc": fmt.Sprint(doc), "photo": fmt.Sprint(photos[0].ID), "rec": s.Record}), "done", "error")
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
	e.must("deck.files_list", map[string]any{"record": s.Record}, &files)
	if len(files) != 2 || files[1].ID != doc {
		t.Fatalf("the uploaded file the result names now belongs to the record: %+v", files)
	}

	other := e.createProject("other", filesManifest())
	_ = other
	code, body := e.call("docs.read", map[string]any{"doc": fmt.Sprint(e.upload("other", "x.txt", "text/plain", "x")), "rec": s.Record})
	if code == 200 || !strings.Contains(string(body), "no file") {
		t.Fatalf("a file of another project is not an input: %d %s", code, body)
	}
}

func TestUploadsNeedTheProjectAndGoWhenNoCommandTookThem(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, nil)
	e.createProject("docs", filesManifest())
	e.saveScript("docs", "scripts/read.py", "import sys; print('{}')")
	_, narrow := e.token("narrow", "docs.read")
	_, wide := e.token("wide", "docs.*")
	form := func(tok string) int {
		var buf bytes.Buffer
		mw := multipart.NewWriter(&buf)
		w, _ := mw.CreateFormFile("file", "a.txt")
		_, _ = io.WriteString(w, "a")
		_ = mw.Close()
		req, _ := http.NewRequest("POST", e.URL+"/api/files?project=docs", &buf)
		req.Header.Set("Authorization", "Bearer "+tok)
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
	kept := e.upload("docs", "kept.txt", "text/plain", "k")
	e.waitScript(e.start("docs", "read", map[string]any{"doc": fmt.Sprint(kept)}), "done", "error")
	e.App.Sweep(context.Background(), e.Clock.Add(25*time.Hour))
	var uploads, all int
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM file WHERE owner_kind = 'upload'`).Scan(&uploads)
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM file`).Scan(&all)
	if uploads != 0 || all != 1 {
		t.Fatalf("an upload no command took is gone after a day, the one a process took stays: %d uploads, %d files", uploads, all)
	}
}

func TestResultFilesACrashLeftMidMoveAreNeverShownAndGoAfterADay(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, nil)
	e.createProject("keep", scriptManifest("take", "", ""))
	e.saveScript("keep", "scripts/main.py", `import json, os
open(os.path.join(os.environ["DECK_RESULTS"], "a.txt"), "w").write("a")
print(json.dumps({"files": [{"path": "a.txt"}]}))
`)
	id := e.start("keep", "take", nil)
	e.waitScript(id, "done", "error")
	var kept []struct{ ID int64 }
	e.must("deck.files_list", map[string]any{"process": id}, &kept)
	if len(kept) != 1 {
		t.Fatalf("a result file is kept: %+v", kept)
	}
	res, err := e.App.Store.DB.Exec(`INSERT INTO file (project_id, owner_kind, owner_id, name, created_at)
		SELECT project_id, 'keeping', id, 'b.txt', ? FROM process WHERE id = ?`, e.Clock.Now().UTC().Format(time.RFC3339Nano), id)
	if err != nil {
		t.Fatal(err)
	}
	left, _ := res.LastInsertId()
	path := filepath.Join(deckhome.Layout(e.App.Cfg.Home).State(), "files", fmt.Sprint(left))
	_ = os.WriteFile(path, []byte("b"), 0o600)
	var shown []struct{ ID int64 }
	e.must("deck.files_list", map[string]any{"process": id}, &shown)
	if len(shown) != 1 || shown[0].ID != kept[0].ID {
		t.Fatalf("a file still moving belongs to no one: %+v", shown)
	}
	e.App.Sweep(context.Background(), e.Clock.Add(25*time.Hour))
	e.drained()
	var n int
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM file WHERE id = ?`, left).Scan(&n)
	if _, err := os.Stat(path); n != 0 || err == nil {
		t.Fatalf("the daily sweep drops what a crash left mid-move: row %d, file %v", n, err)
	}
}

func TestAResultWithAMissingFileHandsOverNoInputFileEither(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("docs", filesManifest())
	e.saveScript("docs", "scripts/seed.py", seedScript)
	e.saveScript("docs", "scripts/read.py", `import json, os, sys
inp = json.load(sys.stdin)
given = int(os.path.basename(inp["doc"]).split("-")[0])
print(json.dumps({"files": [{"file": given, "record": inp["rec"]}, {"path": "missing.txt", "record": inp["rec"]}]}))
`)
	seed := e.waitScript(e.start("docs", "seed", nil), "done", "error")
	var s struct{ Record int64 }
	_ = json.Unmarshal(seed.Result, &s)
	doc := e.upload("docs", "note.txt", "text/plain", "uploaded text")
	id := e.start("docs", "read", map[string]any{"doc": fmt.Sprint(doc), "rec": s.Record})
	if p := e.waitScript(id, "done", "error"); p.State != "error" || !strings.Contains(p.Error, "missing.txt") {
		t.Fatalf("a result naming a file that is not there fails: %+v", p)
	}
	var onRecord []struct{ ID int64 }
	e.must("deck.files_list", map[string]any{"record": s.Record}, &onRecord)
	if slices.ContainsFunc(onRecord, func(f struct{ ID int64 }) bool { return f.ID == doc }) {
		t.Fatalf("the input file stays with the process when the result fails: %+v", onRecord)
	}
}

// TestAFileDownloadsAsAnAttachmentForWhoMaySeeItsProject: a file of the journal downloads as an attachment
// that never renders as a page of deck; a token that may not call every command of its project gets nothing.
func TestAFileDownloadsAsAnAttachmentForWhoMaySeeItsProject(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("docs", filesManifest())
	e.createProject("other", filesManifest())
	id := e.upload("docs", "отчёт <1>.html", "text/html", "<script>alert(1)</script>")
	_, narrow := e.token("narrow", "docs.read")
	_, wide := e.token("wide", "docs.*")
	_, stranger := e.token("stranger", "other.*")
	get := func(tok, path string) (*http.Response, string) {
		req, _ := http.NewRequest("GET", e.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp, string(b)
	}
	resp, body := get(token, fmt.Sprintf("/api/files/%d", id))
	_, params, _ := mime.ParseMediaType(resp.Header.Get("Content-Disposition"))
	if resp.StatusCode != 200 || body != "<script>alert(1)</script>" || params["filename"] != "отчёт <1>.html" ||
		!strings.HasPrefix(resp.Header.Get("Content-Disposition"), "attachment") || resp.Header.Get("X-Content-Type-Options") != "nosniff" ||
		resp.Header.Get("Content-Security-Policy") != "sandbox" || resp.Header.Get("Content-Type") != "text/html" {
		t.Fatalf("the owner downloads the file as an attachment in a sandbox: %d %q %v", resp.StatusCode, body, resp.Header)
	}
	if resp, _ := get(wide, fmt.Sprintf("/api/files/%d", id)); resp.StatusCode != 200 {
		t.Fatalf("a token for the whole project downloads its file: %d", resp.StatusCode)
	}
	for _, c := range []struct {
		tok, path string
		code      int
	}{{narrow, fmt.Sprintf("/api/files/%d", id), 403}, {stranger, fmt.Sprintf("/api/files/%d", id), 403},
		{token, "/api/files/999", 404}, {token, "/api/files/x", 400}, {"deck_nope", fmt.Sprintf("/api/files/%d", id), 401}} {
		if resp, body := get(c.tok, c.path); resp.StatusCode != c.code || strings.Contains(body, "alert") {
			t.Fatalf("%s with a token of other rights: want %d, got %d %q", c.path, c.code, resp.StatusCode, body)
		}
	}
	files := blockData[[]map[string]any](t, e.screenData("", "files", nil)["b0-0-1"]["files"])
	acts, _ := files[0]["actions"].([]any)
	if len(files) != 1 || files[0]["url"] != fmt.Sprintf("/api/files/%d", id) || len(acts) != 1 {
		t.Fatalf("a file of the files of deck opens from its address and has its delete: %+v", files)
	}
	del := acts[0].(map[string]any)
	e.must(del["command"].(string), del["params"].(map[string]any), nil)
	if left := blockData[[]map[string]any](t, e.screenData("", "files", nil)["b0-0-1"]["files"]); len(left) != 0 {
		t.Fatalf("a deleted file leaves the list: %+v", left)
	}
	undo := del["undo"].(map[string]any)
	e.must(undo["command"].(string), undo["params"].(map[string]any), nil)
	if back := blockData[[]map[string]any](t, e.screenData("", "files", nil)["b0-0-1"]["files"]); len(back) != 1 {
		t.Fatalf("the undo brings the file back: %+v", back)
	}
}

func TestAgentGetsACopyOfItsInputFile(t *testing.T) {
	t.Parallel()
	var same, read bool
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if m := inputOf.FindStringSubmatch(c.Req.Prompt); m != nil {
			var in struct{ Doc string }
			_ = json.Unmarshal([]byte(m[1]), &in)
			a, errA := os.Stat(in.Doc)
			b, errB := os.Stat(filepath.Join(c.Req.Cwd, "..", "..", "..", "files", filepath.Base(in.Doc)[:strings.Index(filepath.Base(in.Doc), "-")]))
			same = errA == nil && errB == nil && os.SameFile(a, b)
			body, _ := os.ReadFile(in.Doc)
			read = string(body) == "hello"
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	e.createProject("docs", `{"version": 13, "commands": [{"name": "look", "title": "Look", "description": "Looks at a file.", "class": "internal",
	  "input": {"type": "object", "properties": {"doc": {"type": "string", "format": "file"}}}, "result": {"type": "object"},
	  "executor": {"type": "agent", "prompt": "Look."}}]}`)
	doc := e.upload("docs", "a.txt", "text/plain", "hello")
	e.waitState(e.start("docs", "look", map[string]any{"doc": fmt.Sprint(doc)}), "done")
	if !read || same {
		t.Fatalf("an agent reads its input file from a copy, never the stored file itself: read %v, same %v", read, same)
	}
}

// TestTheMasterReadsTheDataFolderOfAProject: a file or a folder of the data folder of a project is read by a command,
// as data, and nothing outside the folder is reached.
func TestTheMasterReadsTheDataFolderOfAProject(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	p := e.createProject("notes", plainManifest)
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
	e.must("deck.data_read", map[string]any{"project": "notes"}, &dir)
	if len(dir.Entries) != 2 || dir.Entries[0].Name != "cache" || !dir.Entries[0].Dir || dir.Entries[1].Name != "state.json" {
		t.Fatalf("the data folder lists its entries: %+v", dir)
	}
	var file struct{ Text string }
	e.must("deck.data_read", map[string]any{"project": "notes", "path": "state.json"}, &file)
	if !strings.Contains(file.Text, `{"last": 7}`) {
		t.Fatalf("a file of the data folder is read as text: %+v", file)
	}
	_ = os.WriteFile(filepath.Join(data, "long.txt"), []byte("a"+strings.Repeat("ж", 40000)), 0o600)
	e.must("deck.data_read", map[string]any{"project": "notes", "path": "long.txt"}, &file)
	if !strings.Contains(file.Text, "жжж") {
		t.Fatalf("a long text cut in the middle of a letter is still text: %d", len(file.Text))
	}
	if code, out := e.call("deck.data_read", map[string]any{"project": "notes", "path": "../secret.txt"}); code == 200 && strings.Contains(string(out), "outside") {
		t.Fatalf("nothing outside the data folder is read: %d %s", code, out)
	}
}

// TestAFolderOfTheDataFolderShowsItsSteps: the file manager of a data folder shows the steps from its root, each
// opening its folder, the last being where it is.
func TestAFolderOfTheDataFolderShowsItsSteps(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	p := e.createProject("notes", plainManifest)
	_ = os.MkdirAll(filepath.Join(deckhome.Layout(e.App.Cfg.Home).Data(p), "cache", "pages"), 0o700)
	var crumbs []struct {
		Label  string
		Action map[string]any
	}
	for _, d := range e.screenData("", "data", map[string]any{"params": `{"project": "notes", "path": "cache/pages"}`, "lang": "ru"}) {
		if raw, ok := d["crumbs"]; ok {
			_ = json.Unmarshal(raw, &crumbs)
		}
	}
	if len(crumbs) != 3 || crumbs[0].Label != "Папка данных" || crumbs[1].Label != "cache" || crumbs[2].Label != "pages" || crumbs[2].Action != nil {
		t.Fatalf("the steps of the folder from its root: %+v", crumbs)
	}
	for i, want := range []string{"", "cache"} {
		params, _ := crumbs[i].Action["params"].(map[string]any)
		if crumbs[i].Action["screen"] != "data" || params["path"] != want || params["project"] != "notes" {
			t.Fatalf("a step opens its folder: %+v", crumbs[i].Action)
		}
	}
}

// TestAModuleHasACoverForItsCard: a picture the owner gives is the cover of a module's card, the menu names where it
// is read, another one replaces it, a file that is no picture is refused, and without a file it goes.
func TestAModuleHasACoverForItsCard(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("notes", plainManifest)
	cover := func() string {
		var m struct {
			Projects []struct{ Project, Cover string }
		}
		e.must("deck.menu", nil, &m)
		for _, p := range m.Projects {
			if p.Project == "notes" {
				return p.Cover
			}
		}
		return ""
	}
	png := e.upload("notes", "cover.png", "image/png", "\x89PNG\r\n\x1a\n")
	e.must("deck.project_cover", map[string]any{"project": "notes", "file": fmt.Sprint(png)}, nil)
	first := cover()
	if !strings.HasPrefix(first, "/api/files/") {
		t.Fatalf("the menu names where the cover of a module is read: %q", first)
	}
	again := e.upload("", "other.png", "image/png", "\x89PNG\r\n\x1a\n")
	e.must("deck.project_cover", map[string]any{"project": "notes", "file": fmt.Sprint(again)}, nil)
	var n int
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM file WHERE owner_kind = 'cover'`).Scan(&n)
	if now := cover(); now == first || now == "" || n != 1 {
		t.Fatalf("a new cover replaces the old one: %q %q %d", first, now, n)
	}
	text := e.upload("notes", "a.txt", "text/plain", "hello")
	e.refused("deck.project_cover", map[string]any{"project": "notes", "file": fmt.Sprint(text)}, 422, "picture")
	e.must("deck.project_cover", map[string]any{"project": "notes"}, nil)
	if cover() != "" {
		t.Fatal("without a file the cover goes")
	}
}
