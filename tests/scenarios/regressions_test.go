package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/internal/api"
	"deck2/internal/provider"
	"deck2/internal/store/db"
	"deck2/web"
)

func TestAllowOnceAllowsExactlyOneCall(t *testing.T) {
	var mu sync.Mutex
	var behaviors []string
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if !c.Req.Resume {
			c.Approve(ctx, "Bash", map[string]any{"command": "make deploy"})
			return provider.Result{Text: "stopped"}
		}
		var wg sync.WaitGroup
		for range 4 {
			wg.Go(func() {
				d := c.Approve(ctx, "Bash", map[string]any{"command": "make deploy"})
				mu.Lock()
				behaviors = append(behaviors, d["behavior"].(string))
				mu.Unlock()
			})
		}
		wg.Wait()
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}, short)
	p := e.createProject("ideas", ideasManifest)
	id := e.run(p, "collect")
	e.waitState(id, "waiting_answer")
	e.answer(e.waitAsk().ID, "allow")
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(behaviors)
		mu.Unlock()
		if n == 4 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	allowed := 0
	for _, b := range behaviors {
		if b == "allow" {
			allowed++
		}
	}
	if allowed != 1 {
		t.Fatalf("allow once must let exactly one of the concurrent calls through, got %v", behaviors)
	}
}

func TestEmptyTokenDoesNotOpenTheAPI(t *testing.T) {
	e := newEnv(t, nil)
	ts := httptest.NewServer(api.New(e.App, "", web.FS()).Handler())
	defer ts.Close()
	req, _ := http.NewRequest("POST", ts.URL+"/api/commands/deck.projects_list", nil)
	req.Header.Set("Authorization", "Bearer ")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("empty token must refuse every request, got %d", resp.StatusCode)
	}
}

const numbersManifest = `{
  "version": 4,
  "collections": [{"name": "items", "description": "Items", "key": "id", "display": "name",
    "schema": {"type": "object", "properties": {"id": {"type": "integer"}, "name": {"type": "string"}, "score": {"type": "number"}}, "required": ["id"]}}],
  "commands": [{"name": "pick", "title": "Pick", "description": "Picks an item", "class": "internal",
    "input": {"type": "object", "properties": {"item": {"type": "integer", "format": "record"}, "name": {"type": "string"}, "why": {"type": "string"}}},
    "executor": {"type": "agent", "prompt": "Pick."}}],
  "screens": [{"name": "main", "title": "Items", "blocks": [{"type": "table", "title": "Best",
    "source": {"collection": "items", "sort": "-score", "limit": 1},
    "columns": [{"label": "Name", "bind": "name"}],
    "actions": [{"label": "Pick", "command": "pick", "params": {"item": "$record.id", "name": "$record.name", "why": "best"}}]}]}]
}`

func TestLargeIntegerKeysStayDistinct(t *testing.T) {
	e := newEnv(t, nil)
	p := e.createProject("items", numbersManifest)
	var up struct{ Written int }
	e.must("deck.records_upsert", map[string]any{"project": "items", "collection": "items",
		"records": `{"id": 9007199254740992, "name": "a"}` + "\n" + `{"id": 9007199254740993.0, "name": "b"}`}, &up)
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "items"})
	if up.Written != 2 || len(recs) != 2 || recs[1].Key != "9007199254740993" || !strings.Contains(recs[1].Data, "9007199254740993") {
		t.Fatalf("large integer keys must stay exact and distinct, got %+v", recs)
	}
}

func TestScreenSortsNumbersAndResolvesRowParams(t *testing.T) {
	e := newEnv(t, nil)
	p := e.createProject("items", numbersManifest)
	e.must("deck.records_upsert", map[string]any{"project": "items", "collection": "items",
		"records": jsonl(map[string]any{"id": 1, "name": "nine", "score": 9}, map[string]any{"id": 2, "name": "hundred", "score": 100},
			map[string]any{"id": 3, "name": "twenty", "score": 20})}, nil)
	rows := e.screenRows(p, "main")
	if len(rows) != 1 || rows[0].Cells[0] != "hundred" {
		t.Fatalf("descending numeric sort must put 100 first, got %v", rows)
	}
	params, _ := rows[0].Actions[0]["params"].(map[string]any)
	if params["name"] != "hundred" || params["why"] != "best" || params["item"] == nil {
		t.Fatalf("row action params must be resolved from the record, got %v", rows[0])
	}
}

