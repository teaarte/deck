package scenarios

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/internal/app"
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

func TestPauseWithoutAResetTimeProbesHourlyUntilItsLimit(t *testing.T) {
	var mu sync.Mutex
	ok := false
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		mu.Lock()
		defer mu.Unlock()
		if !ok {
			return limited(time.Time{})
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 2}`)}
	})
	p := e.createProject("plain", testManifest([]string{agentCommand("work", "")}, nil))
	id := e.run(p, "work")
	e.waitState(id, "rate_limited")
	for h := 1; h <= 5; h++ {
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
	e.Clock.Add(time.Hour)
	e.sweep()
	got := e.waitState(id, "stopped")
	if n := len(e.Fake.Calls()); n != 7 || !strings.Contains(got.Error, "pause") {
		t.Fatalf("the last probe at six hours after the first refusal is refused, so the process stops; later refusals did not move that: %d calls, %+v", n, got)
	}
	a := e.waitAsk()
	if a.Kind != "limit" {
		t.Fatalf("the owner is offered to continue: %+v", a)
	}
	if code, _ := e.call("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "retry"}); code == 200 {
		t.Fatal("a stopped pause is answered with continue only")
	}
	mu.Lock()
	ok = true
	mu.Unlock()
	e.answer(a.ID, "continue")
	got = e.waitState(id, "done", "error")
	calls := e.Fake.Calls()
	if got.State != "done" || string(got.Result) != `{"n":2}` || !calls[7].Resume || calls[7].SessionID != calls[0].SessionID {
		t.Fatalf("continue resumes the same session: %+v", got)
	}
	pp, _ := e.App.Store.Q.GetProcess(context.Background(), id)
	if pp.PauseLimitMs.Valid {
		t.Fatalf("the owner's word starts a new pause limit: %+v", pp.PauseLimitMs)
	}
}

func TestReworkAfterAStoppedPauseIsNotHeldByItsAsk(t *testing.T) {
	var mu sync.Mutex
	limit := true
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		mu.Lock()
		defer mu.Unlock()
		if limit {
			return limited(time.Time{})
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 3}`)}
	}, func(c *app.Config) { c.PauseLimit = time.Hour })
	p := e.createProject("plain", testManifest([]string{agentCommand("work", "")}, nil))
	id := e.run(p, "work")
	e.waitState(id, "rate_limited")
	e.Clock.Add(time.Hour)
	e.sweep()
	e.waitState(id, "stopped")
	mu.Lock()
	limit = false
	mu.Unlock()
	e.must("deck.process_rework", map[string]any{"process": id, "remark": "go on"}, nil)
	if got := e.waitState(id, "done", "waiting_answer", "error"); got.State != "done" {
		t.Fatalf("rework after a stopped pause finishes; its limit ask is no open question: %+v", got)
	}
	var as []askView
	e.must("deck.asks_list", nil, &as)
	if len(as) != 0 {
		t.Fatalf("the rework closed the offer to continue: %+v", as)
	}
}

func TestOwnerAnswerDuringAPauseGivesAFullLimit(t *testing.T) {
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.N == 1 {
			c.Tool(ctx, "notes__post", map[string]any{"text": "hi"})
		}
		return limited(time.Time{})
	})
	p := e.createProject("notes", strings.Replace(notesManifest, `"requires": ["no_cloud"],`, "", 1))
	id := e.run(p, "collect")
	e.waitState(id, "rate_limited")
	e.Clock.Add(3 * time.Hour)
	e.answer(e.waitAsk().ID, "deny")
	e.waitFor("a new pause limit", func() bool {
		pp, _ := e.App.Store.Q.GetProcess(context.Background(), id)
		return pp.PauseLimitMs.Int64 == e.Clock.Now().Add(6*time.Hour).UnixMilli()
	})
}
