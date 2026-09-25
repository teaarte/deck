package scenarios

import (
	"context"
	"encoding/json"
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
		if c.Req.ResumeSession == "" {
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
  "version": 2,
  "collections": [{"name": "items", "description": "Items", "key": "id", "display": "name",
    "schema": {"type": "object", "properties": {"id": {"type": "integer"}, "name": {"type": "string"}, "score": {"type": "number"}}, "required": ["id"]}}],
  "commands": [{"name": "pick", "title": "Pick", "description": "Picks an item", "class": "internal",
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
		"records": `{"id": 9007199254740992, "name": "a"}` + "\n" + `{"id": 9007199254740993, "name": "b"}`}, &up)
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
	if len(rows) != 1 || rows[0]["c0"] != "hundred" {
		t.Fatalf("descending numeric sort must put 100 first, got %v", rows)
	}
	params, _ := rows[0]["a0"].(map[string]any)
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
	a1, ts1 := startApp(t, home, fake)
	e := &Env{T: t, App: a1, URL: ts1.URL, Fake: fake, names: map[int64]string{}}
	p := e.createProject("ideas", ideasManifest)
	id := e.run(p, "collect")
	e.waitState(id, "waiting_answer")
	a := e.waitAsk()
	_ = a1.Close()
	ts1.Close()

	fake2 := &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}}
	a2, ts2 := startApp(t, home, fake2)
	defer func() { _ = a2.Close(); ts2.Close() }()
	if _, err := a2.Store.Q.AnswerAsk(context.Background(), db.AnswerAskParams{Answer: "rust", ID: a.ID}); err != nil {
		t.Fatal(err)
	}
	if err := a2.Runner.ResumeInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	e2 := &Env{T: t, App: a2, URL: ts2.URL, Fake: fake2, names: map[int64]string{}}
	e2.waitState(id, "done")
	if calls := fake2.Calls(); len(calls) != 1 || !strings.Contains(calls[0].Prompt, "rust") {
		t.Fatalf("the saved answer must reach the parked process after restart, got %+v", calls)
	}
}

func TestResumeOfRemovedCommandFailsTheProcess(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
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
	got := e.waitState(id, "error")
	if !strings.Contains(got.Error, "no longer exists") {
		t.Fatalf("want an error naming the missing command, got %+v", got)
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
