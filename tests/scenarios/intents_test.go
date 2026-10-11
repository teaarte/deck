package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/internal/provider"
)

// hangingService answers the requests numbered in hang only when released, like a service deck lost the
// answer of; release(0) frees all of them.
func hangingService(t *testing.T, hang ...int) (*service, func(n int)) {
	t.Helper()
	gates := map[int]chan struct{}{}
	for _, n := range hang {
		gates[n] = make(chan struct{})
	}
	var mu sync.Mutex
	release := func(n int) {
		mu.Lock()
		defer mu.Unlock()
		for k, g := range gates {
			if n == 0 || n == k {
				close(g)
				delete(gates, k)
			}
		}
	}
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		mu.Lock()
		g := gates[n]
		mu.Unlock()
		if g != nil {
			<-g
		}
		jsonReply(w, fmt.Sprintf(`{"id": "job-%d"}`, n))
	})
	t.Cleanup(func() { release(0) })
	return svc, release
}

func generateManifest(url string) string {
	return fmt.Sprintf(`{"version": 12,
  "commands": [
    {"name": "make", "title": "Make", "description": "Starts a paid generation.", "class": "external",
     "input": {"type": "object", "properties": {"topic": {"type": "string"}}},
     "executor": {"type": "tool", "tool": "generate"}},
    {"name": "work", "title": "Work", "description": "Asks for a generation.", "class": "internal",
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Generate."}}],
  "triggers": [{"command": "make", "cron": "0 9 * * *", "params": {"topic": "cats"}}],
  "tools": {"http": [{"name": "generate", "description": "Starts a generation", "method": "POST", "url": "%s/gen",
    "input": {"type": "object", "properties": {"topic": {"type": "string"}}}}]},
  "allowed_external": ["generate", "make"]}`, url)
}

// restart opens a second deck on the same home while the first one hangs, as after a crash of the first.
func restart(t *testing.T, e *Env, home string, fake *Fake) *Env {
	t.Helper()
	e2 := newEnvWith(t, home, fake, withClock(e.Clock))
	e2.Fake, e2.Clock = fake, e.Clock
	for id, n := range e.names {
		e2.names[id] = n
	}
	if err := e2.App.Runner.ResumeInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	return e2
}

func TestUnknownOutcomeIsNotRepeatedAfterARestartNorByACatchUpRun(t *testing.T) {
	t.Parallel()
	svc, release := hangingService(t, 1, 2)
	defer release(0)
	home := t.TempDir()
	clock := &Clock{now: time.Date(2026, 9, 25, 8, 0, 0, 0, time.Local)}
	e := newEnvWith(t, home, &Fake{}, withClock(clock))
	e.Clock = clock
	e.createProject("clips", generateManifest(svc.URL))
	clock.Add(time.Hour + time.Minute)
	e.App.Scheduler.Tick(context.Background())
	e.waitFor("the call reaches the service", func() bool { return len(svc.requests()) == 1 })

	e2 := restart(t, e, home, &Fake{})
	var unknown askView
	e2.waitFor("the unknown outcome", func() bool {
		_, out := e2.call("deck.asks_list", nil)
		var as []askView
		_ = json.Unmarshal(out, &as)
		for _, a := range as {
			if a.Kind == "unknown" {
				unknown = a
			}
		}
		return unknown.ID != 0
	})
	if !strings.Contains(unknown.Text, "clips.generate") || !strings.Contains(unknown.Text, "cats") {
		t.Fatalf("the owner sees the call and its parameters: %+v", unknown)
	}
	first := e2.processes()[0]
	if first.State != "interrupted" {
		t.Fatalf("the tool process of the lost call is interrupted, not rerun: %+v", first)
	}
	clock.Add(24 * time.Hour)
	e2.App.Scheduler.Tick(context.Background())
	e2.waitFor("the catch-up run ends", func() bool {
		ps := e2.processes()
		return len(ps) == 2 && ps[0].State == "error"
	})
	if ps := e2.processes(); !strings.Contains(ps[0].Error, "unknown outcome") || len(svc.requests()) != 1 {
		t.Fatalf("a catch-up run does not repeat the call: %+v, %d requests", ps[0], len(svc.requests()))
	}
	var out struct{ Result map[string]any }
	retried := make(chan struct{})
	go func() {
		defer close(retried)
		e2.must("deck.ask_answer", map[string]any{"ask": unknown.ID, "answer": "retry"}, &out)
	}()
	e2.waitFor("the retry reaches the service", func() bool { return len(svc.requests()) == 2 })
	clock.Add(24 * time.Hour)
	e2.App.Scheduler.Tick(context.Background())
	e2.waitFor("the catch-up run during the retry ends", func() bool {
		ps := e2.processes()
		return len(ps) == 3 && ps[0].State == "error"
	})
	if len(svc.requests()) != 2 {
		t.Fatalf("a catch-up run does not repeat the call while the owner's retry is in flight: %d requests", len(svc.requests()))
	}
	release(2)
	<-retried
	if len(svc.requests()) != 2 || out.Result["id"] != "job-2" {
		t.Fatalf("retry makes the frozen call again: %+v, %d requests", out, len(svc.requests()))
	}
	release(1)
	e.App.Runner.Wait()
	var outcome string
	_ = e2.App.Store.DB.QueryRow(`SELECT outcome FROM tool_call WHERE tool = 'clips.generate' ORDER BY id LIMIT 1`).Scan(&outcome)
	if outcome != "unknown" {
		t.Fatalf("a late answer to the lost deck does not rewrite the unknown outcome: %q", outcome)
	}
}

