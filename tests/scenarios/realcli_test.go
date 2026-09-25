package scenarios

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deck2/internal/api"
	"deck2/internal/app"
	"deck2/internal/provider/claudecode"
	"deck2/internal/store/db"
	"deck2/web"
)

const realManifest = `{
  "version": 3,
  "collections": [{
    "name": "ideas", "description": "Short ideas", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}, "score": {"type": "integer"}, "topic": {"type": "string"}},
               "required": ["title", "score", "topic"]}
  }],
  "commands": [{
    "name": "collect", "title": "Collect ideas", "description": "Writes ideas", "class": "internal",
    "executor": {"type": "agent", "prompt": "First ask the owner with the AskUserQuestion tool which topic to use, with exactly two options: go and rust. Then write two short project ideas about the chosen topic into the ideas collection using the records_upsert tool (score 1-10, topic = the chosen topic). Do not use any other tools. Finish with the number of records written."},
    "result": {"type": "object", "properties": {"written": {"type": "integer"}}, "required": ["written"]}
  }],
  "triggers": [],
  "screens": [{"name": "main", "title": "Ideas", "blocks": [{"type": "table", "title": "Ideas",
    "source": {"collection": "ideas"}, "columns": [{"label": "Idea", "bind": "title"}, {"label": "Topic", "bind": "topic"}]}]}]
}`

// TestRealClaudeCodeQuestionResume runs the stage 0 acceptance scenario on the installed Claude Code CLI.
func TestRealClaudeCodeQuestionResume(t *testing.T) {
	if os.Getenv("DECK_REAL_CLI") == "" {
		t.Skip("set DECK_REAL_CLI=1 to run against the real Claude Code CLI")
	}
	ts := httptest.NewUnstartedServer(nil)
	base := "http://" + ts.Listener.Addr().String()
	a, err := app.New(app.Config{Home: t.TempDir(), MCPBase: base, QuestionWait: 5 * time.Second, ConfirmWait: 5 * time.Second}, claudecode.New())
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = api.New(a, token, web.FS()).Handler()
	ts.Start()
	t.Cleanup(func() { _ = a.Close(); ts.Close() })
	e := &Env{T: t, App: a, URL: base, names: map[int64]string{}}

	p := e.createProject("ideas", realManifest)
	id := e.run(p, "collect")
	waitLong := func(states ...string) proc {
		deadline := time.Now().Add(4 * time.Minute)
		for time.Now().Before(deadline) {
			got := e.process(id)
			for _, s := range states {
				if got.State == s {
					return got
				}
			}
			time.Sleep(500 * time.Millisecond)
		}
		t.Fatalf("process did not reach %v: %+v", states, e.process(id))
		return proc{}
	}
	stopped := waitLong("waiting_answer", "done", "error")
	if stopped.State != "waiting_answer" {
		t.Fatalf("agent should stop waiting for the owner, got %+v", stopped)
	}
	a1 := e.waitAsk()
	if a1.Kind != "question" {
		t.Fatalf("unexpected ask %+v", a1)
	}
	t.Logf("question: %s", a1.Text)
	e.answer(a1.ID, "rust")
	got := waitLong("done", "error")
	t.Logf("result: %+v", got)
	if got.State != "done" || got.NotBySchema {
		t.Fatalf("want done by schema, got %+v", got)
	}
	recs, _ := a.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "ideas"})
	if len(recs) < 2 {
		t.Fatalf("want at least 2 records, got %d", len(recs))
	}
	for _, r := range recs {
		if !strings.Contains(strings.ToLower(r.Data), `"topic":"rust"`) {
			t.Fatalf("record ignores the owner's answer: %s", r.Data)
		}
		t.Logf("record: %s", r.Data)
	}
	if rows := e.screenRows(p, "main"); len(rows) != len(recs) {
		t.Fatalf("screen shows %d rows, want %d", len(rows), len(recs))
	}
}

const bashManifest = `{
  "version": 3,
  "collections": [],
  "commands": [{
    "name": "mark", "title": "Mark", "description": "Writes a marker file with Bash", "class": "internal",
    "executor": {"type": "agent", "prompt": "Use the Bash tool to run exactly this command: echo deck-ok > marker.txt\nIf it is refused, do not try any other way; just report the refusal and stop."}
  }],
  "triggers": [],
  "screens": []
}`

// TestRealClaudeCodePermission checks that a Bash permission request reaches the owner and that allow and
// deny with a reason both work on the real CLI.
func TestRealClaudeCodePermission(t *testing.T) {
	if os.Getenv("DECK_REAL_CLI") == "" {
		t.Skip("set DECK_REAL_CLI=1 to run against the real Claude Code CLI")
	}
	home := t.TempDir()
	ts := httptest.NewUnstartedServer(nil)
	base := "http://" + ts.Listener.Addr().String()
	a, err := app.New(app.Config{Home: home, MCPBase: base, QuestionWait: 2 * time.Minute, ConfirmWait: 2 * time.Minute}, claudecode.New())
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = api.New(a, token, web.FS()).Handler()
	ts.Start()
	t.Cleanup(func() { _ = a.Close(); ts.Close() })
	e := &Env{T: t, App: a, URL: base, names: map[int64]string{}}
	p := e.createProject("perm", bashManifest)

	for _, tc := range []struct{ answer, wantFile string }{{"allow", "deck-ok"}, {"deny:not today", ""}} {
		id := e.run(p, "mark")
		deadline := time.Now().Add(3 * time.Minute)
		var ask askView
		for time.Now().Before(deadline) {
			_, out := e.call("deck.asks_list", nil)
			var as []askView
			_ = json.Unmarshal(out, &as)
			if len(as) > 0 {
				ask = as[0]
				break
			}
			time.Sleep(300 * time.Millisecond)
		}
		if ask.Kind != "confirm" || !strings.Contains(ask.Text, "marker.txt") {
			t.Fatalf("want a Bash confirmation, got %+v (process %+v)", ask, e.process(id))
		}
		t.Logf("confirm: %s", ask.Text)
		e.answer(ask.ID, tc.answer)
		for time.Now().Before(deadline) && e.process(id).State == "running" {
			time.Sleep(300 * time.Millisecond)
		}
		got := e.process(id)
		b, _ := os.ReadFile(filepath.Join(home, "runs", itoa(id), "marker.txt"))
		t.Logf("%s -> state %s, file %q, result %q", tc.answer, got.State, strings.TrimSpace(string(b)), got.Result)
		if got.State != "done" || strings.TrimSpace(string(b)) != tc.wantFile {
			t.Fatalf("answer %q: state %s, file %q", tc.answer, got.State, b)
		}
	}
}
