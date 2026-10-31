package harness

import (
	"archive/zip"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ScriptManifest is a manifest with one python script command; extra goes into the manifest object.
func ScriptManifest(command, result, extra string) string {
	if result == "" {
		result = `{"type": "object"}`
	}
	return fmt.Sprintf(`{"version": 13,
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

func (e *Env) SaveScript(project, name, text string) {
	e.T.Helper()
	e.Must("deck.document_save", map[string]any{"project": project, "name": name, "kind": "script", "text": text}, nil)
}

// HealthRow is a row of deck's health.
type HealthRow struct {
	Subject string `json:"subject"`
	Status  string `json:"status"`
	Error   string `json:"error"`
	Version string `json:"version"`
}

// Health is deck's health, of a project when one is named.
func (e *Env) Health(project ...string) []HealthRow {
	e.T.Helper()
	in := map[string]any{}
	if len(project) > 0 {
		in["project"] = project[0]
	}
	var hs []HealthRow
	e.Must("deck.health_list", in, &hs)
	return hs
}

// WaitHealth waits until the health of subject is status, or any status for "", and returns it.
func (e *Env) WaitHealth(subject, status string) HealthRow {
	e.T.Helper()
	var got HealthRow
	e.Until("health of "+subject+" to be "+status, func() bool {
		for _, h := range e.Health() {
			if h.Subject == subject && (status == "" || h.Status == status) {
				got = h
				return true
			}
		}
		return false
	})
	return got
}

func (e *Env) Warnings(contains string) int {
	e.T.Helper()
	n := 0
	for _, f := range e.Notes() {
		if f.Author == "deck" && !f.Read && strings.Contains(f.Text, contains) {
			n++
		}
	}
	return n
}

// feedNote is a note of the owner's feed.
type Note struct {
	ID           int64
	Author, Text string
	CreatedAt    string `json:"created_at"`
	Read         bool
	CanRead      bool `json:"can_read"`
}

// notes are the notes of the owner's feed, newest first.
func (e *Env) Notes() []Note {
	e.T.Helper()
	var out []Note
	e.Must("deck.feed_list", map[string]any{"kind": "note"}, &out)
	return out
}

// waitNote waits for an unread note of deck that holds contains.
func (e *Env) WaitNote(contains string) Note {
	e.T.Helper()
	var got Note
	e.Until("a note of deck with "+contains, func() bool {
		for _, f := range e.Notes() {
			if f.Author == "deck" && !f.Read && strings.Contains(f.Text, contains) {
				got = f
				return true
			}
		}
		return false
	})
	return got
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
