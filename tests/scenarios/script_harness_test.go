package scenarios

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// scriptManifest is a manifest with one python script command; extra goes into the manifest object.
func scriptManifest(command, result, extra string) string {
	if result == "" {
		result = `{"type": "object"}`
	}
	return fmt.Sprintf(`{"version": 11,
  "collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}, "size": {"type": "integer"}}, "required": ["name"]}}],
  "commands": [
    {"name": %q, "title": "Run", "description": "Runs the script.", "class": "internal",
     "input": {"type": "object", "properties": {"n": {"type": "integer"}, "project": {"type": "string"}, "allowed": {"type": "string"},
       "other": {"type": "string"}, "outside": {"type": "string"}, "secret": {"type": "string"}, "key": {"type": "string"},
       "socket": {"type": "string"}, "method": {"type": "string"}, "form": {"type": "string"}, "type": {"type": "string"}, "use": {"type": "string"}}},
     "result": %s, "executor": {"type": "script", "script": "main"}},
    {"name": "publish", "title": "Publish", "description": "Publishes outside.", "class": "external",
     "executor": {"type": "agent", "prompt": "Publish."}}],
  "scripts": [{"name": "main", "file": "scripts/main.py", "runtime": "python"}]%s}`, command, result, extra)
}

func (e *Env) saveScript(project, name, text string) {
	e.T.Helper()
	e.must("deck.document_save", map[string]any{"project": project, "name": name, "kind": "script", "text": text}, nil)
}

// start runs a command of a project with input and returns its process.
func (e *Env) start(project, command string, input map[string]any) int64 {
	e.T.Helper()
	if input == nil {
		input = map[string]any{}
	}
	var r struct{ Process int64 }
	e.must(project+"."+command, input, &r)
	return r.Process
}

type healthRow struct {
	Subject string `json:"subject"`
	Status  string `json:"status"`
	Error   string `json:"error"`
	Version string `json:"version"`
}

// waitHealth waits until the health of subject is status, or any status for "", and returns it.
func (e *Env) waitHealth(subject, status string) healthRow {
	e.T.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		var hs []healthRow
		e.must("deck.health_list", nil, &hs)
		for _, h := range hs {
			if h.Subject == subject && (status == "" || h.Status == status) {
				return h
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	e.T.Fatalf("health of %s did not become %s", subject, status)
	return healthRow{}
}

func (e *Env) waitScript(id int64, states ...string) proc {
	e.T.Helper()
	return e.pollState(id, 2*time.Minute, 50*time.Millisecond, states...)
}

func (e *Env) warnings(contains string) int {
	e.T.Helper()
	n := 0
	for _, f := range e.notes() {
		if f.Author == "deck" && !f.Read && strings.Contains(f.Text, contains) {
			n++
		}
	}
	return n
}

// feedNote is a note of the owner's feed.
type feedNote struct {
	ID           int64
	Author, Text string
	Read         bool
	CanRead      bool `json:"can_read"`
}

// notes are the notes of the owner's feed, newest first.
func (e *Env) notes() []feedNote {
	e.T.Helper()
	var out []feedNote
	e.must("deck.feed_list", map[string]any{"kind": "note"}, &out)
	return out
}

// waitNote waits for an unread note of deck that holds contains.
func (e *Env) waitNote(contains string) feedNote {
	e.T.Helper()
	var got feedNote
	e.waitFor("a note of deck with "+contains, func() bool {
		for _, f := range e.notes() {
			if f.Author == "deck" && !f.Read && strings.Contains(f.Text, contains) {
				got = f
				return true
			}
		}
		return false
	})
	return got
}

// wheel builds a python package and returns the path of the wheel.
func wheel(t *testing.T, pkg string) string {
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

// pythonLib is the part of test scripts that calls deck and fetches through the proxy.
const pythonLib = `import json, os, sys, urllib.request, urllib.error
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
