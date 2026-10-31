package modules

import (
	"archive/zip"
	"bytes"
	deckdocs "deck2/internal/docs"
	"deck2/internal/store"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAModuleExportedByTheSkillImportsAsADraft(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("links", fullManifest)
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
		e.Must("deck.document_save", d, nil)
	}
	var up struct{ IDs []int64 }
	e.Must("deck.records_upsert", map[string]any{"project": "links", "collection": "sources", "records": `{"url": "https://a.example"}`}, &up)
	e.Must("deck.document_save", map[string]any{"project": "links", "record": up.IDs[0], "name": "notes.md", "kind": "doc", "text": "data of a record"}, nil)
	_, tok := e.Token("terminal", "deck.*")
	s, err := e.Terminal(tok)
	if err != nil {
		t.Fatal(err)
	}
	modules := t.TempDir()
	e.Must("deck.folder_add", map[string]any{"project": "links", "name": "modules", "path": modules}, nil)
	dir := filepath.Join(modules, "links-copy")
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
	out, failed := harness.Use(s, "project_import", map[string]any{"folder": dir})
	if failed || json.Unmarshal([]byte(out), &im) != nil {
		t.Fatalf("the terminal agent imports the folder: %s", out)
	}
	if im.Name != "links-copy" || im.Status != store.ProjectDraft || !slices.Contains(im.Rights, "allowed external: post_link") {
		t.Fatalf("import creates a draft named after the folder and shows its rights: %+v", im)
	}
	var list []docView
	e.Must("deck.documents_list", map[string]any{"project": "links-copy"}, &list)
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
	_ = os.WriteFile(filepath.Join(bad, deckdocs.ManifestName), []byte(harness.Manifest(`{"commands": [{"name": "x"}]}`)), 0o644)
	if code, out := e.Call("deck.project_import", map[string]any{"folder": bad, "name": "bad"}); code != 422 || !strings.Contains(string(out), "manifest") {
		t.Fatalf("an invalid manifest is refused with the reason: %d %s", code, out)
	}
	_ = os.WriteFile(filepath.Join(bad, deckdocs.ManifestName), []byte(harness.Manifest(`{}`)), 0o644)
	_ = os.WriteFile(filepath.Join(bad, "logo.png"), []byte{0x89, 'P', 'N', 'G', 0xff, 0xfe}, 0o644)
	if code, out := e.Call("deck.project_import", map[string]any{"folder": bad, "name": "bad"}); code != 422 || !strings.Contains(string(out), "logo.png: not text") {
		t.Fatalf("a folder with a file deck cannot take is refused: %d %s", code, out)
	}
	_ = os.Remove(filepath.Join(bad, "logo.png"))
	_ = os.WriteFile(filepath.Join(bad, "_init.py"), []byte("x"), 0o644)
	if code, out := e.Call("deck.project_import", map[string]any{"folder": bad, "name": "bad"}); code != 422 || !strings.Contains(string(out), "_init.py") {
		t.Fatalf("a file name deck cannot store is refused: %d %s", code, out)
	}
	var ps []harness.ProjectV
	e.ProjectsOf(&ps)
	if len(ps) != 2 {
		t.Fatalf("a refused import leaves no project behind: %+v", ps)
	}
	if code, _ := e.Call("deck.project_import", map[string]any{"folder": "relative/path"}); code != 422 {
		t.Fatalf("folders are absolute paths: %d", code)
	}
}

func TestAgentImportsClaudeSkillsWithDocumentSave(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	_, tok := e.Token("terminal", "deck.*")
	s, err := e.Terminal(tok)
	if err != nil {
		t.Fatal(err)
	}
	if res := s.InitializeResult(); res == nil || !strings.Contains(res.Instructions, "importing the owner's Claude Code skills") {
		t.Fatalf("the MCP server points the terminal agent at the skill: %+v", res)
	}
	save := func(name, text string) (string, bool) {
		return harness.Use(s, "document_save", map[string]any{"name": name, "kind": "skill", "text": text})
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
	m := strings.Replace(harness.PlainManifest, `"prompt": "Do the work."}`, `"prompt": "Do the work.", "skills": ["voice"]}`, 1)
	p := e.Project("plain", m)
	e.WaitState(e.Run(p, "work"), "done")
	if got := e.Fake.Calls()[0].Skills; len(got) != 2 || got["voice/examples/one.md"] != "An example." {
		t.Fatalf("an imported workspace skill reaches the agents that list it: %v", got)
	}
	var doc struct{ Text string }
	e.Must("deck.document_get", map[string]any{"name": "skills/module-builder/SKILL.md"}, &doc)
	if !strings.Contains(doc.Text, "name: module-builder") || !strings.Contains(doc.Text, "Import the owner's Claude Code skills") {
		t.Fatalf("the built-in skill tells how to import skills: %.200s", doc.Text)
	}
}

func TestImportTakesNoLinks(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, deckdocs.ManifestName), []byte(harness.Manifest(`{}`)), 0o644)
	secret := filepath.Join(t.TempDir(), "secret.txt")
	_ = os.WriteFile(secret, []byte("owner's secret"), 0o600)
	_ = os.Symlink(secret, filepath.Join(dir, "notes.md"))
	if code, out := e.Call("deck.project_import", map[string]any{"folder": dir, "name": "linked"}); code != 422 || !strings.Contains(string(out), "notes.md: a symbolic link") {
		t.Fatalf("an import does not follow links out of its folder: %d %s", code, out)
	}
}

