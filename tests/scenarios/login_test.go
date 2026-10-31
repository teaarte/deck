package scenarios

import (
	"context"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"strings"
	"testing"
	"time"
)

func TestResumeAfterEarlyCrashStartsTheSessionAgain(t *testing.T) {
	t.Parallel()
	c := newCLI(t)
	c.after(`[ "$n" = 1 ] && sleep 300`)
	c.stream(2, "noconversation.jsonl")
	c.stream(3, "ok.jsonl")
	home := t.TempDir()
	e1 := New(t, WithHome(home), WithProvider(c.adapter(home)))
	p := e1.Project("plain", plainManifest)
	id := e1.Run(p, "work")
	sid := e1.session(id)
	e1.Until("the CLI started", func() bool { return c.calls() > 0 })
	_ = e1.App.Close()

	e2 := New(t, WithHome(home), WithProvider(c.adapter(home)))
	e2.Resume()
	if got := e2.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	if v, ok := flag(c.call(2).args, "--resume"); !ok || v != sid {
		t.Fatalf("deck must first resume session %s: %v", sid, c.call(2).args)
	}
	third := c.call(3)
	if v, ok := flag(third.args, "--session-id"); !ok || v != sid || !strings.Contains(third.stdin, "Do the work.") {
		t.Fatalf("a session that never started must start again with its id and the full prompt: %v %q", third.args, third.stdin)
	}
}

func TestLostLoginWaitsForOwnerAndRetries(t *testing.T) {
	t.Parallel()
	c := newCLI(t)
	c.stream(4, "nologin.jsonl")
	c.stream(5, "ok.jsonl")
	e := checkedCLIEnv(t, c, t.TempDir())
	p := e.Project("plain", plainManifest)
	id := e.Run(p, "work")
	e.WaitState(id, "needs_login")
	a := e.WaitAsk()
	if a.Kind != "login" || !strings.Contains(a.Text, "Not logged in") || !strings.Contains(string(a.Payload), "new_session") {
		t.Fatalf("want a login ask with the choices, got %+v", a)
	}
	if h := e.health("provider:claude-code"); h["status"] != "needs_login" || !strings.Contains(h["error"].(string), "Not logged in") {
		t.Fatalf("the provider health must show the lost login: %v", h)
	}
	if code, _ := e.Call("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "yes"}); code != 422 {
		t.Fatalf("a login ask takes only retry or new_session, got %d", code)
	}
	e.Answer(a.ID, "retry")
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	second := c.call(5)
	if v, ok := flag(second.args, "--resume"); !ok || v != e.session(id) || !strings.Contains(second.stdin, "logged in again") {
		t.Fatalf("retry must resume the same session: %v %q", second.args, second.stdin)
	}
	if h := e.health("provider:claude-code"); h["status"] != "ok" {
		t.Fatalf("a successful turn must clear the health: %v", h)
	}
}

func TestLostLoginNewSessionGetsReportOfWhatWasDone(t *testing.T) {
	t.Parallel()
	c := newCLI(t)
	c.stream(4, "nologin.jsonl")
	c.stream(5, "ok.jsonl")
	e := checkedCLIEnv(t, c, t.TempDir())
	p := e.Project("plain", plainManifest)
	id := e.Run(p, "work")
	e.WaitState(id, "needs_login")
	first := e.session(id)
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
	second := c.call(5)
	sid, ok := flag(second.args, "--session-id")
	if !ok || sid == first || sid != e.session(id) {
		t.Fatalf("new_session must start a new session deck assigns: %v, first %s", second.args, first)
	}
	if !strings.Contains(second.stdin, "Do the work.") || !strings.Contains(second.stdin, "- records_upsert: done") {
		t.Fatalf("the new session needs the task and the journal of the old one: %q", second.stdin)
	}
}

func TestRetryAfterLostLoginFinishesWhenTheSessionWasNeverSaved(t *testing.T) {
	t.Parallel()
	c := newCLI(t)
	c.stream(4, "nologin.jsonl")
	c.stream(5, "noconversation.jsonl")
	c.stream(6, "ok.jsonl")
	e := checkedCLIEnv(t, c, t.TempDir())
	p := e.Project("plain", plainManifest)
	id := e.Run(p, "work")
	e.WaitState(id, "needs_login")
	e.Answer(e.WaitAsk().ID, "retry")
	if got := e.WaitState(id, "done", "error"); got.State != "done" || !strings.Contains(string(got.Result), "ok") {
		t.Fatalf("want done with the result, got %+v", got)
	}
}

func TestNewSessionReportSurvivesRestart(t *testing.T) {
	t.Parallel()
	c := newCLI(t)
	c.stream(4, "nologin.jsonl")
	c.after(`[ "$n" = 5 ] && sleep 300`)
	c.stream(6, "noconversation.jsonl")
	c.stream(7, "ok.jsonl")
	home := t.TempDir()
	e1 := checkedCLIEnv(t, c, home)
	p := e1.Project("plain", plainManifest)
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
	Until(t, "the CLI to start 5 times", func() bool { return c.calls() >= 5 })
	_ = e1.App.Close()

	e2 := New(t, WithHome(home), WithProvider(c.adapter(home)))
	e2.Resume()
	if got := e2.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	fourth := c.call(7)
	if sid, _ := flag(fourth.args, "--session-id"); sid != e2.session(id) || !strings.Contains(fourth.stdin, "- records_upsert: done") {
		t.Fatalf("the restarted new session must still get the report: %v %q", fourth.args, fourth.stdin)
	}
}

func TestResultSeenWhileDeckStopsIsNotTheOutcome(t *testing.T) {
	t.Parallel()
	c := newCLI(t)
	c.stream(1, "ok.jsonl")
	c.after(`[ "$n" = 1 ] && sleep 300`)
	c.stream(2, "ok.jsonl")
	home := t.TempDir()
	slow := c.adapter(home)
	slow.Linger = time.Minute
	e1 := New(t, WithHome(home), WithProvider(slow))
	p := e1.Project("plain", plainManifest)
	id := e1.Run(p, "work")
	Until(t, "the CLI to start", func() bool { return c.calls() >= 1 })
	// Deck reads the result line before it stops; no read shows that.
	time.Sleep(200 * time.Millisecond)
	_ = e1.App.Close()

	e2 := New(t, WithHome(home), WithProvider(c.adapter(home)))
	if got := e2.Proc(id); got.State != "running" {
		t.Fatalf("a turn cut by the stop of deck must stay running, got %+v", got)
	}
	e2.Resume()
	e2.WaitState(id, "done")
	if _, ok := flag(c.call(2).args, "--resume"); !ok {
		t.Fatalf("the process must continue its session after the start: %v", c.call(2).args)
	}
}

// checkedCLIEnv is deck on the fake CLI after its first self-check, calls 1 to 3, passed, as the loop of deck runs it
// at start.
func checkedCLIEnv(t *testing.T, c *fakeCLI, home string) *Env {
	t.Helper()
	c.stream(1, "selfcheck.jsonl")
	c.stream(2, "selfcheck_resume.jsonl")
	c.approving()
	e := New(t, WithHome(home), WithProvider(c.adapter(home)))
	e.App.Providers.Checks(context.Background(), time.Now())
	e.WaitHealth("provider:claude-code", "ok")
	Until(t, "the CLI to start 3 times", func() bool { return c.calls() >= 3 })
	return e
}
