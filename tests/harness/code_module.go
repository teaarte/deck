package harness

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// CodeModule is a copy of the example of the code-project skill; with a url it also takes GitLab issues from there, the way a terminal
// agent adds a tracker by the code-project skill (testdata/tracker).
func CodeModule(t *testing.T, url string) string {
	t.Helper()
	src := filepath.Join(Root, "skills/code-project/example")
	dst := filepath.Join(t.TempDir(), "code")
	err := filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dst, rel)), 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), b, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
	if url != "" {
		addTracker(t, dst, url)
	}
	return dst
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
