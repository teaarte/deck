package harness

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// Upload sends a file to deck as a form would and returns its id.
func (e *Env) Upload(project, name, ctype, content string) int64 {
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
	e.Sign(req)
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

const SeedScript = PythonLib + `
st, out = deck("deck.records_upsert", {"collection": "items", "records": json.dumps({"name": "a"})})
with open(os.path.join(os.environ["DECK_RESULTS"], "photo.jpg"), "w") as f:
    f.write("record photo")
print(json.dumps({"record": out["ids"][0], "files": [{"path": "photo.jpg", "record": out["ids"][0]}]}))
`

const ReadScript = PythonLib + `
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

// RepoFolder is a folder that looks like a git repo to deck, by its real path.
func RepoFolder(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "My Repo")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

func (e *Env) SaveScript(project, name, text string) {
	e.T.Helper()
	e.Must("deck.document_save", map[string]any{"project": project, "name": name, "kind": "script", "text": text}, nil)
}

// Wheel builds a python package and returns the path of the wheel.
func Wheel(t *testing.T, pkg string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), pkg+"-0.1-py3-none-any.whl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	info := pkg + "-0.1.dist-info/"
	files := map[string]string{
		pkg + "/__init__.py": "VALUE = 42\n",
		info + "METADATA":    "Metadata-Version: 2.1\nName: " + pkg + "\nVersion: 0.1\n",
		info + "WHEEL":       "Wheel-Version: 1.0\nGenerator: deck-test\nRoot-Is-Purelib: true\nTag: py3-none-any\n",
	}
	record := ""
	for name, text := range files {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(text))
		record += name + ",,\n"
	}
	w, _ := zw.Create(info + "RECORD")
	_, _ = w.Write([]byte(record + info + "RECORD,,\n"))
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()
	return path
}

// PythonLib is the part of test scripts that calls deck and fetches through the proxy.
const PythonLib = `import json, os, sys, urllib.request, urllib.error
URL, TOKEN = os.environ["DECK_URL"], os.environ["DECK_TOKEN"]

def deck(name, body):
    req = urllib.request.Request(URL + "/commands/" + name, data=json.dumps(body).encode(), method="POST",
        headers={"Authorization": "Bearer " + TOKEN, "Content-Type": "application/json"})
    try:
        with urllib.request.urlopen(req, timeout=20) as r:
            return r.status, json.load(r)
    except urllib.error.HTTPError as e:
        return e.code, e.read().decode()

def fetch(url, proxy=True):
    if proxy is True:
        proxy = os.environ["HTTP_PROXY"]
    handler = urllib.request.ProxyHandler({"http": proxy, "https": proxy} if proxy else {})
    try:
        with urllib.request.build_opener(handler).open(url, timeout=10) as r:
            return r.status
    except urllib.error.HTTPError as e:
        return e.code
    except Exception as e:
        return str(e)

def can_write(path):
    try:
        with open(path, "w") as f:
            f.write("x")
        return True
    except OSError:
        return False

def can_read(path):
    try:
        with open(path) as f:
            f.read()
        return True
    except OSError:
        return False
`

func Served(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("hello")) }))
	t.Cleanup(s.Close)
	return s
}

// OwnerHome is a home of the owner outside the temp folders, which the Linux sandbox empties anyway.
func OwnerHome(t *testing.T) string {
	t.Helper()
	cache, err := os.UserCacheDir()
	if err == nil {
		_ = os.MkdirAll(cache, 0o700)
	}
	dir, err := os.MkdirTemp(cache, "deck2-owner-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

const ClipsRender = `import json, sys, time
inp = json.load(sys.stdin)
time.sleep(inp.get("wait", 0))
print(json.dumps({"n": inp.get("n", 0), "record": inp.get("record")}))
`
