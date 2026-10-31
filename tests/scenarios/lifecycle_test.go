package scenarios

import (
	"context"
	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/internal/store/db"
	"deck2/tests/harness"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func askQuestion(ctx context.Context, c *harness.Call, q string) map[string]any {
	return c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{{"question": q}}})
}

func TestAnswerBetweenTimeoutAndStopIsNotLost(t *testing.T) {
	t.Parallel()
	asked, answered := make(chan struct{}), make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if !c.Req.Resume {
			askQuestion(ctx, c, "Which topic?")
			close(asked)
			<-answered
			return provider.Result{Text: "stopped"}
		}
		writeIdeas(ctx, c, "late")
		return provider.Result{Structured: json.RawMessage(`{"written": 3}`)}
	}), harness.WithClock())

	p := e.Project("ideas", ideasManifest)
	id := e.Run(p, "collect")
	a := e.WaitAsk()
	e.PassWaits("the question's wait to pass while the turn still runs", func() bool {
		select {
		case <-asked:
			return true
		default:
			return false
		}
	})
	e.Answer(a.ID, "go")
	close(answered)
	e.WaitState(id, "done")
	if n := len(e.Fake.Calls()); n != 2 {
		t.Fatalf("want the answer delivered by one resume, got %d provider calls", n)
	}
}

func TestTwoLateAnswersResumeOnce(t *testing.T) {
	t.Parallel()
	var running, maxRunning int32
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
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
	}), harness.WithClock())

	p := e.Project("ideas", ideasManifest)
	id := e.Run(p, "collect")
	e.PassState(id, "waiting_answer")
	var asks []harness.Ask
	e.Must("deck.asks_list", nil, &asks)
	if len(asks) != 2 {
		t.Fatalf("want 2 open asks, got %d", len(asks))
	}
	var wg sync.WaitGroup
	for _, a := range asks {
		wg.Add(1)
		go func(id int64) { defer wg.Done(); e.Answer(id, "yes") }(a.ID)
	}
	wg.Wait()
	e.WaitState(id, "done")
	e.App.Runner.Wait()
	if maxRunning != 1 {
		t.Fatalf("one session must never run twice at once, max concurrent = %d", maxRunning)
	}
}

func TestProviderErrorMarksProcessError(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Fake.Script = func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: "boom"}
	}
	p := e.Project("ideas", ideasManifest)
	got := e.WaitState(e.Run(p, "collect"), "error", "done")
	if got.State != "error" {
		t.Fatalf("want error, got %+v", got)
	}
}

func TestLateAllowPermitsExactlyThatCallOnce(t *testing.T) {
	t.Parallel()
	var second, third map[string]any
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		call := map[string]any{"command": "make deploy"}
		if !c.Req.Resume {
			c.Approve(ctx, "Bash", call)
			return provider.Result{Text: "stopped"}
		}
		second = c.Approve(ctx, "Bash", call)
		third = c.Approve(ctx, "Bash", call)
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}), harness.WithClock())

	p := e.Project("ideas", ideasManifest)
	id := e.Run(p, "collect")
	e.PassState(id, "waiting_answer")
	a := e.WaitAsk()
	e.Answer(a.ID, "allow")
	e.Until("the resume", func() bool { return len(e.Fake.Calls()) >= 2 })
	e.PassState(id, "waiting_answer")
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
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if !c.Req.Resume {
			c.Approve(ctx, "Bash", map[string]any{"command": "rm -rf build"})
			return provider.Result{Text: "stopped"}
		}
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}), harness.WithClock(), harness.WithConfig(

		func(c *app.Config) { c.ConfirmTTL = time.Hour }))

	p := e.Project("ideas", ideasManifest)
	id := e.Run(p, "collect")
	e.PassState(id, "waiting_answer")
	a := e.WaitAsk()
	e.Clock.Add(2 * time.Hour)
	e.Sweep()
	e.WaitState(id, "done")
	if !strings.Contains(e.Fake.Calls()[1].Prompt, "Expired") {
		t.Fatalf("resume must report the expiry: %q", e.Fake.Calls()[1].Prompt)
	}
	code, _ := e.Call("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"})
	if code != 409 {
		t.Fatalf("answering an expired confirmation must be refused, got %d", code)
	}
}

