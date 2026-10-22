package scenarios

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/internal/provider"
)

func limited(resetsAt time.Time) provider.Result {
	r := provider.Result{RateLimited: true, IsError: true, Text: "You've hit your limit"}
	if !resetsAt.IsZero() {
		r.ResetsAt = resetsAt.Unix()
	}
	return r
}

func TestProviderLimitPausesAndContinuesTheSameSession(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var prompts []string
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		mu.Lock()
		prompts = append(prompts, c.Req.Prompt)
		mu.Unlock()
		if c.N == 1 {
			return limited(t0.Add(2 * time.Hour))
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	p := e.createProject("plain", testManifest([]string{agentCommand("work", "")}, nil))
	id := e.run(p, "work")
	e.waitState(id, "rate_limited")
	e.Clock.Add(time.Hour)
	e.sweep()
	e.settle()
	if got := e.full(id); got.State != "rate_limited" || got.Deadline != "" || got.SilentSince != "" || len(e.Fake.Calls()) != 1 {
		t.Fatalf("before the reset the process waits, its ceiling and silence stand still: %+v, %d calls", got, len(e.Fake.Calls()))
	}
	e.Clock.Add(time.Hour)
	e.sweep()
	got := e.waitState(id, "done", "error")
	calls := e.Fake.Calls()
	if got.State != "done" || len(calls) != 2 || !calls[1].Resume || calls[1].SessionID != calls[0].SessionID ||
		!strings.Contains(prompts[1], "limit should have reset") {
		t.Fatalf("at the reset the same session goes on: %+v, %d calls", got, len(calls))
	}
	var hs []map[string]any
	e.must("deck.health_list", nil, &hs)
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
	cf := &checkedFake{Fake: &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		mu.Lock()
		defer mu.Unlock()
		if !ok {
			return limited(time.Time{})
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 2}`)}
	}}}
	clock := &Clock{now: t0}
	e := newEnvWith(t, t.TempDir(), cf, withClock(clock))
	e.Clock, e.Fake = clock, cf.Fake
	p := e.createProject("plain", testManifest([]string{agentCommand("work", "")}, nil))
	id := e.run(p, "work")
	e.waitState(id, "rate_limited")
	for h := 1; h <= 9; h++ {
		e.Clock.Add(30 * time.Minute)
		e.sweep()
		e.settle()
		if n := len(e.Fake.Calls()); n != h {
			t.Fatalf("no probe before the hour is up: %d calls after %dh30", n, h-1)
		}
		e.Clock.Add(30 * time.Minute)
		e.sweep()
		e.waitFor("the hourly probe", func() bool { return len(e.Fake.Calls()) == h+1 })
		e.waitState(id, "rate_limited")
	}
	var as []askView
	e.must("deck.asks_list", nil, &as)
	if len(as) != 0 {
		t.Fatalf("a pause asks the owner nothing: %+v", as)
	}
	e.must("deck.process_stop", map[string]any{"process": id}, nil)
	e.waitState(id, "stopped")
	e.Clock.Add(2 * time.Hour)
	e.sweep()
	e.settle()
	if n := len(e.Fake.Calls()); n != 10 {
		t.Fatalf("a stopped pause probes no more: %d calls", n)
	}
	mu.Lock()
	ok = true
	mu.Unlock()
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	e.waitHealth("provider:fake", "ok")
	e.must("deck.process_rework", map[string]any{"process": id, "remark": "go on"}, nil)
	got := e.waitState(id, "done", "waiting_answer", "error")
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
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		mu.Lock()
		prompts = append(prompts, c.Req.Prompt)
		mu.Unlock()
		if c.N == 1 {
			c.Tool(ctx, "notes__post", map[string]any{"text": "hi"})
			return limited(time.Time{})
		}
		return provider.Result{Structured: json.RawMessage(`{"written": 0}`)}
	})
	p := e.createProject("notes", strings.Replace(notesManifest, `"requires": ["no_cloud"],`, "", 1))
	id := e.run(p, "collect")
	e.waitState(id, "rate_limited")
	e.answer(e.waitAsk().ID, "deny")
	e.settle()
	if n := len(e.Fake.Calls()); n != 1 {
		t.Fatalf("the answer waits for the next try: %d calls", n)
	}
	e.Clock.Add(time.Hour)
	e.sweep()
	e.waitState(id, "done", "error")
	mu.Lock()
	defer mu.Unlock()
	if len(prompts) != 2 || !strings.Contains(prompts[1], "limit should have reset") || !strings.Contains(prompts[1], "notes.post") {
		t.Fatalf("the next try carries the answer: %q", prompts)
	}
}
