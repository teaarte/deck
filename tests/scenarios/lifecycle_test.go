package scenarios

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/api"
	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/internal/store/db"
	"deck2/web"
)

func short(c *app.Config) {
	c.QuestionWait = 100 * time.Millisecond
	c.ConfirmWait = 100 * time.Millisecond
}

func askQuestion(ctx context.Context, c *Call, q string) map[string]any {
	return c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{{"question": q}}})
}

func TestAnswerBetweenTimeoutAndStopIsNotLost(t *testing.T) {
	answered := make(chan struct{})
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if !c.Req.Resume {
			askQuestion(ctx, c, "Which topic?")
			<-answered
			return provider.Result{Text: "stopped"}
		}
		writeIdeas(ctx, c, "late")
		return provider.Result{Structured: json.RawMessage(`{"written": 3}`)}
	}, short)
	p := e.createProject("ideas", ideasManifest)
	id := e.run(p, "collect")
	a := e.waitAsk()
	time.Sleep(200 * time.Millisecond)
	e.answer(a.ID, "go")
	close(answered)
	e.waitState(id, "done")
	if n := len(e.Fake.Calls()); n != 2 {
		t.Fatalf("want the answer delivered by one resume, got %d provider calls", n)
	}
}

func TestTwoLateAnswersResumeOnce(t *testing.T) {
	var running, maxRunning int32
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.Req.Resume {
			n := atomic.AddInt32(&running, 1)
			defer atomic.AddInt32(&running, -1)
			for {
				m := atomic.LoadInt32(&maxRunning)
				if n <= m || atomic.CompareAndSwapInt32(&maxRunning, m, n) {
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
			return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
		}
		askQuestion(ctx, c, "First?")
		askQuestion(ctx, c, "Second?")
		return provider.Result{Text: "stopped"}
	}, short)
	p := e.createProject("ideas", ideasManifest)
	id := e.run(p, "collect")
	e.waitState(id, "waiting_answer")
	asks, _ := e.App.Store.Q.ListOpenAsks(context.Background())
	if len(asks) != 2 {
		t.Fatalf("want 2 open asks, got %d", len(asks))
	}
	var wg sync.WaitGroup
	for _, a := range asks {
		wg.Add(1)
		go func(id int64) { defer wg.Done(); e.answer(id, "yes") }(a.ID)
	}
	wg.Wait()
	e.waitState(id, "done")
	e.App.Runner.Wait()
	if maxRunning != 1 {
		t.Fatalf("one session must never run twice at once, max concurrent = %d", maxRunning)
	}
}

func TestProviderErrorMarksProcessError(t *testing.T) {
	e := newEnv(t, nil)
	e.Fake.script = func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{IsError: true, Text: "boom"}
	}
	p := e.createProject("ideas", ideasManifest)
	got := e.waitState(e.run(p, "collect"), "error", "done")
	if got.State != "error" {
		t.Fatalf("want error, got %+v", got)
	}
}

func TestLateAllowPermitsExactlyThatCallOnce(t *testing.T) {
	var second, third map[string]any
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		call := map[string]any{"command": "make deploy"}
		if !c.Req.Resume {
			c.Approve(ctx, "Bash", call)
			return provider.Result{Text: "stopped"}
		}
		second = c.Approve(ctx, "Bash", call)
		third = c.Approve(ctx, "Bash", call)
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}, short)
	p := e.createProject("ideas", ideasManifest)
	id := e.run(p, "collect")
	e.waitState(id, "waiting_answer")
	a := e.waitAsk()
	e.answer(a.ID, "allow")
	deadline := time.Now().Add(5 * time.Second)
	for len(e.Fake.Calls()) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	e.waitState(id, "waiting_answer")
	e.App.Runner.Wait()
	if second["behavior"] != "allow" {
		t.Fatalf("the allowed call must go through after resume: %v", second)
	}
	if third["behavior"] != "deny" {
		t.Fatalf("allow once must not allow a second identical call: %v", third)
	}
	if !strings.Contains(e.Fake.Calls()[1].Prompt, "Allowed") {
		t.Fatalf("resume message must say the call was allowed: %q", e.Fake.Calls()[1].Prompt)
	}
}

func TestConfirmationExpiresAndProcessIsTold(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if !c.Req.Resume {
			c.Approve(ctx, "Bash", map[string]any{"command": "rm -rf build"})
			return provider.Result{Text: "stopped"}
		}
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}, short, func(c *app.Config) { c.ConfirmTTL = time.Hour })
	p := e.createProject("ideas", ideasManifest)
	id := e.run(p, "collect")
	e.waitState(id, "waiting_answer")
	a := e.waitAsk()
	e.App.Sweep(context.Background(), time.Now().Add(2*time.Hour))
	e.waitState(id, "done")
	if !strings.Contains(e.Fake.Calls()[1].Prompt, "Expired") {
		t.Fatalf("resume must report the expiry: %q", e.Fake.Calls()[1].Prompt)
	}
	code, _ := e.call("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"})
	if code != 409 {
		t.Fatalf("answering an expired confirmation must be refused, got %d", code)
	}
}