// exportLikeTheSkill writes a module folder the way the module-building skill tells an agent to.
func exportLikeTheSkill(t *testing.T, s *mcp.ClientSession, project, dir string) {
	t.Helper()
	out, failed := harness.Use(s, "documents_list", map[string]any{"project": project})
	var docs []docView
	if failed || json.Unmarshal([]byte(out), &docs) != nil {
		t.Fatalf("documents_list: %s", out)
	}
	for _, d := range docs {
		if d.OwnerKind != "" || !slices.Contains([]string{"manifest", "prompt", "script", "skill", "custom", "doc"}, d.Kind) {
			continue
		}
		out, _ := harness.Use(s, "document_get", map[string]any{"document": d.ID})
		var full docView
		_ = json.Unmarshal([]byte(out), &full)
		path := filepath.Join(dir, filepath.FromSlash(d.Name))
		_ = os.MkdirAll(filepath.Dir(path), 0o755)
		_ = os.WriteFile(path, []byte(full.Text), 0o644)
	}
}

func TestBuiltInSkillsReachEveryWorkspace(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	first := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{Script: harness.Ok}))
	if _, err := first.App.Store.DB.Exec(`INSERT INTO workspace (id, name) VALUES (2, 'other')`); err != nil {
		t.Fatal(err)
	}
	_ = first.App.Close()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{Script: harness.Ok}))
	var n int
	err := e.App.Store.DB.QueryRow(`SELECT COUNT(*) FROM document WHERE workspace_id = 2 AND name = 'skills/module-builder/SKILL.md'`).Scan(&n)
	if err != nil || n != 1 {
		t.Fatalf("the built-in skill is stored for every workspace: %d %v", n, err)
	}
}

