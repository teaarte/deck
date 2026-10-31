package providers

import (
	"context"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/tests/harness"
	"strings"
	"testing"
	"time"
)

func TestResumeAfterEarlyCrashStartsTheSessionAgain(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.After(`[ "$n" = 1 ] && sleep 300`)
	c.Stream(2, "noconversation.jsonl")
	c.Stream(3, "ok.jsonl")
	home := t.TempDir()
	e1 := harness.New(t, harness.WithHome(home), harness.WithProvider(c.Adapter(home)))
	p := e1.Project("plain", harness.PlainManifest)
	id := e1.Run(p, "work")
	sid := e1.Session(id)
	e1.Until("the CLI started", func() bool { return c.Calls() > 0 })
	_ = e1.App.Close()

	e2 := harness.New(t, harness.WithHome(home), harness.WithProvider(c.Adapter(home)))
	e2.Resume()
	if got := e2.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	if v, ok := harness.Flag(c.Call(2).Args, "--resume"); !ok || v != sid {
		t.Fatalf("deck must first resume session %s: %v", sid, c.Call(2).Args)
	}
	third := c.Call(3)
	if v, ok := harness.Flag(third.Args, "--session-id"); !ok || v != sid || !strings.Contains(third.Stdin, "Do the work.") {
		t.Fatalf("a session that never started must start again with its id and the full prompt: %v %q", third.Args, third.Stdin)
	}
}

func TestLostLoginWaitsForOwnerAndRetries(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(4, "nologin.jsonl")
	c.Stream(5, "ok.jsonl")
	e := checkedCLIEnv(t, c, t.TempDir())
	p := e.Project("plain", harness.PlainManifest)
	id := e.Run(p, "work")
	e.WaitState(id, "needs_login")
	a := e.WaitAsk()
	if a.Kind != "login" || !strings.Contains(a.Text, "Not logged in") || !strings.Contains(string(a.Payload), "new_session") {
		t.Fatalf("want a login ask with the choices, got %+v", a)
	}
	if h := e.HealthRowOf("provider:claude-code"); h["status"] != "needs_login" || !strings.Contains(h["error"].(string), "Not logged in") {
		t.Fatalf("the provider health must show the lost login: %v", h)
	}
	if code, _ := e.Call("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "yes"}); code != 422 {
		t.Fatalf("a login ask takes only retry or new_session, got %d", code)
	}
	e.Answer(a.ID, "retry")
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	second := c.Call(5)
	if v, ok := harness.Flag(second.Args, "--resume"); !ok || v != e.Session(id) || !strings.Contains(second.Stdin, "logged in again") {
		t.Fatalf("retry must resume the same session: %v %q", second.Args, second.Stdin)
	}
	if h := e.HealthRowOf("provider:claude-code"); h["status"] != "ok" {
		t.Fatalf("a successful turn must clear the health: %v", h)
	}
}

