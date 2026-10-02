package scenarios

import (
	"context"
	deckhome "deck2/internal/home"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deck2/internal/provider"
)

// seeded makes the docs project with one record that has a photo and a document.
func seeded(e *Env) (record, photo int64) {
	e.T.Helper()
	e.saveScript("docs", "scripts/seed.py", seedScript)
	e.saveScript("docs", "scripts/read.py", readScript)
	seed := e.waitScript(e.start("docs", "seed", nil), "done", "error")
	var s struct{ Record int64 }
	if json.Unmarshal(seed.Result, &s) != nil || s.Record == 0 {
		e.T.Fatalf("seed: %+v", seed)
	}
	var files []struct{ ID int64 }
	e.must("deck.files_list", map[string]any{"record": s.Record}, &files)
	e.must("deck.document_save", map[string]any{"project": "docs", "name": "notes/a.md", "kind": "artifact", "text": "a", "record": s.Record}, nil)
	return s.Record, files[0].ID
}

func TestRecordIsReadByIDAndDeletedWithItsDocumentsAndFiles(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("docs", filesManifest())
	rec, photo := seeded(e)
	var got struct {
		ID   int64
		Data map[string]any
	}
	e.must("deck.record_get", map[string]any{"record": rec}, &got)
	if got.ID != rec || got.Data["name"] != "a" {
		t.Fatalf("record_get gives the record by its id: %+v", got)
	}
	_, tok := e.token("terminal", "deck.*")
	if code, out := e.callWith(tok, "deck.record_delete", map[string]any{"record": rec}); code != 403 || !strings.Contains(string(out), "only the owner") {
		t.Fatalf("the master does not delete records of an enabled project: %d %s", code, out)
	}
	e.must("deck.record_delete", map[string]any{"record": rec}, nil)
	if code, _ := e.call("deck.record_get", map[string]any{"record": rec}); code != 404 {
		t.Fatalf("a deleted record is gone: %d", code)
	}
	var docs []struct{ Name string }
	e.must("deck.documents_list", map[string]any{"project": "docs"}, &docs)
	for _, d := range docs {
		if d.Name == "notes/a.md" {
			t.Fatalf("the documents of a deleted record go with it: %+v", docs)
		}
	}
	if _, err := os.Stat(filepath.Join(deckhome.Layout(e.App.Cfg.Home).State(), "files", fmt.Sprint(photo))); !os.IsNotExist(err) {
		t.Fatalf("the files of a deleted record go with it: %v", err)
	}
}

func TestBuilderCleansTrialRecordsOfADraftAndUploadsAFile(t *testing.T) {
	e := newEnv(t, nil)
	_, tok := e.token("terminal", "deck.*")
	if code, out := e.callWith(tok, "deck.project_create", map[string]any{"name": "docs", "manifest": filesManifest()}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	rec, _ := seeded(e)
	if code, out := e.callWith(tok, "deck.record_delete", map[string]any{"record": rec}); code != 200 {
		t.Fatalf("the builder deletes what trial runs left in a draft: %d %s", code, out)
	}
	if code, out := e.callWith(tok, "deck.file_upload", map[string]any{"project": "docs", "name": "x.txt", "content": "not base64!"}); code != 422 {
		t.Fatalf("content must be base64: %d %s", code, out)
	}
	var f struct{ ID int64 }
	code, out := e.callWith(tok, "deck.file_upload", map[string]any{"project": "docs", "name": "note.txt",
		"content": base64.StdEncoding.EncodeToString([]byte("uploaded text"))})
	if code != 200 || json.Unmarshal(out, &f) != nil || f.ID == 0 {
		t.Fatalf("the builder uploads a sample for a trial run: %d %s", code, out)
	}
	p := e.waitScript(e.start("docs", "read", map[string]any{"doc": fmt.Sprint(f.ID)}), "done", "error")
	var res struct{ Checks map[string]bool }
	if json.Unmarshal(p.Result, &res) != nil || !res.Checks["doc_read"] {
		t.Fatalf("the uploaded file is the input of a command: %+v", p)
	}
}

func TestAgentReadsARecordByIDOnlyInItsProjectOrItsReads(t *testing.T) {
	var rec int64
	var got []string
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		out, _ := c.Tool(ctx, "record_get", map[string]any{"record": rec})
		got = append(got, out)
		return provider.Result{Structured: json.RawMessage(`null`)}
	})
	e.createProject("docs", filesManifest())
	rec, _ = seeded(e)
	for i, m := range []string{plainManifest, strings.Replace(plainManifest, `"screens": []`, `"screens": [], "reads": [{"project": "docs", "collections": ["items"]}]`, 1)} {
		p := e.createProject(fmt.Sprintf("p%d", i), m)
		e.waitState(e.run(p, "work"), "done", "error")
	}
	if len(got) != 2 || !strings.Contains(got[0], "no record") || !strings.Contains(got[1], `"name":"a"`) {
		t.Fatalf("an agent reads a record of another project only by its reads: %q", got)
	}
}