func TestAModuleExportedToAZipImportsAsADraftWithWhatItMayDo(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("links", fullManifest)
	for _, d := range []map[string]any{
		{"name": "prompt.md", "kind": "prompt", "text": "# Collects links\nMore."},
		{"name": "scripts/parse.py", "kind": "script", "text": "print('{}')\n"},
		{"name": "prompts/collect.md", "kind": "doc", "text": "Visit each source."},
		{"name": "skills/reader/SKILL.md", "kind": "skill", "text": "---\nname: reader\ndescription: Reads.\n---\nRead.\n"},
		{"name": "custom/chart.html", "kind": "custom", "text": "<div></div>"},
		{"name": "reports/today.md", "kind": "artifact", "text": "an artifact is data"},
		{"name": "memory/owner-tea.md", "kind": "memory", "text": harness.Fact("owner-tea", "user", "The owner likes tea")},
	} {
		d["project"] = "links"
		e.Must("deck.document_save", d, nil)
	}
	var up struct{ IDs []int64 }
	e.Must("deck.records_upsert", map[string]any{"project": "links", "collection": "sources", "records": `{"url": "https://a.example"}`}, &up)
	e.Must("deck.document_save", map[string]any{"project": "links", "record": up.IDs[0], "name": "notes.md", "kind": "doc", "text": "data of a record"}, nil)
	e.Must("deck.secret_set", map[string]any{"name": "feed", "value": "s3cret-feed-value"}, nil)

	a := e.RowAction("", "settings", "deck.project_export", map[string]any{"project": "links"})
	if a == nil || a["download"] != true {
		t.Fatalf("a module's row in Settings exports it to a file the browser downloads: %v", a)
	}
	code, out := e.Press(a)
	var ex struct {
		File  int64
		Name  string
		Files int
	}
	if code != 200 || json.Unmarshal(out, &ex) != nil || ex.File == 0 || ex.Name != "links.zip" {
		t.Fatalf("the export names the file it kept: %d %s", code, out)
	}
	code, body := e.Do("GET", "/api/files/"+strconv.FormatInt(ex.File, 10), nil)
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if code != 200 || err != nil {
		t.Fatalf("the owner downloads the .zip: %d %v", code, err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	slices.Sort(names)
	want := []string{"links/custom/chart.html", "links/manifest.json", "links/prompt.md", "links/prompts/collect.md", "links/scripts/parse.py", "links/skills/reader/SKILL.md"}
	if !slices.Equal(names, want) || ex.Files != len(want) {
		t.Fatalf("the .zip holds the module in its folder, not its artifacts, memory or records: %v", names)
	}
	if bytes.Contains(body, []byte("s3cret-feed-value")) {
		t.Fatal("no secret leaves deck in an export")
	}

	id := e.Upload("", "links.zip", "application/zip", string(body))
	var im struct {
		Name, Status string
		Rights       []string
	}
	e.Must("deck.project_import", map[string]any{"file": strconv.FormatInt(id, 10), "name": "links-shared"}, &im)
	if im.Name != "links-shared" || im.Status != store.ProjectDraft || !slices.Contains(im.Rights, "allowed external: post_link") {
		t.Fatalf("an uploaded .zip from a screen of deck comes in as a draft with its rights: %+v", im)
	}
	var list []docView
	e.Must("deck.documents_list", map[string]any{"project": "links-shared"}, &list)
	if len(list) != len(want) {
		t.Fatalf("import takes every file of the .zip, out of its folder: %+v", list)
	}

	var rights []struct{ What, Detail string }
	e.Must("deck.project_rights", map[string]any{"project": "links-shared"}, &rights)
	if !slices.Contains(rights, struct{ What, Detail string }{"allowed external", "post_link"}) ||
		!slices.Contains(rights, struct{ What, Detail string }{"read domain", "news.example.com"}) {
		t.Fatalf("what a draft may do is a row each: %+v", rights)
	}
	if b, _ := json.Marshal(e.ScreenData("", "settings", nil)); !strings.Contains(string(b), `"params":{"project":"links-shared","revision":`) ||
		!strings.Contains(string(b), `"screen":"draft"`) {
		t.Fatalf("a draft is enabled from the page of what it may do: %s", b)
	}
	rev := e.ManifestRevision("links-shared")
	data := e.ScreenData("", "draft", map[string]any{"params": fmt.Sprintf(`{"project": "links-shared", "revision": "%d"}`, rev)})
	b, _ := json.Marshal(data)
	if !strings.Contains(string(b), "post_link") {
		t.Fatalf("the page of a draft lists what it may do: %s", b)
	}
	msgs, _ := json.Marshal(e.ScreenMessages("", "draft", nil))
	if !strings.Contains(string(msgs), "Install modules only from authors you know") {
		t.Fatalf("under the warning not to install modules of unknown authors: %s", msgs)
	}
	enable := fmt.Sprintf(`{"command":"deck.project_enable","params":{"project":"links-shared","revision":%d},"primary":true,"verb":"enable"}`, rev)
	if !strings.Contains(string(b), enable) {
		t.Fatalf("its button enables the revision the owner read: %s", b)
	}
	e.Must("deck.project_enable", map[string]any{"project": "links-shared", "revision": rev}, nil)
	if e.ProjectRow("links-shared").Status != store.ProjectEnabled {
		t.Fatal("the draft is enabled")
	}
}

func TestAZipThatLeavesItsFolderIsRefused(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	pack := func(files map[string]string) string {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for name, text := range files {
			w, _ := zw.Create(name)
			_, _ = w.Write([]byte(text))
		}
		_ = zw.Close()
		return buf.String()
	}
	for _, evil := range []string{"../evil.md", "a/../../evil.md", "/etc/evil.md"} {
		id := e.Upload("", "evil.zip", "application/zip", pack(map[string]string{"manifest.json": harness.Manifest(`{}`), evil: "x"}))
		e.Refused("deck.project_import", map[string]any{"file": strconv.FormatInt(id, 10)}, 422, "leaves the module folder")
	}
	id := e.Upload("", "plain.zip", "application/zip", pack(map[string]string{"manifest.json": harness.Manifest(`{}`), ".hidden/x.md": "x", "__MACOSX/._manifest.json": "x"}))
	var im struct{ Name string }
	e.Must("deck.project_import", map[string]any{"file": strconv.FormatInt(id, 10)}, &im)
	if im.Name != "plain" {
		t.Fatalf("a .zip with the module at its root is named after the file: %+v", im)
	}
	e.Refused("deck.project_import", map[string]any{}, 422, "folder, file or starter")
	e.Refused("deck.project_import", map[string]any{"file": "not-a-zip"}, 404, "no uploaded file")
}
