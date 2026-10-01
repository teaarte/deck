package scenarios

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/internal/store/db"
)

const calcManifest = `{
  "version": 4,
  "collections": [{"name": "nums", "description": "A number", "key": "n",
    "schema": {"type": "object", "properties": {"n": {"type": "integer"}}, "required": ["n"]}}],
  "commands": [{"name": "calc", "title": "Calc", "description": "Uses a number.", "class": "internal",
    "input": {"type": "object", "properties": {"n": {"type": "integer"}}},
    "executor": {"type": "agent", "prompt": "OLD PROMPT"}}]
}`

func TestDocumentIDsAreNotReused(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("nums", calcManifest)
	e.must("deck.records_upsert", map[string]any{"project": "nums", "collection": "nums", "records": jsonl(map[string]any{"n": 1})}, nil)
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: e.projectID("nums"), Collection: "nums"})
	var old struct{ ID int64 }
	e.must("deck.document_save", map[string]any{"project": "nums", "name": "a.md", "kind": "artifact", "text": "mine", "record": recs[0].ID}, &old)
	e.must("deck.collection_delete", map[string]any{"project": "nums", "collection": "nums"}, nil)
	var fresh struct{ ID int64 }
	e.must("deck.document_save", map[string]any{"project": "nums", "name": "b.md", "kind": "doc", "text": "other"}, &fresh)
	if fresh.ID == old.ID {
		t.Fatalf("a new document must not take the id of a deleted one: %d", fresh.ID)
	}
	e.refused("deck.document_get", map[string]any{"document": old.ID}, 404, "no document")
}

