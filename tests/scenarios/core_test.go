package scenarios

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const ideasManifest = `{
  "version": 5,
  "collections": [{
    "name": "ideas", "description": "Short ideas", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "score": {"type": "integer"}, "topic": {"type": "string"}},
               "required": ["title", "score"]}
  }],
  "commands": [{
    "name": "collect", "title": "Collect ideas", "description": "Writes ideas", "class": "internal",
    "executor": {"type": "agent", "prompt": "Write three ideas into the ideas collection."},
    "result": {"type": "object", "properties": {"written": {"type": "integer"}}, "required": ["written"]}
  }],
  "triggers": [{"command": "collect", "cron": "0 9 * * *"}],
  "screens": [{
    "name": "main", "title": "Ideas",
    "blocks": [{"type": "table", "title": "Best ideas",
      "source": {"collection": "ideas", "where": "score >= 5", "sort": "-score", "limit": 10},
      "columns": [{"label": "Idea", "bind": "title"}, {"label": "Score", "bind": "score"}],
      "actions": [{"label": "Again", "command": "collect"}]}]
  }]
}`

func jsonl(recs ...map[string]any) string {
	var b strings.Builder
	for _, r := range recs {
		line, _ := json.Marshal(r)
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func writeIdeas(ctx context.Context, c *Call, topic string) {
	c.Tool(ctx, "records_upsert", map[string]any{"collection": "ideas", "records": jsonl(
		map[string]any{"title": "alpha", "score": 7, "topic": topic}, map[string]any{"title": "beta", "score": 3, "topic": topic},
		map[string]any{"title": "gamma", "score": 9, "topic": topic}, map[string]any{"title": "broken"})})
}

func TestManifestIsValidatedOnSave(t *testing.T) {
	e := newEnv(t, func(context.Context, *Call) provider.Result { return provider.Result{} })
	bad := strings.NewReplacer(`"collection": "ideas", "where"`, `"collection": "nope", "where"`, `"0 9 * * *"`, `"every day"`).Replace(ideasManifest)
	code, out := e.call("deck.project_create", map[string]any{"name": "bad", "manifest": bad})
	if code != 422 || !strings.Contains(string(out), "unknown collection") || !strings.Contains(string(out), "cron") {
		t.Fatalf("want 422 with reasons, got %d %s", code, out)
	}
}

func TestCommandWritesRecordsAndScreenShowsThem(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		writeIdeas(ctx, c, "x")
		return provider.Result{Structured: json.RawMessage(`{"written": 3}`)}
	})
	p := e.createProject("ideas", ideasManifest)
	id := e.run(p, "collect")
	got := e.waitState(id, "done")
	if got.NotBySchema || string(got.Result) != `{"written":3}` {
		t.Fatalf("unexpected result %+v", got)
	}
	rows := e.screenRows(p, "main")
	if len(rows) != 2 || rows[0].Cells[0] != "gamma" || rows[1].Cells[0] != "alpha" {
		t.Fatalf("want gamma, alpha filtered and sorted, got %v", rows)
	}
	e.run(p, "collect")
	time.Sleep(200 * time.Millisecond)
	e.App.Runner.Wait()
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "ideas"})
	if len(recs) != 3 {
		t.Fatalf("upsert by key must not duplicate, got %d records", len(recs))
	}
}

func TestQuestionAnsweredLive(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		d := c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{
			{"question": "Which topic?", "header": "Topic", "options": []map[string]any{{"label": "go"}, {"label": "rust"}}}}})
		answers, _ := d["updatedInput"].(map[string]any)["answers"].(map[string]any)
		writeIdeas(ctx, c, answers["Which topic?"].(string))
		return provider.Result{Structured: json.RawMessage(`{"written": 3}`)}
	})
	p := e.createProject("ideas", ideasManifest)
	id := e.run(p, "collect")
	a := e.waitAsk()
	if a.Kind != "question" || a.Text != "Which topic?" {
		t.Fatalf("unexpected ask %+v", a)
	}
	if items := e.cockpitItems(); len(items) != 1 || items[0].Title != "Topic" {
		t.Fatalf("a question the picker shows in full is headed by its header, not its text twice: %+v", items)
	}
	e.answer(a.ID, `{"Which topic?":"go"}`)
	e.waitState(id, "done")
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "ideas"})
	if len(recs) == 0 || !strings.Contains(recs[0].Data, `"topic":"go"`) {
		t.Fatalf("answer did not reach the agent: %v", recs)
	}
}

func TestQuestionAnsweredLateResumesSession(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if !c.Req.Resume {
			d := c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{{"question": "Which topic?"}}})
			if d["behavior"] != "deny" {
				panic("expected deny on timeout")
			}
			return provider.Result{Text: "stopped"}
		}
		topic := "?"
		if strings.Contains(c.Req.Prompt, "rust") {
			topic = "rust"
		}
		writeIdeas(ctx, c, topic)
		return provider.Result{Structured: json.RawMessage(`{"written": 3}`)}
	}, func(c *app.Config) { c.QuestionWait = 100 * time.Millisecond })
	p := e.createProject("ideas", ideasManifest)
	id := e.run(p, "collect")
	e.waitState(id, "waiting_answer")
	a := e.waitAsk()
	e.answer(a.ID, "rust")
	e.waitState(id, "done")
	calls := e.Fake.Calls()
	if len(calls) != 2 || !calls[1].Resume || calls[1].SessionID != calls[0].SessionID {
		t.Fatalf("want resume of the first session, got %+v", calls)
	}
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "ideas"})
	if len(recs) == 0 || !strings.Contains(recs[0].Data, `"topic":"rust"`) {
		t.Fatalf("late answer did not reach the agent: %v", recs)
	}
}

func TestConfirmationRejectedWithReason(t *testing.T) {
	var msg string
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		d := c.Approve(ctx, "Bash", map[string]any{"command": "git push"})
		msg, _ = d["message"].(string)
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	})
	p := e.createProject("ideas", ideasManifest)
	id := e.run(p, "collect")
	a := e.waitAsk()
	if a.Kind != "confirm" || !strings.Contains(a.Text, "git push") {
		t.Fatalf("unexpected ask %+v", a)
	}
	e.answer(a.ID, "deny:not now")
	e.waitState(id, "done")
	if !strings.Contains(msg, "rejected") || !strings.Contains(msg, "not now") {
		t.Fatalf("agent must get the refusal with reason, got %q", msg)
	}
	calls, _ := e.App.Store.Q.ListToolCalls(context.Background(), store.ID(id))
	if len(calls) < 2 || calls[len(calls)-1].Outcome != "rejected" {
		t.Fatalf("tool call journal: %+v", calls)
	}
}

func TestResultNotBySchemaAfterOneRetry(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"count": 1}`)}
	})
	p := e.createProject("ideas", ideasManifest)
	id := e.run(p, "collect")
	got := e.waitState(id, "done")
	if !got.NotBySchema || string(got.Result) != `{"count":1}` {
		t.Fatalf("want raw result marked not by schema, got %+v", got)
	}
	calls := e.Fake.Calls()
	if len(calls) != 2 || !strings.Contains(calls[1].Prompt, "did not match") {
		t.Fatalf("want exactly one retry asking for JSON only, got %d calls", len(calls))
	}
}