func TestAnswerSavedBeforeRestartReachesParkedProcess(t *testing.T) {
	home := t.TempDir()
	fake := &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		askQuestion(ctx, c, "Which topic?")
		return provider.Result{Text: "stopped"}
	}}
	e := restartEnv(t, home, fake)
	p := e.createProject("ideas", ideasManifest)
	id := e.run(p, "collect")
	e.waitState(id, "waiting_answer")
	a := e.waitAsk()
	_ = e.App.Close()

	fake2 := &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}}
	e2 := restartEnv(t, home, fake2)
	if err := e2.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		_, err := q.AnswerAsk(ctx, db.AnswerAskParams{Answer: "rust", ID: a.ID})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := e2.App.Runner.ResumeInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	e2.waitState(id, "done")
	if calls := fake2.Calls(); len(calls) != 1 || !strings.Contains(calls[0].Prompt, "rust") {
		t.Fatalf("the saved answer must reach the parked process after restart, got %+v", calls)
	}
}

func TestResumeAfterItsCommandWasRenamedFinishesTheSession(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.Req.Resume {
			return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
		}
		askQuestion(ctx, c, "Which topic?")
		return provider.Result{Text: "stopped"}
	}, short)
	p := e.createProject("ideas", ideasManifest)
	id := e.run(p, "collect")
	e.waitState(id, "waiting_answer")
	a := e.waitAsk()
	renamed := strings.NewReplacer(`"name": "collect"`, `"name": "gather"`, `"command": "collect"`, `"command": "gather"`).Replace(ideasManifest)
	e.must("deck.manifest_save", map[string]any{"project": "ideas", "manifest": renamed}, nil)
	e.answer(a.ID, "go")
	if got := e.waitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the session goes on by the manifest revision it started by: %+v", got)
	}
}

func TestProjectPromptReachesTheAgent(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	})
	var p struct{ ID int64 }
	e.must("deck.project_create", map[string]any{"name": "ideas", "manifest": ideasManifest, "prompt": "Ideas for a solo developer.\nPrefer Go."}, &p)
	e.names[p.ID] = "ideas"
	e.waitState(e.run(p.ID, "collect"), "done")
	if calls := e.Fake.Calls(); len(calls) != 1 || !strings.Contains(calls[0].Prompt, "Prefer Go.") {
		t.Fatalf("project prompt must be part of the agent prompt, got %+v", calls)
	}
}

func TestRecordIDsAreNotReusedAndArtifactsGoWithRecords(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("links", fullManifest)
	e.must("deck.records_upsert", map[string]any{"project": "links", "collection": "links", "records": `{"url": "https://a.example", "title": "a"}`}, nil)
	var recs []struct{ ID int64 }
	e.must("deck.records_list", map[string]any{"project": "links", "collection": "links"}, &recs)
	first := recs[0].ID
	e.must("deck.document_save", map[string]any{"project": "links", "name": "notes.md", "kind": "artifact", "text": "x", "record": first}, nil)
	e.must("deck.collection_delete", map[string]any{"project": "links", "collection": "links"}, nil)
	var docs []struct{ Name string }
	e.must("deck.documents_list", map[string]any{"project": "links", "kind": "artifact"}, &docs)
	if len(docs) != 0 {
		t.Fatalf("artifacts of deleted records must go with them, got %+v", docs)
	}
	e.must("deck.records_upsert", map[string]any{"project": "links", "collection": "links", "records": `{"url": "https://b.example", "title": "b"}`}, nil)
	e.must("deck.records_list", map[string]any{"project": "links", "collection": "links"}, &recs)
	if recs[0].ID == first {
		t.Fatalf("a new record must not reuse id %d", first)
	}
}

func TestConcurrentCreatesKeepTheFirstImmutableValue(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("links", fullManifest)
	var wg sync.WaitGroup
	var mu sync.Mutex
	written := 0
	for i := range 8 {
		wg.Go(func() {
			var up struct{ Written int }
			e.must("deck.records_upsert", map[string]any{"project": "links", "collection": "links",
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
	e := newEnv(t, nil)
	m := `{"version": 4, "collections": [{"name": "people", "description": "A person", "key": "lower(email)",
	  "schema": {"$ref": "#/$defs/p", "$defs": {"p": {"type": "object", "properties": {"email": {"type": "string"}}, "required": ["email"]}}}}]}`
	e.createProject("people", m)
	e.must("deck.records_upsert", map[string]any{"project": "people", "collection": "people", "records": `{"email": "A@x.example"}`}, nil)
	var recs []struct{ ID int64 }
	e.must("deck.records_list", map[string]any{"project": "people", "collection": "people"}, &recs)
	if code, _ := e.call("deck.record_update", map[string]any{"record": recs[0].ID, "data": `{"email": "a@x.example"}`}); code != 422 {
		t.Fatalf("key field must stay immutable even with a $ref schema, got %d", code)
	}
}
