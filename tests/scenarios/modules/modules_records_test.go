package modules

import (
	"context"
	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/store/db"
	"deck2/tests/harness"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCommandWritesRecordsAndScreenShowsThem(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		harness.WriteIdeas(ctx, c, "x")
		return provider.Result{Structured: json.RawMessage(`{"written": 3}`)}
	}))

	p := e.Project("ideas", harness.IdeasManifest)
	id := e.Run(p, "collect")
	got := e.WaitState(id, "done")
	if got.NotBySchema || string(got.Result) != `{"written":3}` {
		t.Fatalf("unexpected result %+v", got)
	}
	rows := e.ScreenRows(p, "main")
	if len(rows) != 2 || rows[0].Cells[0] != "gamma" || rows[1].Cells[0] != "alpha" {
		t.Fatalf("want gamma, alpha filtered and sorted, got %v", rows)
	}
	e.WaitState(e.Run(p, "collect"), "done")
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "ideas"})
	if len(recs) != 3 {
		t.Fatalf("upsert by key must not duplicate, got %d records", len(recs))
	}
}

// seeded makes the docs project with one record that has a photo and a document.
func seeded(e *harness.Env) (record, photo int64) {
	e.T.Helper()
	e.SaveScript("docs", "scripts/seed.py", harness.SeedScript)
	e.SaveScript("docs", "scripts/read.py", harness.ReadScript)
	seed := e.WaitState(e.Start("docs", "seed", nil), "done", "error")
	var s struct{ Record int64 }
	if json.Unmarshal(seed.Result, &s) != nil || s.Record == 0 {
		e.T.Fatalf("seed: %+v", seed)
	}
	var files []struct{ ID int64 }
	e.Must("deck.files_list", map[string]any{"record": s.Record}, &files)
	e.Must("deck.document_save", map[string]any{"project": "docs", "name": "notes/a.md", "kind": "artifact", "text": "a", "record": s.Record}, nil)
	return s.Record, files[0].ID
}

func TestRecordIsReadByIDAndDeletedWithItsDocumentsAndFiles(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("docs", harness.FilesManifest())
	rec, photo := seeded(e)
	var got struct {
		ID   int64
		Data map[string]any
	}
	e.Must("deck.record_get", map[string]any{"record": rec}, &got)
	if got.ID != rec || got.Data["name"] != "a" {
		t.Fatalf("record_get gives the record by its id: %+v", got)
	}
	_, tok := e.Token("terminal", "deck.*")
	if code, out := e.As(tok).Call("deck.record_delete", map[string]any{"record": rec}); code != 403 || !strings.Contains(string(out), "only the owner") {
		t.Fatalf("the master does not delete records of an enabled project: %d %s", code, out)
	}
	e.Must("deck.record_delete", map[string]any{"record": rec}, nil)
	if code, _ := e.Call("deck.record_get", map[string]any{"record": rec}); code != 404 {
		t.Fatalf("a deleted record is gone: %d", code)
	}
	var docs []struct{ Name string }
	e.Must("deck.documents_list", map[string]any{"project": "docs"}, &docs)
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
	t.Parallel()
	e := harness.New(t)
	_, tok := e.Token("terminal", "deck.*")
	if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "docs", "manifest": harness.FilesManifest()}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	rec, _ := seeded(e)
	if code, out := e.As(tok).Call("deck.record_delete", map[string]any{"record": rec}); code != 200 {
		t.Fatalf("the builder deletes what trial runs left in a draft: %d %s", code, out)
	}
	if code, out := e.As(tok).Call("deck.file_upload", map[string]any{"project": "docs", "name": "x.txt", "content": "not base64!"}); code != 422 {
		t.Fatalf("content must be base64: %d %s", code, out)
	}
	var f struct{ ID int64 }
	code, out := e.As(tok).Call("deck.file_upload", map[string]any{"project": "docs", "name": "note.txt",
		"content": base64.StdEncoding.EncodeToString([]byte("uploaded text"))})
	if code != 200 || json.Unmarshal(out, &f) != nil || f.ID == 0 {
		t.Fatalf("the builder uploads a sample for a trial run: %d %s", code, out)
	}
	p := e.WaitState(e.Start("docs", "read", map[string]any{"doc": fmt.Sprint(f.ID)}), "done", "error")
	var res struct{ Checks map[string]bool }
	if json.Unmarshal(p.Result, &res) != nil || !res.Checks["doc_read"] {
		t.Fatalf("the uploaded file is the input of a command: %+v", p)
	}
}

