package scenarios

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"deck2/internal/app"
	"deck2/internal/store"
)

func TestProjectExportAndImportAsADraft(t *testing.T) {
	e := newEnv(t, ok)
	e.createProject("links", fullManifest)
	docs := []map[string]any{
		{"name": "prompt.md", "kind": "prompt", "text": "# Collects links\nMore."},
		{"name": "scripts/parse.py", "kind": "script", "text": "print('{}')\n"},
		{"name": "prompts/collect.md", "kind": "doc", "text": "Visit each source."},
		{"name": "skills/reader/SKILL.md", "kind": "skill", "text": "---\nname: reader\ndescription: Reads.\n---\nRead.\n"},
		{"name": "custom/chart.html", "kind": "custom", "text": "<div></div>"},
		{"name": "reports/today.md", "kind": "artifact", "text": "an artifact is data"},
	}
	for _, d := range docs {
		d["project"] = "links"
		e.must("deck.document_save", d, nil)
	}
	dir := filepath.Join(t.TempDir(), "links-copy")
	var ex struct{ Files []string }
	e.must("deck.project_export", map[string]any{"project": "links", "folder": dir}, &ex)
	want := []string{"custom/chart.html", "manifest.json", "prompt.md", "prompts/collect.md", "scripts/parse.py", "skills/reader/SKILL.md"}
	if !slices.Equal(ex.Files, want) {
		t.Fatalf("export writes the module, not its data: %v", ex.Files)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "prompts", "collect.md")); string(b) != "Visit each source." {
		t.Fatalf("files carry the current text: %q", b)
	}
	if code, _ := e.call("deck.project_export", map[string]any{"project": "links", "folder": dir}); code != 422 {
		t.Fatalf("export does not write into a folder that is not empty: %d", code)
	}

	_ = os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte{0, 1}, 0o644)
	var im struct {
		Name, Status string
		Rights       []string
	}
	e.must("deck.project_import", map[string]any{"folder": dir}, &im)
	if im.Name != "links-copy" || im.Status != store.ProjectDraft || !slices.Contains(im.Rights, "allowed external: post_link") {
		t.Fatalf("import creates a draft named after the folder and shows its rights: %+v", im)
	}
	var list []docView
	e.must("deck.documents_list", map[string]any{"project": "links-copy"}, &list)
	kinds := map[string]string{}
	for _, d := range list {
		kinds[d.Name] = d.Kind
	}
	for _, d := range docs[:5] {
		if kinds[d["name"].(string)] != d["kind"] {
			t.Fatalf("import restores %s as %s: %v", d["name"], d["kind"], kinds)
		}
	}
	if len(kinds) != 6 {
		t.Fatalf("import takes the module files and nothing hidden: %v", kinds)
	}

	bad := t.TempDir()
	_ = os.WriteFile(filepath.Join(bad, app.ManifestName), []byte(`{"version": 4, "commands": [{"name": "x"}]}`), 0o644)
	if code, out := e.call("deck.project_import", map[string]any{"folder": bad, "name": "bad"}); code != 422 || !strings.Contains(string(out), "manifest") {
		t.Fatalf("an invalid manifest is refused with the reason: %d %s", code, out)
	}
	_ = os.WriteFile(filepath.Join(bad, app.ManifestName), []byte(`{"version": 4}`), 0o644)
	_ = os.WriteFile(filepath.Join(bad, "logo.png"), []byte{0x89, 'P', 'N', 'G', 0xff, 0xfe}, 0o644)
	if code, out := e.call("deck.project_import", map[string]any{"folder": bad, "name": "bad"}); code != 422 || !strings.Contains(string(out), "logo.png: not text") {
		t.Fatalf("a folder with a file deck cannot take is refused: %d %s", code, out)
	}
	_ = os.Remove(filepath.Join(bad, "logo.png"))
	_ = os.WriteFile(filepath.Join(bad, "_init.py"), []byte("x"), 0o644)
	if code, out := e.call("deck.project_import", map[string]any{"folder": bad, "name": "bad"}); code != 422 || !strings.Contains(string(out), "_init.py") {
		t.Fatalf("a file name deck cannot store is refused: %d %s", code, out)
	}
	var ps []projectV
	e.must("deck.projects_list", nil, &ps)
	if len(ps) != 2 {
		t.Fatalf("a refused import leaves no project behind: %+v", ps)
	}
	if code, _ := e.call("deck.project_import", map[string]any{"folder": "relative/path"}); code != 422 {
		t.Fatalf("folders are absolute paths: %d", code)
	}
}