func TestUnknownOutcomeIsNotRepeatedOnResume(t *testing.T) {
	t.Parallel()
	svc, release := hangingService(t, 1)
	defer release(0)
	home := t.TempDir()
	started := make(chan struct{})
	first := &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		close(started)
		c.Tool(ctx, "clips__generate", map[string]any{"topic": "dogs"})
		<-ctx.Done()
		return provider.Result{}
	}}
	clock := &Clock{now: t0}
	e := newEnvWith(t, home, first, withClock(clock))
	e.Fake, e.Clock = first, clock
	p := e.createProject("clips", generateManifest(svc.URL))
	id := e.run(p, "work")
	<-started
	e.waitFor("the call reaches the service", func() bool { return len(svc.requests()) == 1 })

	var again string
	second := &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		if c.N == 1 {
			again, _ = c.Tool(ctx, "clips__generate", map[string]any{"topic": "dogs"})
		}
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}}
	e2 := restart(t, e, home, second)
	e2.waitState(id, "waiting_answer")
	msg := second.Calls()[0].Prompt
	if !second.Calls()[0].Resume || !strings.Contains(msg, "Outcome unknown") || !strings.Contains(msg, "Do not make this call again") {
		t.Fatalf("the resumed agent is told the outcome is unknown: %q", msg)
	}
	if !strings.Contains(again, "unknown outcome") || len(svc.requests()) != 1 {
		t.Fatalf("the resumed agent cannot repeat the call: %q, %d requests", again, len(svc.requests()))
	}
	e2.answer(e2.waitAsk().ID, "done")
	e2.waitState(id, "done")
	if msg := second.Calls()[1].Prompt; !strings.Contains(msg, "counts it as done") || len(svc.requests()) != 1 {
		t.Fatalf("the owner's done reaches the agent and nothing is repeated: %q", msg)
	}
}