func TestLostLoginNewSessionGetsReportOfWhatWasDone(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(4, "nologin.jsonl")
	c.Stream(5, "ok.jsonl")
	e := checkedCLIEnv(t, c, t.TempDir())
	p := e.Project("plain", harness.PlainManifest)
	id := e.Run(p, "work")
	e.WaitState(id, "needs_login")
	first := e.Session(id)
	err := e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		_, err := q.LogToolCall(ctx, db.LogToolCallParams{CreatedAt: store.Time(time.Now()), WorkspaceID: 1, ProcessID: store.ID(id), ProjectID: store.ID(p), Tool: "records_upsert", Class: "internal", Outcome: "done"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	e.Answer(e.WaitAsk().ID, "new_session")
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	second := c.Call(5)
	sid, ok := harness.Flag(second.Args, "--session-id")
	if !ok || sid == first || sid != e.Session(id) {
		t.Fatalf("new_session must start a new session deck assigns: %v, first %s", second.Args, first)
	}
	if !strings.Contains(second.Stdin, "Do the work.") || !strings.Contains(second.Stdin, "- records_upsert: done") {
		t.Fatalf("the new session needs the task and the journal of the old one: %q", second.Stdin)
	}
}

func TestRetryAfterLostLoginFinishesWhenTheSessionWasNeverSaved(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(4, "nologin.jsonl")
	c.Stream(5, "noconversation.jsonl")
	c.Stream(6, "ok.jsonl")
	e := checkedCLIEnv(t, c, t.TempDir())
	p := e.Project("plain", harness.PlainManifest)
	id := e.Run(p, "work")
	e.WaitState(id, "needs_login")
	e.Answer(e.WaitAsk().ID, "retry")
	if got := e.WaitState(id, "done", "error"); got.State != "done" || !strings.Contains(string(got.Result), "ok") {
		t.Fatalf("want done with the result, got %+v", got)
	}
}

func TestNewSessionReportSurvivesRestart(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(4, "nologin.jsonl")
	c.After(`[ "$n" = 5 ] && sleep 300`)
	c.Stream(6, "noconversation.jsonl")
	c.Stream(7, "ok.jsonl")
	home := t.TempDir()
	e1 := checkedCLIEnv(t, c, home)
	p := e1.Project("plain", harness.PlainManifest)
	id := e1.Run(p, "work")
	e1.WaitState(id, "needs_login")
	err := e1.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		_, err := q.LogToolCall(ctx, db.LogToolCallParams{CreatedAt: store.Time(time.Now()), WorkspaceID: 1, ProcessID: store.ID(id), ProjectID: store.ID(p), Tool: "records_upsert", Class: "internal", Outcome: "done"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	e1.Answer(e1.WaitAsk().ID, "new_session")
	harness.Until(t, "the CLI to start 5 times", func() bool { return c.Calls() >= 5 })
	_ = e1.App.Close()

	e2 := harness.New(t, harness.WithHome(home), harness.WithProvider(c.Adapter(home)))
	e2.Resume()
	if got := e2.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	fourth := c.Call(7)
	if sid, _ := harness.Flag(fourth.Args, "--session-id"); sid != e2.Session(id) || !strings.Contains(fourth.Stdin, "- records_upsert: done") {
		t.Fatalf("the restarted new session must still get the report: %v %q", fourth.Args, fourth.Stdin)
	}
}

func TestResultSeenWhileDeckStopsIsNotTheOutcome(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(1, "ok.jsonl")
	c.After(`[ "$n" = 1 ] && sleep 300`)
	c.Stream(2, "ok.jsonl")
	home := t.TempDir()
	slow := c.Adapter(home)
	slow.Linger = time.Minute
	e1 := harness.New(t, harness.WithHome(home), harness.WithProvider(slow))
	p := e1.Project("plain", harness.PlainManifest)
	id := e1.Run(p, "work")
	harness.Until(t, "the CLI to start", func() bool { return c.Calls() >= 1 })
	// Deck reads the result line before it stops; no read shows that.
	time.Sleep(200 * time.Millisecond)
	_ = e1.App.Close()

	e2 := harness.New(t, harness.WithHome(home), harness.WithProvider(c.Adapter(home)))
	if got := e2.Proc(id); got.State != "running" {
		t.Fatalf("a turn cut by the stop of deck must stay running, got %+v", got)
	}
	e2.Resume()
	e2.WaitState(id, "done")
	if _, ok := harness.Flag(c.Call(2).Args, "--resume"); !ok {
		t.Fatalf("the process must continue its session after the start: %v", c.Call(2).Args)
	}
}

// checkedCLIEnv is deck on the fake CLI after its first self-check, calls 1 to 3, passed, as the loop of deck runs it
// at start.
func checkedCLIEnv(t *testing.T, c *harness.FakeCLI, home string) *harness.Env {
	t.Helper()
	c.Stream(1, "selfcheck.jsonl")
	c.Stream(2, "selfcheck_resume.jsonl")
	c.Approving()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(c.Adapter(home)))
	e.App.Providers.Checks(context.Background(), time.Now())
	e.WaitHealth("provider:claude-code", "ok")
	harness.Until(t, "the CLI to start 3 times", func() bool { return c.Calls() >= 3 })
	return e
}