func TestShutdownKeepsProcessForResumeAfterRestart(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	started := make(chan struct{})
	fake := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		close(started)
		<-ctx.Done()
		return provider.Result{}
	}}
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(fake), harness.WithConfig(func(c *app.Config) { c.QuestionWait, c.ConfirmWait = time.Second, time.Second }))
	p := e.Project("ideas", ideasManifest)
	id := e.Run(p, "collect")
	<-started
	_ = e.App.Close()

	fake2 := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}}
	e2 := e.Restart(harness.WithProvider(fake2))
	e2.Resume()
	e2.WaitState(id, "done")
	calls := fake2.Calls()
	if len(calls) != 1 || !calls[0].Resume || calls[0].SessionID != fake.Calls()[0].SessionID || !strings.Contains(calls[0].Prompt, "restarted") {
		t.Fatalf("want resume of the interrupted session, got %+v", calls)
	}
}

func TestRecordWithoutKeyIsRejected(t *testing.T) {
	t.Parallel()
	var out string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		out, _ = c.Tool(ctx, "records_upsert", map[string]any{"collection": "ideas", "records": harness.Jsonl(map[string]any{"score": 5})})
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}))

	p := e.Project("ideas", strings.Replace(ideasManifest, `"required": ["title", "score"]`, `"required": ["score"]`, 1))
	e.WaitState(e.Run(p, "collect"), "done")
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "ideas"})
	if len(recs) != 0 || !strings.Contains(out, "key") {
		t.Fatalf("record without a key must be rejected, got %d records, %s", len(recs), out)
	}
}

func TestDuplicateTriggerIsRejected(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	m := strings.Replace(ideasManifest, `"triggers": [{"command": "collect", "cron": "0 9 * * *"}]`,
		`"triggers": [{"command": "collect", "cron": "0 9 * * *"}, {"command": "collect", "cron": "0 9 * * *"}]`, 1)
	code, out := e.Call("deck.project_create", map[string]any{"name": "dup", "manifest": m})
	if code != 422 || !strings.Contains(string(out), "duplicate trigger") {
		t.Fatalf("want duplicate trigger refused, got %d %s", code, out)
	}
}

