package providers

import (
	"context"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestProviderLimitPausesAndContinuesTheSameSession(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var prompts []string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		mu.Lock()
		prompts = append(prompts, c.Req.Prompt)
		mu.Unlock()
		if c.N == 1 {
			return harness.Limited(harness.T0.Add(2 * time.Hour))
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}), harness.WithClock())

	p := e.Project("plain", harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil))
	id := e.Run(p, "work")
	e.WaitState(id, "rate_limited")
	e.Clock.Add(time.Hour)
	e.Sweep()
	e.Settle()
	if got := e.Proc(id); got.State != "rate_limited" || got.Deadline != "" || got.SilentSince != "" || len(e.Fake.Calls()) != 1 {
		t.Fatalf("before the reset the process waits, its ceiling and silence stand still: %+v, %d calls", got, len(e.Fake.Calls()))
	}
	e.Clock.Add(time.Hour)
	e.Sweep()
	got := e.WaitState(id, "done", "error")
	calls := e.Fake.Calls()
	if got.State != "done" || len(calls) != 2 || !calls[1].Resume || calls[1].SessionID != calls[0].SessionID ||
		!strings.Contains(prompts[1], "limit should have reset") {
		t.Fatalf("at the reset the same session goes on: %+v, %d calls", got, len(calls))
	}
	var hs []map[string]any
	e.Must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if h["subject"] == "provider:fake" && h["status"] != "ok" {
			t.Fatalf("an answer of the model lifts the refusal: %v", h)
		}
	}
}

// TestAPauseWithoutAResetTimeProbesHourlyUntilTheOwnerStopsIt: no end of its own; the owner stops it, and a
// rework goes on in the same session.
func TestAPauseWithoutAResetTimeProbesHourlyUntilTheOwnerStopsIt(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	ok := false
	cf := &checkedFake{Fake: &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		mu.Lock()
		defer mu.Unlock()
		if !ok {
			return harness.Limited(time.Time{})
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 2}`)}
	}}}
	clock := &harness.Clock{At: harness.T0}
	e := harness.New(t, harness.WithProvider(cf), harness.WithClock(clock))
	e.Fake = cf.Fake
	p := e.Project("plain", harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil))
	id := e.Run(p, "work")
	e.WaitState(id, "rate_limited")
	for h := 1; h <= 9; h++ {
		e.Clock.Add(30 * time.Minute)
		e.Sweep()
		e.Settle()
		if n := len(e.Fake.Calls()); n != h {
			t.Fatalf("no probe before the hour is up: %d calls after %dh30", n, h-1)
		}
		e.Clock.Add(30 * time.Minute)
		e.Sweep()
		e.Until("the hourly probe", func() bool { return len(e.Fake.Calls()) == h+1 })
		e.WaitState(id, "rate_limited")
	}
	var as []harness.Ask
	e.Must("deck.asks_list", nil, &as)
	if len(as) != 0 {
		t.Fatalf("a pause asks the owner nothing: %+v", as)
	}
	e.Must("deck.process_stop", map[string]any{"process": id}, nil)
	e.WaitState(id, "stopped")
	e.Clock.Add(2 * time.Hour)
	e.Sweep()
	e.Settle()
	if n := len(e.Fake.Calls()); n != 10 {
		t.Fatalf("a stopped pause probes no more: %d calls", n)
	}
	mu.Lock()
	ok = true
	mu.Unlock()
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	e.WaitHealth("provider:fake", "ok")
	e.Must("deck.process_rework", map[string]any{"process": id, "remark": "go on"}, nil)
	got := e.WaitState(id, "done", "waiting_answer", "error")
	calls := e.Fake.Calls()
	if got.State != "done" || string(got.Result) != `{"n":2}` || !calls[10].Resume || calls[10].SessionID != calls[0].SessionID {
		t.Fatalf("a rework after the owner stopped the pause resumes the same session: %+v", got)
	}
}

// TestAnAnswerDuringAPauseReachesTheNextTry: the owner's answer waits with the process and goes with its next try.
func TestAnAnswerDuringAPauseReachesTheNextTry(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var prompts []string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		mu.Lock()
		prompts = append(prompts, c.Req.Prompt)
		mu.Unlock()
		if c.N == 1 {
			c.Tool(ctx, "notes__post", map[string]any{"text": "hi"})
			return harness.Limited(time.Time{})
		}
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	}), harness.WithClock())

	p := e.Project("notes", strings.Replace(harness.NotesManifest, `"requires": ["no_cloud"],`, "", 1))
	id := e.Run(p, "collect")
	e.WaitState(id, "rate_limited")
	e.Answer(e.WaitAsk().ID, "deny")
	e.Settle()
	if n := len(e.Fake.Calls()); n != 1 {
		t.Fatalf("the answer waits for the next try: %d calls", n)
	}
	e.Clock.Add(time.Hour)
	e.Sweep()
	e.WaitState(id, "done", "error")
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 2 || !strings.Contains(prompts[1], "limit should have reset") || !strings.Contains(prompts[1], "notes.post") {
		t.Fatalf("the next try carries the answer: %q", prompts)
	}
}
