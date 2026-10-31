package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

// ProjectsOf lists the projects other than the built-in one into out.
func (e *Env) ProjectsOf(out any) {
	e.T.Helper()
	var all []map[string]any
	e.Must("deck.projects_list", nil, &all)
	mine := []map[string]any{}
	for _, p := range all {
		if p["builtin"] != true {
			mine = append(mine, p)
		}
	}
	b, _ := json.Marshal(mine)
	if err := json.Unmarshal(b, out); err != nil {
		e.T.Fatal(err)
	}
}

// addTracker adds the GitLab intake of testdata/tracker to a module folder.
func addTracker(t *testing.T, dir, url string) {
	t.Helper()
	var m, in map[string]any
	for path, v := range map[string]*map[string]any{filepath.Join(dir, "manifest.json"): &m, Testdata("tracker/intake.json"): &in} {
		b, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal([]byte(strings.ReplaceAll(string(b), "https://gitlab.com", url)), v); err != nil {
			t.Fatal(err)
		}
	}
	for _, k := range []string{"scripts", "triggers"} {
		m[k] = in[k]
	}
	m["collections"] = append(m["collections"].([]any), in["collections"].([]any)...)
	m["tools"] = in["tools"]
	m["commands"] = append(m["commands"].([]any), in["commands"].([]any)...)
	m["screens"] = append(m["screens"].([]any), in["screen"])
	m["menu"] = append(m["menu"].([]any), "intake")
	b, _ := json.Marshal(m)
	files := map[string][]byte{"manifest.json": b}
	for _, f := range []string{"poll_gitlab.py", "follow_gitlab.py"} {
		src, err := os.ReadFile(filepath.Join(Testdata("tracker"), f))
		if err != nil {
			t.Fatal(err)
		}
		files[filepath.Join("scripts", f)] = src
	}
	for name, b := range files {
		if err := errors.Join(os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o700), os.WriteFile(filepath.Join(dir, name), b, 0o600)); err != nil {
			t.Fatal(err)
		}
	}
}

func (e *Env) ImportEnabled(dir, name string) int64 {
	e.T.Helper()
	var p struct{ ID int64 }
	e.Must("deck.project_import", map[string]any{"folder": dir, "name": name}, &p)
	var m struct{ Revision int64 }
	e.Must("deck.manifest_get", map[string]any{"project": name}, &m)
	e.Must("deck.project_enable", map[string]any{"project": name, "revision": m.Revision}, nil)
	e.Names[p.ID] = name
	return p.ID
}

// PostManifest has an agent command and an HTTP tool of the given extra fields that is not allowed yet.
func PostManifest(url, extra string) string {
	return Manifest(fmt.Sprintf(`{"commands": [{"name": "work", "title": "Work", "description": "Works with the service.", "class": "internal",
    "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Post the note."}}],
  "tools": {"http": [{"name": "post", "description": "Posts a note", "method": "POST", "url": "%s/items/{id}",
    "headers": {"X-Key": "$secret.svc"}, "body": {"text": "{text}", "n": "{n}"}%s,
    "input": {"type": "object", "properties": {"id": {"type": "string"}, "text": {"type": "string"}, "n": {"type": "integer"}},
      "required": ["id", "text"]}}]}}`, url, extra))
}

type ProjectV struct {
	Name     string
	Status   string
	Rights   []string
	Asks     []string
	Revision int64
}

func (e *Env) ProjectRow(name string) ProjectV {
	e.T.Helper()
	var ps []ProjectV
	e.Must("deck.projects_list", nil, &ps)
	for _, p := range ps {
		if p.Name == name {
			return p
		}
	}
	e.T.Fatalf("no project %s in %+v", name, ps)
	return ProjectV{}
}

func (e *Env) ManifestRevision(project string) int64 {
	e.T.Helper()
	var cur struct{ Revision int64 }
	e.Must("deck.manifest_get", map[string]any{"project": project}, &cur)
	return cur.Revision
}

type RecordRow struct {
	ID   int64          `json:"id"`
	Data map[string]any `json:"data"`
}

type CommandView struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Input       json.RawMessage `json:"input"`
	Class       string          `json:"class"`
}

// Commands are what the owner may call, each as command_describe tells it.
func (e *Env) Commands() []CommandView {
	e.T.Helper()
	var rows []struct{ Name string }
	e.Must("deck.commands_list", nil, &rows)
	out := make([]CommandView, 0, len(rows))
	for _, r := range rows {
		var c CommandView
		e.Must("deck.command_describe", map[string]any{"command": r.Name}, &c)
		out = append(out, c)
	}
	return out
}

type StarterView struct {
	Name, Title, Icon, Description, Status string
	Revision                               int64
}

// Clip writes a clip and gives its record.
func (e *Env) Clip(slug string) int64 {
	e.T.Helper()
	e.Must("deck.records_upsert", map[string]any{"project": "clips", "collection": "clips", "records": Jsonl(map[string]any{"slug": slug})}, nil)
	var all []struct {
		ID  int64
		Key string
	}
	e.Must("deck.records_list", map[string]any{"project": "clips", "collection": "clips"}, &all)
	for _, r := range all {
		if r.Key == slug {
			return r.ID
		}
	}
	e.T.Fatalf("no clip %s", slug)
	return 0
}
