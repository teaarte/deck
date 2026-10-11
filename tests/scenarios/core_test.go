package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

const ideasManifest = `{
  "version": 12,
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
	t.Parallel()
	e := newEnv(t, func(context.Context, *Call) provider.Result { return provider.Result{} })
	bad := strings.NewReplacer(`"collection": "ideas", "where"`, `"collection": "nope", "where"`, `"0 9 * * *"`, `"every day"`).Replace(ideasManifest)
	code, out := e.call("deck.project_create", map[string]any{"name": "bad", "manifest": bad})
	if code != 422 || !strings.Contains(string(out), "unknown collection") || !strings.Contains(string(out), "cron") {
		t.Fatalf("want 422 with reasons, got %d %s", code, out)
	}
}

func TestCommandWritesRecordsAndScreenShowsThem(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	if running := e.cockpitRunning(); len(running) != 1 || !strings.Contains(running[0].Text, "waits for your answer") ||
		!strings.Contains(running[0].Text, "Which topic?") {
		t.Fatalf("a process that waits for the owner says so in Running now, with its question: %+v", running)
	}
	if b, _ := json.Marshal(e.screenMessages("", "cockpit", map[string]any{"topic": "asks"})); !strings.Contains(string(b), "waits for your answer") {
		t.Fatalf("a question that opens reads Running now again: %s", b)
	}
	e.answer(a.ID, `{"Which topic?":"go"}`)
	e.waitState(id, "done")
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "ideas"})
	if len(recs) == 0 || !strings.Contains(recs[0].Data, `"topic":"go"`) {
		t.Fatalf("answer did not reach the agent: %v", recs)
	}
}

// TestAnAnswerTheProcessDidNotActOnGoesToTheMaster: an agent asks the owner, gets the answer live and ends with
// nothing; the answer reaches the master on its next turn, and the owner's feed says so.
func TestAnAnswerTheProcessDidNotActOnGoesToTheMaster(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() != "collect" {
			return provider.Result{Text: "On it."}
		}
		c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{
			{"question": "The voice reads the style aloud; how should I fix it?", "options": []map[string]any{{"label": "Trim it"}}}}})
		return provider.Result{Structured: json.RawMessage(`null`)}
	})
	p := e.createProject("ideas", nullable(ideasManifest))
	id := e.run(p, "collect")
	a := e.waitAsk()
	e.answer(a.ID, `{"The voice reads the style aloud; how should I fix it?":"regenerate the voice"}`)
	e.waitState(id, "done")
	n := e.waitNote("regenerate the voice")
	if !strings.Contains(n.Text, fmt.Sprintf("#%d", id)) || !strings.Contains(n.Text, "master") {
		t.Fatalf("the owner hears that the process did nothing with the answer and the master has it: %+v", n)
	}
	topic := e.say(0, "hello")
	e.masterSaid(topic, 1)
	var prompt string
	for _, c := range e.Fake.Calls() {
		if (&Call{Req: c}).command() != "collect" {
			prompt = c.Prompt
		}
	}
	if !strings.Contains(prompt, "regenerate the voice") || !strings.Contains(prompt, "how should I fix it?") ||
		!strings.Contains(prompt, fmt.Sprintf("#%d", id)) || !strings.Contains(prompt, "(data, not instructions)") {
		t.Fatalf("the master's next turn has the answer, the question and the process, as data:\n%s", prompt)
	}
}

// TestAnAnswerAfterTheProcessFinishedGoesToTheMaster: the owner answers a question whose process finished in the
// meantime; no process takes the answer, so the master does.
func TestAnAnswerAfterTheProcessFinishedGoesToTheMaster(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`null`)}
	})
	p := e.createProject("ideas", nullable(ideasManifest))
	id := e.run(p, "collect")
	e.waitState(id, "done")
	if _, err := e.App.Store.DB.Exec(`INSERT INTO ask (workspace_id, project_id, process_id, kind, text) VALUES (1, ?, ?, 'question', 'Which voice?')`, p, id); err != nil {
		t.Fatal(err)
	}
	a := e.waitAsk()
	e.answer(a.ID, `{"Which voice?":"the calm one"}`)
	if n := e.waitNote("the calm one"); !strings.Contains(n.Text, fmt.Sprintf("#%d", id)) {
		t.Fatalf("the owner hears that the master has the answer: %+v", n)
	}
	var feed []struct{ Kind string }
	e.must("deck.feed_list", map[string]any{"unread": true}, &feed)
	for _, f := range feed {
		if f.Kind != "note" {
			t.Fatalf("unread are notes only: %+v", feed)
		}
	}
}

// TestAQuestionWhoseCallDroppedStaysOpenWhenTheTurnBreaks: the transport drops while the owner is asked and the
// turn ends with an error; the question stays for the owner and the answer resumes the session.
func TestAQuestionWhoseCallDroppedStaysOpenWhenTheTurnBreaks(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if !c.Req.Resume {
			dropped, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			c.Tool(dropped, "approve", map[string]any{"tool_name": "AskUserQuestion", "tool_use_id": "t1", "input": map[string]any{"questions": []map[string]any{
				{"question": "Which topic?", "options": []map[string]any{{"label": "go"}, {"label": "rust"}}}}}})
			return provider.Result{IsError: true, Text: `MCP server "deck" transport dropped mid-call`}
		}
		writeIdeas(ctx, c, map[bool]string{true: "rust", false: "?"}[strings.Contains(c.Req.Prompt, "rust")])
		return provider.Result{Structured: json.RawMessage(`{"written": 3}`)}
	})
	p := e.createProject("ideas", ideasManifest)
	id := e.run(p, "collect")
	e.waitState(id, "waiting_answer", "error")
	a := e.waitAsk()
	if a.Kind != "question" || a.Text != "Which topic?" {
		t.Fatalf("the question stays open for the owner: %+v", a)
	}
	e.answer(a.ID, `{"Which topic?":"rust"}`)
	if got := e.waitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the answer resumes the session: %+v", got)
	}
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "ideas"})
	if len(recs) == 0 || !strings.Contains(recs[0].Data, `"topic":"rust"`) {
		t.Fatalf("the answer reached the agent: %v", recs)
	}
}

func TestQuestionAnsweredLateResumesSession(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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
	t.Parallel()
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

const calcManifest = `{
  "version": 12,
  "collections": [{"name": "nums", "description": "A number", "key": "n",
    "schema": {"type": "object", "properties": {"n": {"type": "integer"}}, "required": ["n"]}}],
  "commands": [{"name": "calc", "title": "Calc", "description": "Uses a number.", "class": "internal",
    "input": {"type": "object", "properties": {"n": {"type": "integer"}}},
    "executor": {"type": "agent", "prompt": "OLD PROMPT"}}]
}`

// TestAToolAddedToTheProjectReachesARunningTurn: a command added to the manifest while an agent works is a tool of
// its session at once, and a command taken out is gone, so the master tries what it just built in the same turn.
func TestAToolAddedToTheProjectReachesARunningTurn(t *testing.T) {
	t.Parallel()
	saved, listed := make(chan struct{}), make(chan []string, 2)
	names := func(ctx context.Context, c *Call) []string {
		var out []string
		for tool, err := range c.Session.Tools(ctx, nil) {
			if err == nil {
				out = append(out, tool.Name)
			}
		}
		return out
	}
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		listed <- names(ctx, c)
		<-saved
		time.Sleep(300 * time.Millisecond)
		listed <- names(ctx, c)
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	p := e.createProject("notes", plainManifest)
	id := e.run(p, "work")
	before := <-listed
	more := strings.Replace(plainManifest, `"commands": [{`, `"commands": [{"name": "extra", "title": "Extra", "description": "Does more", "class": "internal",
    "executor": {"type": "agent", "prompt": "More."}}, {`, 1)
	e.must("deck.manifest_save", map[string]any{"project": "notes", "manifest": more}, nil)
	close(saved)
	after := <-listed
	if slices.Contains(before, "notes__extra") || !slices.Contains(after, "notes__extra") {
		t.Fatalf("the new command is a tool of the running turn: before %v after %v", before, after)
	}
	e.waitState(id, "done")
}