func TestAgentReadsARecordByIDOnlyInItsProjectOrItsReads(t *testing.T) {
	t.Parallel()
	var rec int64
	var got []string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		out, _ := c.Tool(ctx, "record_get", map[string]any{"record": rec})
		got = append(got, out)
		return provider.Result{Structured: json.RawMessage(`null`)}
	}))

	e.Project("docs", harness.FilesManifest())
	rec, _ = seeded(e)
	for i, m := range []string{harness.PlainManifest, strings.Replace(harness.PlainManifest, `"screens": []`, `"screens": [], "reads": [{"project": "docs", "collections": ["items"]}]`, 1)} {
		p := e.Project(fmt.Sprintf("p%d", i), m)
		e.WaitState(e.Run(p, "work"), "done", "error")
	}
	if len(got) != 2 || !strings.Contains(got[0], "no record") || !strings.Contains(got[1], `"name":"a"`) {
		t.Fatalf("an agent reads a record of another project only by its reads: %q", got)
	}
}

func TestRecordsGoAfterTheirRetention(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	e.Project("files", strings.Replace(harness.ScriptManifest("make", "", ""), `"key": "name",`,
		`"key": "name", "retention_days": 30,`, 1))
	e.SaveScript("files", "scripts/main.py", harness.PythonLib+`
st, out = deck("deck.records_upsert", {"collection": "items", "records": json.dumps({"name": "a"})})
deck("deck.document_save", {"name": "notes/a.md", "kind": "doc", "text": "a", "record": out["ids"][0]})
open(os.path.join(os.environ["DECK_RESULTS"], "f.txt"), "w").write("f")
print(json.dumps({"files": [{"path": "f.txt", "record": out["ids"][0]}]}))
`)
	if p := e.WaitState(e.Start("files", "make", nil), "done", "error"); p.State != "done" {
		t.Fatalf("the script writes a record with a document and a file: %+v", p)
	}
	var created string
	_ = e.App.Store.DB.QueryRow(`SELECT created_at FROM record`).Scan(&created)
	if !strings.HasPrefix(created, harness.T0.UTC().Format("2006-01-02T15:04")) {
		t.Fatalf("a record is dated by the deck clock: %s", created)
	}
	file := filepath.Join(deckhome.Layout(e.App.Cfg.Home).State(), "files", "1")
	e.Clock.Add(29 * 24 * time.Hour)
	e.Sweep()
	var recs []map[string]any
	e.Must("deck.records_list", map[string]any{"project": "files", "collection": "items"}, &recs)
	if _, err := os.Stat(file); err != nil || len(recs) != 1 {
		t.Fatalf("a record and its file stay before retention_days: %v, %d records", err, len(recs))
	}
	e.Clock.Add(2 * 24 * time.Hour)
	e.Sweep()
	e.Must("deck.records_list", map[string]any{"project": "files", "collection": "items"}, &recs)
	var docs []map[string]any
	e.Must("deck.documents_list", map[string]any{"project": "files"}, &docs)
	if len(recs) != 0 || strings.Contains(fmt.Sprint(docs), "notes/a.md") {
		t.Fatalf("a record goes after retention_days with its documents: %d records, %v", len(recs), docs)
	}
}

func TestLargeIntegerKeysStayDistinct(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	p := e.Project("items", numbersManifest)
	var up struct{ Written int }
	e.Must("deck.records_upsert", map[string]any{"project": "items", "collection": "items",
		"records": `{"id": 9007199254740992, "name": "a"}` + "\n" + `{"id": 9007199254740993.0, "name": "b"}`}, &up)
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "items"})
	if up.Written != 2 || len(recs) != 2 || recs[1].Key != "9007199254740993" || !strings.Contains(recs[1].Data, "9007199254740993") {
		t.Fatalf("large integer keys must stay exact and distinct, got %+v", recs)
	}
}

func TestRecordIDsAreNotReusedAndArtifactsGoWithRecords(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("links", fullManifest)
	e.Must("deck.records_upsert", map[string]any{"project": "links", "collection": "links", "records": `{"url": "https://a.example", "title": "a"}`}, nil)
	var recs []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "links", "collection": "links"}, &recs)
	first := recs[0].ID
	e.Must("deck.document_save", map[string]any{"project": "links", "name": "notes.md", "kind": "artifact", "text": "x", "record": first}, nil)
	e.Must("deck.collection_delete", map[string]any{"project": "links", "collection": "links"}, nil)
	var docs []struct{ Name string }
	e.Must("deck.documents_list", map[string]any{"project": "links", "kind": "artifact"}, &docs)
	if len(docs) != 0 {
		t.Fatalf("artifacts of deleted records must go with them, got %+v", docs)
	}
	e.Must("deck.records_upsert", map[string]any{"project": "links", "collection": "links", "records": `{"url": "https://b.example", "title": "b"}`}, nil)
	e.Must("deck.records_list", map[string]any{"project": "links", "collection": "links"}, &recs)
	if recs[0].ID == first {
		t.Fatalf("a new record must not reuse id %d", first)
	}
}