func TestResumeKeepsTheManifestRevisionTheProcessStartedBy(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if !c.Req.Resume {
			askQuestion(ctx, c, "Go on?")
			return provider.Result{Text: "waiting"}
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 2}`)}
	}), harness.WithConfig(

		func(c *app.Config) { c.QuestionWait = 50 * time.Millisecond }))

	m := harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil)
	p := e.Project("pinned", m)
	id := e.Run(p, "job")
	e.WaitState(id, "waiting_answer")
	changed := strings.Replace(m, `"executor": {"type": "agent", "prompt": "Do job."}, "result": {"type": "object", "properties": {"n": {"type": "integer"}}}`,
		`"executor": {"type": "script", "script": "job"}, "result": {"type": "object", "required": ["s"]}`, 1)
	changed = strings.Replace(changed, `"triggers": []`, `"triggers": [], "scripts": [{"name": "job", "file": "job.py", "runtime": "python"}]`, 1)
	if changed == m {
		t.Fatal("fixture replacement did not apply")
	}
	e.Must("deck.manifest_save", map[string]any{"project": "pinned", "manifest": changed}, nil)
	e.Answer(e.WaitAsk().ID, "yes")
	got := e.WaitState(id, "done", "error")
	if got.State != "done" || got.NotBySchema || string(got.Result) != `{"n": 2}` && string(got.Result) != `{"n":2}` {
		t.Fatalf("the answer continues the agent session and checks its result by the revision it started by: %+v", got)
	}
	if calls := e.Fake.Calls(); len(calls) != 2 || !calls[1].Resume {
		t.Fatalf("the same session resumes: %d calls", len(calls))
	}
}

func TestStopThatSucceedsAlwaysEndsTheProcessStopped(t *testing.T) {
	t.Parallel()
	returning := make(chan struct{}, 1)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		returning <- struct{}{}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))

	p := e.Project("race", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	for i := range 60 {
		id := e.Run(p, "job")
		<-returning
		time.Sleep(time.Duration(i%6) * 300 * time.Microsecond)
		code, _ := e.Call("deck.process_stop", map[string]any{"process": id})
		got := e.WaitState(id, "done", "stopped")
		if code == 200 && got.State != "stopped" || code != 200 && got.State != "done" {
			t.Fatalf("stop answered %d but the process ended %s", code, got.State)
		}
		e.Settle()
	}
}

func TestAnswerSavedBeforeRestartReachesParkedProcess(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	fake := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		askQuestion(ctx, c, "Which topic?")
		return provider.Result{Text: "stopped"}
	}}
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(fake), harness.WithConfig(func(c *app.Config) { c.QuestionWait, c.ConfirmWait = time.Second, time.Second }))
	p := e.Project("ideas", ideasManifest)
	id := e.Run(p, "collect")
	e.WaitState(id, "waiting_answer")
	a := e.WaitAsk()
	_ = e.App.Close()

	fake2 := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}}
	e2 := e.Restart(harness.WithProvider(fake2))
	if err := e2.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		_, err := q.AnswerAsk(ctx, db.AnswerAskParams{Answer: "rust", ID: a.ID})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	e2.Resume()
	e2.WaitState(id, "done")
	if calls := fake2.Calls(); len(calls) != 1 || !strings.Contains(calls[0].Prompt, "rust") {
		t.Fatalf("the saved answer must reach the parked process after restart, got %+v", calls)
	}
}

func TestResumeAfterItsCommandWasRenamedFinishesTheSession(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Req.Resume {
			return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
		}
		askQuestion(ctx, c, "Which topic?")
		return provider.Result{Text: "stopped"}
	}), harness.WithClock())

	p := e.Project("ideas", ideasManifest)
	id := e.Run(p, "collect")
	e.PassState(id, "waiting_answer")
	a := e.WaitAsk()
	renamed := strings.NewReplacer(`"name": "collect"`, `"name": "gather"`, `"command": "collect"`, `"command": "gather"`).Replace(ideasManifest)
	e.Must("deck.manifest_save", map[string]any{"project": "ideas", "manifest": renamed}, nil)
	e.Answer(a.ID, "go")
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the session goes on by the manifest revision it started by: %+v", got)
	}
}

// TestAStopEndsTheTurnsBeforeTheServerGoes: a stop of deck ends the turns of agents and its background work
// first, while deck still answers, so no agent loses deck's tools in the middle of a turn.
func TestAStopEndsTheTurnsBeforeTheServerGoes(t *testing.T) {
	t.Parallel()
	started, ended := make(chan struct{}), make(chan struct{})
	// Run resumes what runs as at a start of deck, so a second turn of the same process may begin before the stop.
	var once, end sync.Once
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		once.Do(func() { close(started) })
		<-ctx.Done()
		end.Do(func() { close(ended) })
		return provider.Result{}
	}))

	p := e.Project("long", harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil))
	id := e.Run(p, "work")
	<-started
	e.App.Run(t.Context(), time.Hour)
	e.App.Stop()
	select {
	case <-ended:
	default:
		t.Fatal("the turn has ended when the stop returns")
	}
	if code, out := e.Call("deck.process_get", map[string]any{"process": id}); code != 200 || !strings.Contains(string(out), `"running"`) {
		t.Fatalf("deck still answers after the stop, and the turn resumes on the next start: %d %s", code, out)
	}
}