func TestCommandRunsTheRevisionItWasAuthorizedAgainst(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("nums", calcManifest)
	cmd, err := e.App.Registry.Lookup(context.Background(), 1, "nums.calc")
	if err != nil {
		t.Fatal(err)
	}
	e.must("deck.manifest_save", map[string]any{"project": "nums",
		"manifest": strings.NewReplacer(`"class": "internal"`, `"class": "external", "callers": ["owner"]`, "OLD PROMPT", "NEW PROMPT").Replace(calcManifest)}, nil)
	out, err := cmd.Handler(context.Background(), registry.Actor{Kind: registry.Agent, Workspace: 1, ProjectID: e.projectID("nums")}, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	e.waitState(out.(app.Started).Process, "done")
	if calls := e.Fake.Calls(); len(calls) != 1 || !strings.Contains(calls[0].Prompt, "OLD PROMPT") || strings.Contains(calls[0].Prompt, "NEW PROMPT") {
		t.Fatalf("the process must run the command of the revision the call was checked against: %+v", calls)
	}
}

func TestOutOfRangeIntegersAreRefused(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("nums", calcManifest)
	var res struct {
		Written int
		Errors  []string
	}
	e.must("deck.records_upsert", map[string]any{"project": "nums", "collection": "nums",
		"records": "{\"n\": 18446744073709551614}\n{\"n\": 18446744073709551615}\n"}, &res)
	if res.Written != 0 || len(res.Errors) != 2 || !strings.Contains(res.Errors[0], "does not fit") {
		t.Fatalf("integers beyond 64 bits must be refused, not rounded into one key: %+v", res)
	}
}

func TestCommandInputKeepsLargeIntegers(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("nums", calcManifest)
	code, body := e.do("POST", "/api/commands/nums.calc", map[string]any{"n": int64(9007199254740993)})
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	var started struct{ Process int64 }
	_ = json.Unmarshal(body, &started)
	p, _ := e.App.Store.Q.GetProcess(context.Background(), started.Process)
	if !strings.Contains(p.Input, "9007199254740993") {
		t.Fatalf("input integers must reach the process exactly: %s", p.Input)
	}
	e.waitState(started.Process, "done")
}

func TestMCPSessionNeedsItsOwnToken(t *testing.T) {
	var status int
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		body := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"records_upsert","arguments":{"collection":"nums","records":"{\"n\": 5}"}}}`
		req, _ := http.NewRequestWithContext(ctx, "POST", strings.Replace(c.Req.MCPURL, "/mcp/", "/mcp/not-a-valid-token-", 1), strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")
		req.Header.Set("Mcp-Session-Id", c.Session.ID())
		if resp, err := http.DefaultClient.Do(req); err == nil {
			status = resp.StatusCode
			resp.Body.Close()
		}
		return provider.Result{Text: "ok"}
	})
	p := e.createProject("nums", calcManifest)
	e.waitState(e.run(p, "calc"), "done")
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "nums"})
	if status != http.StatusNotFound || len(recs) != 0 {
		t.Fatalf("a session id under another token must be refused: status %d, %d records", status, len(recs))
	}
}

func TestExpressionVariablesCannotHideFields(t *testing.T) {
	e := newEnv(t, idle)
	key := strings.Replace(calcManifest, `"key": "n"`, `"key": "string(n) + (let n = 1; string(n))"`, 1)
	e.refused("deck.project_create", map[string]any{"name": "a", "manifest": key}, 422, "not allowed in a key")
	display := strings.Replace(calcManifest, `"key": "n"`, `"key": "n", "display": "let n = 'x'; n"`, 1)
	e.refused("deck.project_create", map[string]any{"name": "b", "manifest": display}, 422, `hides the field`)
}

func TestComposedInputSchemasAreRefused(t *testing.T) {
	e := newEnv(t, idle)
	m := strings.Replace(calcManifest, `"input": {"type": "object", "properties": {"n": {"type": "integer"}}}`,
		`"input": {"type": "object", "allOf": [{"properties": {"config": {"type": "object"}}, "required": ["config"]}]}`, 1)
	e.refused("deck.project_create", map[string]any{"name": "a", "manifest": m}, 422, `"allOf" is not allowed`)
	nested := strings.Replace(calcManifest, `"n": {"type": "integer"}}}`, `"n": {"type": "integer", "anyOf": [{"type": "object"}]}}}`, 1)
	e.refused("deck.project_create", map[string]any{"name": "b", "manifest": nested}, 422, `"anyOf" is not allowed`)
}

func TestWholeNumbersWrittenAsFloatsAreRefused(t *testing.T) {
	e := newEnv(t, idle)
	e.createProject("nums", calcManifest)
	var res struct {
		Written int
		Errors  []string
	}
	e.must("deck.records_upsert", map[string]any{"project": "nums", "collection": "nums",
		"records": "{\"n\": 18446744073709551614.0}\n{\"n\": 1.0}\n"}, &res)
	if res.Written != 1 || len(res.Errors) != 1 || !strings.Contains(res.Errors[0], "does not fit") {
		t.Fatalf("a whole number beyond 64 bits is refused in any spelling: %+v", res)
	}
}

func TestTriggerParamsKeepLargeIntegers(t *testing.T) {
	e := newClockEnv(t, idle)
	m := strings.Replace(calcManifest, `"executor": {"type": "agent", "prompt": "OLD PROMPT"}}]`,
		`"executor": {"type": "agent", "prompt": "OLD PROMPT"}}],
  "triggers": [{"command": "calc", "cron": "0 9 * * *", "params": {"n": 9007199254740993}}]`, 1)
	p := e.createProject("nums", m)
	e.Clock.Add(48 * time.Hour)
	e.App.Scheduler.Tick(context.Background())
	e.App.Runner.Wait()
	ps := e.processes()
	if len(ps) != 1 || ps[0].Project != p {
		t.Fatalf("trigger params reach the command exactly: %+v", ps)
	}
	if got, _ := e.App.Store.Q.GetProcess(context.Background(), ps[0].ID); !strings.Contains(got.Input, "9007199254740993") {
		t.Fatalf("trigger params reach the command exactly: %s", got.Input)
	}
}

func TestVariablesNeedKnownFields(t *testing.T) {
	e := newEnv(t, idle)
	m := strings.NewReplacer(`"schema": {"type": "object", "properties": {"n": {"type": "integer"}}, "required": ["n"]}`, `"schema": {"type": "object"}`,
		`"key": "n"`, `"key": "n", "display": "let n = 'x'; n"`).Replace(calcManifest)
	e.refused("deck.project_create", map[string]any{"name": "a", "manifest": m}, 422, "need a schema that lists its properties")
}

func TestFinishedProcessSessionIsClosed(t *testing.T) {
	var url, sid string
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		url, sid = c.Req.MCPURL, c.Session.ID()
		return provider.Result{Text: "ok"}
	})
	p := e.createProject("nums", calcManifest)
	e.waitState(e.run(p, "calc"), "done")
	body := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"records_upsert","arguments":{"collection":"nums","records":"{\"n\": 5}"}}}`
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", sid)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "nums"})
	if resp.StatusCode != http.StatusNotFound || len(recs) != 0 {
		t.Fatalf("a finished process's session takes no calls: %d, %d records", resp.StatusCode, len(recs))
	}
}
