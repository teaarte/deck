package scenarios

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	deckdocs "deck2/internal/docs"
	"deck2/internal/store"
)

func TestAModuleExportedByTheSkillImportsAsADraft(t *testing.T) {
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
	var up struct{ IDs []int64 }
	e.must("deck.records_upsert", map[string]any{"project": "links", "collection": "sources", "records": `{"url": "https://a.example"}`}, &up)
	e.must("deck.document_save", map[string]any{"project": "links", "record": up.IDs[0], "name": "notes.md", "kind": "doc", "text": "data of a record"}, nil)
	_, tok := e.token("terminal", "deck.*")
	s, err := e.terminal(tok)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "links-copy")
	exportLikeTheSkill(t, s, "links", dir)
	var files []string
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(dir, path)
			files = append(files, filepath.ToSlash(rel))
		}
		return nil
	})
	want := []string{"custom/chart.html", "manifest.json", "prompt.md", "prompts/collect.md", "scripts/parse.py", "skills/reader/SKILL.md"}
	if !slices.Equal(files, want) {
		t.Fatalf("an export by the skill writes the module, not its data: %v", files)
	}

	_ = os.WriteFile(filepath.Join(dir, ".DS_Store"), []byte{0, 1}, 0o644)
	var im struct {
		Name, Status string
		Rights       []string
	}
	out, failed := use(s, "project_import", map[string]any{"folder": dir})
	if failed || json.Unmarshal([]byte(out), &im) != nil {
		t.Fatalf("the terminal agent imports the folder: %s", out)
	}
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
	_ = os.WriteFile(filepath.Join(bad, deckdocs.ManifestName), []byte(`{"version": 7, "commands": [{"name": "x"}]}`), 0o644)
	if code, out := e.call("deck.project_import", map[string]any{"folder": bad, "name": "bad"}); code != 422 || !strings.Contains(string(out), "manifest") {
		t.Fatalf("an invalid manifest is refused with the reason: %d %s", code, out)
	}
	_ = os.WriteFile(filepath.Join(bad, deckdocs.ManifestName), []byte(`{"version": 7}`), 0o644)
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
	e.projectsOf(&ps)
	if len(ps) != 2 {
		t.Fatalf("a refused import leaves no project behind: %+v", ps)
	}
	if code, _ := e.call("deck.project_import", map[string]any{"folder": "relative/path"}); code != 422 {
		t.Fatalf("folders are absolute paths: %d", code)
	}
}

func TestAgentImportsClaudeSkillsWithDocumentSave(t *testing.T) {
	e := newEnv(t, ok)
	_, tok := e.token("terminal", "deck.*")
	s, err := e.terminal(tok)
	if err != nil {
		t.Fatal(err)
	}
	if res := s.InitializeResult(); res == nil || !strings.Contains(res.Instructions, "importing the owner's Claude Code skills") {
		t.Fatalf("the MCP server points the terminal agent at the skill: %+v", res)
	}
	save := func(name, text string) (string, bool) {
		return use(s, "document_save", map[string]any{"name": name, "kind": "skill", "text": text})
	}
	if out, failed := save("skills/voice/SKILL.md", "---\nname: voice\ndescription: Writes like the owner.\n---\nShort sentences.\n"); failed {
		t.Fatalf("a skill is saved as a workspace skill: %s", out)
	}
	if out, failed := save("skills/voice/examples/one.md", "An example."); failed {
		t.Fatalf("the other files of the folder too: %s", out)
	}
	if out, failed := save("skills/greedy/SKILL.md", "---\nname: greedy\ndescription: x\nallowed-tools: Bash\n---\n"); !failed || !strings.Contains(out, "allowed-tools") {
		t.Fatalf("a skill that grants tools does not come in: %s", out)
	}
	m := strings.Replace(plainManifest, `"prompt": "Do the work."}`, `"prompt": "Do the work.", "skills": ["voice"]}`, 1)
	p := e.createProject("plain", m)
	e.waitState(e.run(p, "work"), "done")
	if got := e.Fake.Calls()[0].Skills; len(got) != 2 || got["voice/examples/one.md"] != "An example." {
		t.Fatalf("an imported workspace skill reaches the agents that list it: %v", got)
	}
	var doc struct{ Text string }
	e.must("deck.document_get", map[string]any{"name": "skills/module-builder/SKILL.md"}, &doc)
	if !strings.Contains(doc.Text, "name: module-builder") || !strings.Contains(doc.Text, "Import the owner's Claude Code skills") {
		t.Fatalf("the built-in skill tells how to import skills: %.200s", doc.Text)
	}
}

func TestImportTakesNoLinks(t *testing.T) {
	e := newEnv(t, ok)
	_, tok := e.token("terminal", "deck.*")
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, deckdocs.ManifestName), []byte(`{"version": 7}`), 0o644)
	secret := filepath.Join(t.TempDir(), "secret.txt")
	_ = os.WriteFile(secret, []byte("owner's secret"), 0o600)
	_ = os.Symlink(secret, filepath.Join(dir, "notes.md"))
	if code, out := e.callWith(tok, "deck.project_import", map[string]any{"folder": dir, "name": "linked"}); code != 422 || !strings.Contains(string(out), "notes.md: a symbolic link") {
		t.Fatalf("an import does not follow links out of its folder: %d %s", code, out)
	}
}

// exportLikeTheSkill writes a module folder the way the module-building skill tells an agent to.
func exportLikeTheSkill(t *testing.T, s *mcp.ClientSession, project, dir string) {
	t.Helper()
	out, failed := use(s, "documents_list", map[string]any{"project": project})
	var docs []docView
	if failed || json.Unmarshal([]byte(out), &docs) != nil {
		t.Fatalf("documents_list: %s", out)
	}
	for _, d := range docs {
		if d.OwnerKind != "" || !slices.Contains([]string{"manifest", "prompt", "script", "skill", "custom", "doc"}, d.Kind) {
			continue
		}
		out, _ := use(s, "document_get", map[string]any{"document": d.ID})
		var full docView
		_ = json.Unmarshal([]byte(out), &full)
		path := filepath.Join(dir, filepath.FromSlash(d.Name))
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte(full.Text), 0o644)
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