func TestShutdownKeepsProcessForResumeAfterRestart(t *testing.T) {
	home := t.TempDir()
	started := make(chan struct{})
	fake := &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		close(started)
		<-ctx.Done()
		return provider.Result{}
	}}
	a1, ts1 := startApp(t, home, fake)
	e := &Env{T: t, App: a1, URL: ts1.URL, Fake: fake, names: map[int64]string{}}
	p := e.createProject("ideas", ideasManifest)
	id := e.run(p, "collect")
	<-started
	_ = a1.Close()
	ts1.Close()

	fake2 := &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}}
	a2, ts2 := startApp(t, home, fake2)
	defer func() { _ = a2.Close(); ts2.Close() }()
	if err := a2.Runner.ResumeInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	e2 := &Env{T: t, App: a2, URL: ts2.URL, Fake: fake2, names: map[int64]string{}}
	e2.waitState(id, "done")
	calls := fake2.Calls()
	if len(calls) != 1 || !calls[0].Resume || calls[0].SessionID != fake.Calls()[0].SessionID || !strings.Contains(calls[0].Prompt, "restarted") {
		t.Fatalf("want resume of the interrupted session, got %+v", calls)
	}
}

func startApp(t *testing.T, home string, p provider.Provider) (*app.App, *httptest.Server) {
	t.Helper()
	ts := httptest.NewUnstartedServer(nil)
	base := "http://" + ts.Listener.Addr().String()
	a, err := app.New(app.Config{Home: home, MCPBase: base, QuestionWait: time.Second, ConfirmWait: time.Second}, p)
	if err != nil {
		t.Fatal(err)
	}
	ts.Config.Handler = api.New(a, token, web.FS()).Handler()
	ts.Start()
	return a, ts
}

func TestRecordWithoutKeyIsRejected(t *testing.T) {
	var out string
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		out, _ = c.Tool(ctx, "records_upsert", map[string]any{"collection": "ideas", "records": jsonl(map[string]any{"score": 5})})
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	})
	p := e.createProject("ideas", strings.Replace(ideasManifest, `"required": ["title", "score"]`, `"required": ["score"]`, 1))
	e.waitState(e.run(p, "collect"), "done")
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "ideas"})
	if len(recs) != 0 || !strings.Contains(out, "key") {
		t.Fatalf("record without a key must be rejected, got %d records, %s", len(recs), out)
	}
}

func TestDuplicateTriggerIsRejected(t *testing.T) {
	e := newEnv(t, nil)
	m := strings.Replace(ideasManifest, `"triggers": [{"command": "collect", "cron": "0 9 * * *"}]`,
		`"triggers": [{"command": "collect", "cron": "0 9 * * *"}, {"command": "collect", "cron": "0 9 * * *"}]`, 1)
	code, out := e.call("deck.project_create", map[string]any{"name": "dup", "manifest": m})
	if code != 422 || !strings.Contains(string(out), "duplicate trigger") {
		t.Fatalf("want duplicate trigger refused, got %d %s", code, out)
	}
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestResumeKeepsTheManifestRevisionTheProcessStartedBy(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if !c.Req.Resume {
			askQuestion(ctx, c, "Go on?")
			return provider.Result{Text: "waiting"}
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 2}`)}
	}, func(c *app.Config) { c.QuestionWait = 50 * time.Millisecond })
	m := testManifest([]string{agentCommand("job", "")}, nil)
	p := e.createProject("pinned", m)
	id := e.run(p, "job")
	e.waitState(id, "waiting_answer")
	changed := strings.Replace(m, `"executor": {"type": "agent", "prompt": "Do job."}, "result": {"type": "object", "properties": {"n": {"type": "integer"}}}`,
		`"executor": {"type": "script", "script": "job"}, "result": {"type": "object", "required": ["s"]}`, 1)
	changed = strings.Replace(changed, `"triggers": []`, `"triggers": [], "scripts": [{"name": "job", "file": "job.py", "runtime": "python"}]`, 1)
	if changed == m {
		t.Fatal("fixture replacement did not apply")
	}
	e.must("deck.manifest_save", map[string]any{"project": "pinned", "manifest": changed}, nil)
	e.answer(e.waitAsk().ID, "yes")
	got := e.waitState(id, "done", "error")
	if got.State != "done" || got.NotBySchema || string(got.Result) != `{"n": 2}` && string(got.Result) != `{"n":2}` {
		t.Fatalf("the answer continues the agent session and checks its result by the revision it started by: %+v", got)
	}
	if calls := e.Fake.Calls(); len(calls) != 2 || !calls[1].Resume {
		t.Fatalf("the same session resumes: %d calls", len(calls))
	}
}

func TestStopThatSucceedsAlwaysEndsTheProcessStopped(t *testing.T) {
	returning := make(chan struct{}, 1)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		returning <- struct{}{}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	p := e.createProject("race", testManifest([]string{agentCommand("job", "")}, nil))
	for i := range 60 {
		id := e.run(p, "job")
		<-returning
		time.Sleep(time.Duration(i%6) * 300 * time.Microsecond)
		code, _ := e.call("deck.process_stop", map[string]any{"process": id})
		got := e.waitState(id, "done", "stopped")
		if code == 200 && got.State != "stopped" || code != 200 && got.State != "done" {
			t.Fatalf("stop answered %d but the process ended %s", code, got.State)
		}
		e.settle()
	}
}
