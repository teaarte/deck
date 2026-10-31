package scenarios

import (
	"archive/zip"
	"bytes"
	"deck2/internal/store"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"
)

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
		{"name": "memory/owner-tea.md", "kind": "memory", "text": fact("owner-tea", "user", "The owner likes tea")},
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

	id := upload(e, "", "links.zip", "application/zip", string(body))
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
	rev := manifestRevision(e, "links-shared")
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
	if project(e, "links-shared").Status != store.ProjectEnabled {
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
		id := upload(e, "", "evil.zip", "application/zip", pack(map[string]string{"manifest.json": `{"version": 13}`, evil: "x"}))
		e.Refused("deck.project_import", map[string]any{"file": strconv.FormatInt(id, 10)}, 422, "leaves the module folder")
	}
	id := upload(e, "", "plain.zip", "application/zip", pack(map[string]string{"manifest.json": `{"version": 13}`, ".hidden/x.md": "x", "__MACOSX/._manifest.json": "x"}))
	var im struct{ Name string }
	e.Must("deck.project_import", map[string]any{"file": strconv.FormatInt(id, 10)}, &im)
	if im.Name != "plain" {
		t.Fatalf("a .zip with the module at its root is named after the file: %+v", im)
	}
	e.Refused("deck.project_import", map[string]any{}, 422, "folder, file or starter")
	e.Refused("deck.project_import", map[string]any{"file": "not-a-zip"}, 404, "no uploaded file")
}