func TestSkillsImportFromClaudeCode(t *testing.T) {
	src := t.TempDir()
	write := func(path, text string) {
		_ = os.MkdirAll(filepath.Dir(filepath.Join(src, path)), 0o755)
		_ = os.WriteFile(filepath.Join(src, path), []byte(text), 0o644)
	}
	write("voice/SKILL.md", "---\nname: voice\ndescription: Writes like the owner.\n---\nShort sentences.\n")
	write("voice/examples/one.md", "An example.")
	write("voice/.git/HEAD", "ref")
	write("empty/README.md", "no skill here")
	write("misnamed/SKILL.md", "---\nname: other\ndescription: x\n---\n")
	e := newEnv(t, ok, func(c *app.Config) { c.ClaudeSkills = src })

	var got struct{ Imported, Skipped []string }
	e.must("deck.skills_import", map[string]any{}, &got)
	if !slices.Equal(got.Imported, []string{"voice"}) || len(got.Skipped) != 2 ||
		!strings.Contains(strings.Join(got.Skipped, "|"), "empty: no SKILL.md") || !strings.Contains(strings.Join(got.Skipped, "|"), "misnamed") {
		t.Fatalf("skills with a proper SKILL.md are imported, the rest listed: %+v", got)
	}
	var doc struct {
		ID   int64
		Text string
	}
	e.must("deck.document_get", map[string]any{"name": "skills/voice/examples/one.md"}, &doc)
	if doc.Text != "An example." {
		t.Fatalf("every file of the skill folder is imported: %+v", doc)
	}
	e.must("deck.skills_import", map[string]any{"names": []string{"voice"}}, &got)
	var revs []any
	e.must("deck.document_revisions", map[string]any{"document": doc.ID}, &revs)
	if len(revs) != 1 {
		t.Fatalf("an unchanged skill gets no new revision: %v", revs)
	}

	e.must("deck.document_get", map[string]any{"name": "skills/module-builder/SKILL.md"}, &doc)
	if !strings.Contains(doc.Text, "name: module-builder") || !strings.Contains(doc.Text, "cursor") {
		t.Fatalf("the module building skill is built into deck: %.200s", doc.Text)
	}
}

func TestExportIsTheOwnersAndImportTakesNoLinks(t *testing.T) {
	e := newEnv(t, ok)
	e.createProject("links", plainManifest)
	_, tok := e.token("terminal", "deck.*")
	if code, _ := e.callWith(tok, "deck.project_export", map[string]any{"project": "links", "folder": filepath.Join(t.TempDir(), "x")}); code != 403 {
		t.Fatalf("only the owner writes folders outside deck: %d", code)
	}
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, app.ManifestName), []byte(`{"version": 4}`), 0o644)
	secret := filepath.Join(t.TempDir(), "secret.txt")
	_ = os.WriteFile(secret, []byte("owner's secret"), 0o600)
	_ = os.Symlink(secret, filepath.Join(dir, "notes.md"))
	if code, out := e.callWith(tok, "deck.project_import", map[string]any{"folder": dir, "name": "linked"}); code != 422 || !strings.Contains(string(out), "notes.md: a symbolic link") {
		t.Fatalf("an import does not follow links out of its folder: %d %s", code, out)
	}
}

func TestBuiltInSkillsReachEveryWorkspace(t *testing.T) {
	home := t.TempDir()
	first := newEnvWith(t, home, &Fake{script: ok})
	if _, err := first.App.Store.DB.Exec(`INSERT INTO workspace (id, name) VALUES (2, 'other')`); err != nil {
		t.Fatal(err)
	}
	_ = first.App.Close()
	e := newEnvWith(t, home, &Fake{script: ok})
	var n int
	err := e.App.Store.DB.QueryRow(`SELECT COUNT(*) FROM document WHERE workspace_id = 2 AND name = 'skills/module-builder/SKILL.md'`).Scan(&n)
	if err != nil || n != 1 {
		t.Fatalf("the built-in skill is stored for every workspace: %d %v", n, err)
	}
}

// TestModuleAModelBuiltImportsAsADraft keeps the module of the step 7 rehearsal importable.
func TestModuleAModelBuiltImportsAsADraft(t *testing.T) {
	e := newEnv(t, ok)
	dir, _ := filepath.Abs("../../examples/vacancies")
	var im struct{ Name, Status string }
	e.must("deck.project_import", map[string]any{"folder": dir}, &im)
	var cmds []struct{ Name string }
	code, out := e.do("GET", "/api/commands", nil)
	_ = json.Unmarshal(out, &cmds)
	n := 0
	for _, c := range cmds {
		if strings.HasPrefix(c.Name, im.Name+".") {
			n++
		}
	}
	if code != 200 || im.Status != store.ProjectDraft || n == 0 {
		t.Fatalf("the rehearsal module imports as a draft with its commands: %+v, %d commands", im, n)
	}
}