func TestConcurrentCreatesKeepTheFirstImmutableValue(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("links", fullManifest)
	var wg sync.WaitGroup
	var mu sync.Mutex
	written := 0
	for i := range 8 {
		wg.Go(func() {
			var up struct{ Written int }
			e.Must("deck.records_upsert", map[string]any{"project": "links", "collection": "links",
				"records": fmt.Sprintf(`{"url": "https://a.example", "title": "a", "found_at": "day-%d"}`, i)}, &up)
			mu.Lock()
			written += up.Written
			mu.Unlock()
		})
	}
	wg.Wait()
	if written != 1 {
		t.Fatalf("exactly one concurrent create may set the immutable field, got %d writes", written)
	}
}

func TestKeyFieldsStayImmutableWithReferencedSchemas(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	m := `{"version": 13, "collections": [{"name": "people", "description": "A person", "key": "lower(email)",
	  "schema": {"$ref": "#/$defs/p", "$defs": {"p": {"type": "object", "properties": {"email": {"type": "string"}}, "required": ["email"]}}}}]}`
	e.Project("people", m)
	e.Must("deck.records_upsert", map[string]any{"project": "people", "collection": "people", "records": `{"email": "A@x.example"}`}, nil)
	var recs []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "people", "collection": "people"}, &recs)
	if code, _ := e.Call("deck.record_update", map[string]any{"record": recs[0].ID, "data": `{"email": "a@x.example"}`}); code != 422 {
		t.Fatalf("key field must stay immutable even with a $ref schema, got %d", code)
	}
}

const numbersManifest = `{
  "version": 13,
  "collections": [{"name": "items", "description": "Items", "key": "id", "display": "name",
    "schema": {"type": "object", "properties": {"id": {"type": "integer"}, "name": {"type": "string"}, "score": {"type": "number"}}, "required": ["id"]}}],
  "commands": [{"name": "pick", "title": "Pick", "description": "Picks an item", "class": "internal",
    "input": {"type": "object", "properties": {"item": {"type": "integer", "format": "record"}, "name": {"type": "string"}, "why": {"type": "string"}}},
    "executor": {"type": "agent", "prompt": "Pick."}}],
  "screens": [{"name": "main", "title": "Items", "blocks": [{"type": "table", "title": "Best",
    "source": {"collection": "items", "sort": "-score", "limit": 1},
    "columns": [{"label": "Name", "bind": "name"}],
    "actions": [{"label": "Pick", "command": "pick", "params": {"item": "=_id", "name": "=name", "why": "best"}}]}]}]
}`

// TestTheMasterDeletesWhatItsOwnProcessesWrote: in an enabled module the master deletes a record a process of its
// own wrote, never one of the owner, and the owner can undo it from the feed.
func TestTheMasterDeletesWhatItsOwnProcessesWrote(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	said := map[string]string{}
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		out, _ := c.Tool(ctx, "records_upsert", map[string]any{"project": "notes", "collection": "ideas", "records": `{"title": "trial", "score": 1}`})
		var w struct{ IDs []int64 }
		_ = json.Unmarshal([]byte(out), &w)
		mine, _ := c.Tool(ctx, "record_delete", map[string]any{"record": w.IDs[0]})
		var owners []struct{ ID int64 }
		list, _ := c.Tool(ctx, "records_list", map[string]any{"project": "notes", "collection": "ideas"})
		_ = json.Unmarshal([]byte(list), &owners)
		theirs, _ := c.Tool(ctx, "record_delete", map[string]any{"record": owners[0].ID})
		mu.Lock()
		said["mine"], said["theirs"] = mine, theirs
		mu.Unlock()
		return provider.Result{Text: "Cleaned up."}
	}))

	e.Project("notes", harness.IdeasManifest)
	e.Must("deck.records_upsert", map[string]any{"project": "notes", "collection": "ideas", "records": `{"title": "the owner's", "score": 2}`}, nil)
	e.MasterSaid(e.Say(0, "Clean up after the trial"), 1)
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(said["mine"], `"ok":true`) || !strings.Contains(said["theirs"], "only those its own processes wrote") {
		t.Fatalf("the master deletes only what its own processes wrote: %v", said)
	}
	var feed []struct {
		ID      int64
		Text    string
		CanUndo bool `json:"can_undo"`
	}
	e.Must("deck.feed_list", map[string]any{"kind": "change"}, &feed)
	if len(feed) != 1 || !feed[0].CanUndo || !strings.Contains(feed[0].Text, "trial") {
		t.Fatalf("the delete is in the feed with its undo: %+v", feed)
	}
	e.Must("deck.feed_undo", map[string]any{"entry": feed[0].ID}, nil)
	var recs []struct{ Data struct{ Title string } }
	e.Must("deck.records_list", map[string]any{"project": "notes", "collection": "ideas"}, &recs)
	if len(recs) != 2 {
		t.Fatalf("the undo writes the record back: %+v", recs)
	}
}
