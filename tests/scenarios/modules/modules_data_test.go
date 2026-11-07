package modules

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// TestAModuleTravelsWithItsDataAndItsDataIsReset: an export with data takes records, memory and data folder, never
// secrets, and an import brings them back; Reset the data empties the module after a snapshot, its manifest stays.
func TestAModuleTravelsWithItsDataAndItsDataIsReset(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("links", fullManifest)
	e.Must("deck.document_save", map[string]any{"project": "links", "name": "memory/owner-tea.md", "kind": "memory", "text": harness.Fact("owner-tea", "user", "The owner likes tea")}, nil)
	e.Must("deck.record_upsert", map[string]any{"project": "links", "collection": "sources", "records": `{"url": "https://a.example"}`}, nil)
	e.Secret("links", "feed", "s3cret-feed-value")
	data := e.App.Home.Data(e.ProjectID("links"))
	if err := os.MkdirAll(filepath.Join(data, "cache"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "cache", "page.bin"), []byte{0, 1, 2, 3}, 0o600); err != nil {
		t.Fatal(err)
	}

	export := e.RowAction("", "settings", "deck.project_export", map[string]any{"project": "links", "data": "true"})
	if export == nil || export["download"] != true {
		t.Fatalf("Settings → Modules exports a module with its data: %v", export)
	}
	var ex struct {
		File int64
		Name string
	}
	if code, out := e.Press(export); code != 200 || json.Unmarshal(out, &ex) != nil || ex.Name != "links-with-data.zip" {
		t.Fatalf("the export with data names its file: %d %s", code, out)
	}
	_, body := e.Do("GET", "/api/files/"+strconv.FormatInt(ex.File, 10), nil)
	zr, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	for _, want := range []string{"links/manifest.json", "links/.deck-data/records/sources.jsonl", "links/.deck-data/memory/owner-tea.md", "links/.deck-data/data/cache/page.bin"} {
		if !slices.Contains(names, want) {
			t.Fatalf("the .zip with data has %s: %v", want, names)
		}
	}
	if bytes.Contains(body, []byte("s3cret-feed-value")) {
		t.Fatal("no secret leaves deck with the data")
	}

	id := e.Upload("", "links-with-data.zip", "application/zip", string(body))
	var im struct {
		ID   int64
		Name string
		Data int
	}
	e.Must("deck.project_import", map[string]any{"file": strconv.FormatInt(id, 10), "name": "links-copy"}, &im)
	if im.Data == 0 {
		t.Fatalf("the import says what of the data came back: %+v", im)
	}
	var recs []map[string]any
	e.Must("deck.records_list", map[string]any{"project": "links-copy", "collection": "sources"}, &recs)
	if len(recs) != 1 || !strings.Contains(fmtJSON(recs[0]), "a.example") {
		t.Fatalf("the import brings the records back: %+v", recs)
	}
	if b, err := os.ReadFile(filepath.Join(e.App.Home.Data(im.ID), "cache", "page.bin")); err != nil || !bytes.Equal(b, []byte{0, 1, 2, 3}) {
		t.Fatalf("the import brings the data folder back: %v %v", b, err)
	}
	var doc struct{ Text string }
	e.Must("deck.document_get", map[string]any{"project": "links-copy", "name": "memory/owner-tea.md"}, &doc)
	if !strings.Contains(doc.Text, "likes tea") {
		t.Fatalf("the import brings the memory back: %+v", doc)
	}

	// Records of a collection the manifest does not have do not come in.
	var bad bytes.Buffer
	zw := zip.NewWriter(&bad)
	for name, text := range map[string]string{"x/manifest.json": manifestOf(t, e, "links"), "x/.deck-data/records/strangers.jsonl": `{"key": "a", "data": {"a": 1}}` + "\n"} {
		w, _ := zw.Create(name)
		_, _ = w.Write([]byte(text))
	}
	_ = zw.Close()
	bid := e.Upload("", "x.zip", "application/zip", bad.String())
	if code, out := e.Call("deck.project_import", map[string]any{"file": strconv.FormatInt(bid, 10), "name": "strangers"}); code < 400 || !strings.Contains(string(out), "strangers") {
		t.Fatalf("records of a collection the manifest lacks are refused: %d %s", code, out)
	}

	reset := e.RowAction("links", "settings", "deck.project_reset", map[string]any{"project": "links"})
	if reset == nil || reset["confirm"] != true {
		t.Fatalf("the settings of the module reset its data after the owner says yes: %v", reset)
	}
	var rs struct {
		Records  int64
		Snapshot string
	}
	e.Must("deck.project_reset", map[string]any{"project": "links"}, &rs)
	if rs.Records != 1 || rs.Snapshot == "" {
		t.Fatalf("a reset deletes the records after a snapshot: %+v", rs)
	}
	e.Must("deck.records_list", map[string]any{"project": "links", "collection": "sources"}, &recs)
	if es, _ := os.ReadDir(data); len(recs) != 0 || len(es) != 0 {
		t.Fatalf("a reset empties the records and the data folder: %+v %v", recs, es)
	}
	if e.ManifestRevision("links") == 0 {
		t.Fatal("a reset keeps the manifest")
	}
}

func fmtJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// TestADraftIsEnabledFromOneCard: the page of a draft is one card that says what it does, what it may do once enabled,
// what it still lacks, with Enable and the way to set its secrets.
func TestADraftIsEnabledFromOneCard(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("links", fullManifest)
	_, body := exportOf(t, e, "links")
	id := e.Upload("", "links.zip", "application/zip", string(body))
	e.Must("deck.project_import", map[string]any{"file": strconv.FormatInt(id, 10), "name": "draft-links"}, nil)
	card := e.Screen("", "draft", map[string]any{"params": `{"project": "draft-links"}`})["b0"]
	var v struct {
		Fields  []struct{ Label, Value string }
		Actions []map[string]any
	}
	_ = json.Unmarshal(card["value"], &v)
	text := fmtJSON(v.Fields)
	if !strings.Contains(text, "post_link") || !strings.Contains(text, "feed") || !harness.HasVerb(v.Actions, "enable") ||
		!slices.ContainsFunc(v.Actions, func(a map[string]any) bool { return a["label"] == "Set the secrets" }) {
		t.Fatalf("one card says what the draft may do and lacks, with Enable and its secrets: %s %v", text, v.Actions)
	}
	var menu struct {
		Projects []struct {
			Project string
			Draft   bool
		}
	}
	e.Must("deck.menu_get", nil, &menu)
	if !slices.ContainsFunc(menu.Projects, func(p struct {
		Project string
		Draft   bool
	}) bool {
		return p.Project == "draft-links" && p.Draft
	}) {
		t.Fatalf("the card of a draft on the tiles knows it is one: %+v", menu.Projects)
	}
}

// exportOf is the .zip of a module's export, its file and body.
func exportOf(t *testing.T, e *harness.Env, project string) (int64, []byte) {
	t.Helper()
	var ex struct{ File int64 }
	e.Must("deck.project_export", map[string]any{"project": project}, &ex)
	_, body := e.Do("GET", "/api/files/"+strconv.FormatInt(ex.File, 10), nil)
	return ex.File, body
}

// manifestOf is the current manifest of a project as its export holds it.
func manifestOf(t *testing.T, e *harness.Env, project string) string {
	t.Helper()
	var d struct{ Text string }
	e.Must("deck.document_get", map[string]any{"project": project, "name": "manifest.json"}, &d)
	return d.Text
}