func TestAnAllowedCallCutOffByARestartBecomesAnUnknownOutcome(t *testing.T) {
	t.Parallel()
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{}`) })
	home := t.TempDir()
	first := &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		c.Tool(ctx, "notes__post", map[string]any{"id": "a", "text": "hi"})
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}}
	e := newEnvWith(t, home, first)
	e.Fake = first
	e.must("deck.secret_set", map[string]any{"name": "svc", "value": "k"}, nil)
	p := e.createProject("notes", postManifest(svc.URL, ""))
	id := e.run(p, "work")
	e.waitState(id, "waiting_answer")
	a := e.waitAsk()
	_ = e.App.Close()
	second := &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}}
	e2 := newEnvWith(t, home, second)
	if _, err := e2.App.Store.DB.Exec(`UPDATE ask SET state = 'answered', answer = 'allow' WHERE id = ?;
UPDATE tool_call SET run = 'running' WHERE ask_id = ?`, a.ID, a.ID); err != nil {
		t.Fatal(err)
	}
	if err := e2.App.Runner.ResumeInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := e2.waitAsk()
	if got.ID == a.ID || got.Kind != "unknown" || !strings.Contains(got.Text, "Outcome unknown") || len(svc.requests()) != 0 {
		t.Fatalf("a call cut off after yes and before it was sent is an unknown outcome for the owner: %+v", got)
	}
	e2.answer(got.ID, "retry")
	if p := e2.waitState(id, "done", "error"); p.State != "done" || len(svc.requests()) != 1 {
		t.Fatalf("a retry makes the call once and the agent goes on: %+v %d", p, len(svc.requests()))
	}
	if c := second.Calls(); !strings.Contains(c[len(c)-1].Prompt, "retry a call of unknown outcome") {
		t.Fatalf("the agent hears what the retry gave: %s", c[len(c)-1].Prompt)
	}
}

// TestUnknownOutcomeKeepsItsCaller: a lost call of an automation token comes back as the token's, so a retry
// is journaled under it and its command list still applies.
func TestUnknownOutcomeKeepsItsCaller(t *testing.T) {
	t.Parallel()
	svc, release := hangingService(t, 1)
	defer release(0)
	home := t.TempDir()
	clock := &Clock{now: t0}
	e := newEnvWith(t, home, &Fake{}, withClock(clock))
	e.Clock = clock
	e.createProject("clips", generateManifest(svc.URL))
	tid, tok := e.token("shortcuts", "clips.*")
	go func() {
		req, _ := http.NewRequest("POST", e.URL+"/api/commands/clips.generate", strings.NewReader(`{"topic": "owls"}`))
		req.Header.Set("Authorization", "Bearer "+tok)
		if r, err := http.DefaultClient.Do(req); err == nil {
			r.Body.Close()
		}
	}()
	e.waitFor("the call reaches the service", func() bool { return len(svc.requests()) == 1 })
	e2 := restart(t, e, home, &Fake{})
	a := e2.waitAsk()
	var frozen struct {
		Caller struct {
			Kind     string
			Token    int64
			Commands []string
		}
	}
	_ = json.Unmarshal(a.Payload, &frozen)
	if a.Kind != "unknown" || frozen.Caller.Kind != "token" || frozen.Caller.Token != tid || len(frozen.Caller.Commands) != 1 {
		t.Fatalf("the unknown outcome is the token's, not the owner's: %+v %s", a, a.Payload)
	}
}

// TestAnAllowedCallLostWithDeckIsOneUnknownOutcome: deck stops while making a call the owner allowed; after the
// restart the owner has one unknown outcome to decide, and the agent hears that the call did not succeed.
func TestAnAllowedCallLostWithDeckIsOneUnknownOutcome(t *testing.T) {
	t.Parallel()
	svc, release := hangingService(t, 1)
	defer release(0)
	home := t.TempDir()
	first := &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		c.Tool(ctx, "notes__post", map[string]any{"id": "a", "text": "hi"})
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}}
	clock := &Clock{now: t0}
	e := newEnvWith(t, home, first, withClock(clock))
	e.Fake, e.Clock = first, clock
	e.must("deck.secret_set", map[string]any{"name": "svc", "value": "k"}, nil)
	id := e.run(e.createProject("notes", postManifest(svc.URL, "")), "work")
	e.waitState(id, "waiting_answer")
	a := e.waitAsk()
	go func() {
		body := strings.NewReader(fmt.Sprintf(`{"ask": %d, "answer": "allow"}`, a.ID))
		req, _ := http.NewRequest("POST", e.URL+"/api/commands/deck.ask_answer", body)
		req.Header.Set("Authorization", "Bearer "+token)
		if r, err := http.DefaultClient.Do(req); err == nil {
			r.Body.Close()
		}
	}()
	e.waitFor("the allowed call reaches the service", func() bool { return len(svc.requests()) == 1 })
	second := &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}}
	e2 := restart(t, e, home, second)
	e2.waitFor("the unknown outcome", func() bool {
		_, out := e2.call("deck.asks_list", nil)
		var as []askView
		_ = json.Unmarshal(out, &as)
		return len(as) > 0
	})
	e2.App.Runner.Wait()
	_, out := e2.call("deck.asks_list", nil)
	var as []askView
	_ = json.Unmarshal(out, &as)
	if len(as) != 1 || as[0].Kind != "unknown" {
		t.Fatalf("one unknown outcome for the owner: %+v", as)
	}
}
