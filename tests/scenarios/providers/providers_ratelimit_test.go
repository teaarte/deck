package providers

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
	"deck2/internal/store"
	"deck2/tests/harness"
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

// limitedTurn runs a turn of the fake Goose whose model API answers its call with reply, and how long it pauses.
func limitedTurn(t *testing.T, reply harness.ChatReply) time.Duration {
	t.Helper()
	f := harness.NewACP(t)
	llm := harness.NewLLM(t, func(n int, _ harness.ChatRequest) harness.ChatReply {
		if n <= 3 {
			return harness.TextReply("ok")
		}
		return reply
	})
	f.StreamText(4, harness.AcpStream(gooseInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, harness.AcpModel,
		`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Ran into this error: Rate limit exceeded."}}}`,
		`{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, llm))
	id := e.Run(e.AcpProject("paint", harness.ColorManifest), "work")
	if got := e.WaitState(id, "done", "error", "rate_limited"); got.State != "rate_limited" {
		t.Fatalf("a 429 pauses the turn: %+v", got)
	}
	p, _ := e.App.Store.Q.GetProcess(t.Context(), id)
	if h := e.WaitHealth("provider:acp", "paused"); !strings.Contains(h.Error, "next try at "+store.Time(time.UnixMilli(p.PauseUntilMs.Int64))) {
		t.Fatalf("the model waits as long as the process: %+v", h)
	}
	return time.Until(time.UnixMilli(p.PauseUntilMs.Int64))
}

// TestAnOpenRouterLimitPausesUntilTheResetInItsError: OpenRouter tells the reset in unix milliseconds among the
// headers in the metadata of its error, with no Retry-After.
func TestAnOpenRouterLimitPausesUntilTheResetInItsError(t *testing.T) {
	t.Parallel()
	reset := time.Now().Add(10 * time.Minute).UnixMilli()
	left := limitedTurn(t, harness.ChatReply{Status: 429, Body: fmt.Sprintf(`{"error": {"message": "Rate limit exceeded: free-models-per-min. ", "code": 429,
		"metadata": {"headers": {"X-RateLimit-Limit": "20", "X-RateLimit-Remaining": "0", "X-RateLimit-Reset": "%d"}, "provider_name": null}}}`, reset)})
	if left < 5*time.Minute || left > 10*time.Minute {
		t.Fatalf("the pause lasts until the reset OpenRouter told: %s", left)
	}
}

// TestALimitPausesUntilTheResetOfItsHeaders: a reset in X-RateLimit-* as a duration (Groq) or in unix milliseconds.
func TestALimitPausesUntilTheResetOfItsHeaders(t *testing.T) {
	t.Parallel()
	for name, h := range map[string]http.Header{
		"duration": {"X-Ratelimit-Remaining-Requests": {"0"}, "X-Ratelimit-Reset-Requests": {"9m30s"}, "X-Ratelimit-Remaining-Tokens": {"500"}, "X-Ratelimit-Reset-Tokens": {"20m"}},
		"epoch":    {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if len(h) == 0 {
				h = http.Header{"X-Ratelimit-Reset": {fmt.Sprint(time.Now().Add(10 * time.Minute).UnixMilli())}}
			}
			left := limitedTurn(t, harness.ChatReply{Status: 429, Header: h, Body: `{"error": {"message": "Rate limit reached"}}`})
			if left < 5*time.Minute || left > 10*time.Minute {
				t.Fatalf("the pause lasts until the reset of the exhausted limit: %s", left)
			}
		})
	}
}

// TestALimitPausesByItsKind: a limit a minute without a reset time waits a minute, not the hour of an
// unknown one; a limit a day waits until the day ends in UTC, though Gemini tells seconds.
func TestALimitPausesByItsKind(t *testing.T) {
	t.Parallel()
	t.Run("minute", func(t *testing.T) {
		t.Parallel()
		left := limitedTurn(t, harness.ChatReply{Status: 429, Body: `{"error": {"message": "Rate limit exceeded: free-models-per-min. Add 10 credits to unlock 1000 free model requests per day", "code": 429}}`})
		if left > 2*time.Minute {
			t.Fatalf("a limit a minute pauses for minutes, not an hour: %s", left)
		}
	})
	gemini := `[{"error": {"code": 429, "status": "RESOURCE_EXHAUSTED", "details": [{"@type": "type.googleapis.com/google.rpc.QuotaFailure",
		"violations": [{"quotaId": "GenerateRequestsPer%sPerProjectPerModel-FreeTier"}]}, {"@type": "type.googleapis.com/google.rpc.RetryInfo", "retryDelay": "%s"}]}}]`
	for name, body := range map[string]string{
		"day":        `{"error": {"message": "Rate limit exceeded: free-models-per-day. Add 10 credits to unlock 1000 free model requests per day", "code": 429}}`,
		"gemini day": fmt.Sprintf(gemini, "Day", "51s"),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			left := limitedTurn(t, harness.ChatReply{Status: 429, Body: body})
			y, m, d := time.Now().UTC().Date()
			if want := time.Until(time.Date(y, m, d+1, 0, 0, 0, 0, time.UTC)); left < want-time.Minute || left > want+time.Second {
				t.Fatalf("a limit a day pauses until the day ends in UTC, %s: %s", want, left)
			}
		})
	}
	t.Run("gemini minute", func(t *testing.T) {
		t.Parallel()
		left := limitedTurn(t, harness.ChatReply{Status: 429, Body: fmt.Sprintf(gemini, "Minute", "540s")})
		if left < 5*time.Minute || left > 9*time.Minute {
			t.Fatalf("a limit a minute pauses for the retryDelay Gemini told: %s", left)
		}
	})
}

// TestALimitOfAnAgentsModelHandsTheWorkOnWithoutWaiting: work whose assignment has another model goes on there at
// once; only the model waits for its reset.
func TestALimitOfAnAgentsModelHandsTheWorkOnWithoutWaiting(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	llm := harness.NewLLM(t, func(n int, _ harness.ChatRequest) harness.ChatReply {
		if n <= 3 {
			return harness.TextReply("ok")
		}
		return harness.ChatReply{Status: 429, Body: `{"error": {"message": "Rate limit exceeded: free-models-per-min. ", "code": 429}}`}
	})
	f.StreamText(4, harness.AcpStream(gooseInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, harness.AcpModel,
		`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Ran into this error: Rate limit exceeded."}}}`,
		`{"end": "end_turn"}`))
	backup := &harness.Fake{ProviderName: "backup", Script: func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"color": "blue"}`)}
	}}
	e := harness.New(t, harness.WithACP(f, llm), harness.WithProvider(backup))
	e.AcpProject("paint", harness.ColorManifest)
	e.UseModels("paint.work", "acp", "backup")
	id := e.Start("paint", "work", nil)
	if got := e.WaitState(id, "error", "done", "rate_limited"); got.State != "error" || e.Fault(id).ErrorType != "model.limit" {
		t.Fatalf("the refused process closes on the model's limit, it does not wait: %+v %+v", got, e.Fault(id))
	}
	if got := e.WaitState(e.Following(id), "done", "error"); got.State != "done" || len(backup.Calls()) != 1 {
		t.Fatalf("the next model of the assignment does the work: %+v", got)
	}
	if h := e.WaitHealth("provider:acp", "paused"); !strings.Contains(h.Error, "usage limit reached") {
		t.Fatalf("the model at its limit waits for its reset: %+v", h)
	}
}
