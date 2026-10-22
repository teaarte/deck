package scenarios

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/store/db"
)

// twoModels is deck with the main fake and a backup one, the command work of plain assigned to both in that order.
func twoModels(t *testing.T, main, backup func(context.Context, *Call) provider.Result, cfg ...func(*app.Config)) (*Env, *Fake, int64) {
	t.Helper()
	m, b := &Fake{script: main}, &Fake{name: "backup", script: backup}
	e := newEnvBuiltin(t, t.TempDir(), []provider.Provider{m, b}, cfg...)
	e.Fake = m
	p := e.createProject("plain", testManifest([]string{agentCommand("work", `{"type": "object", "properties": {"n": {"type": "integer"}}}`)}, nil))
	e.useModels("plain.work", "fake", "backup")
	return e, b, p
}

func (e *Env) startWork(n int) int64 {
	e.T.Helper()
	var r struct{ Process int64 }
	e.must("plain.work", map[string]any{"n": n}, &r)
	return r.Process
}

// following waits for the one process that goes on with the work of id.
func (e *Env) following(id int64) int64 {
	e.T.Helper()
	var next int64
	e.waitFor("the process that goes on", func() bool {
		if got := e.fault(id); len(got.FollowedBy) == 1 {
			next = got.FollowedBy[0]
		}
		return next != 0
	})
	return next
}

// TestAModelAtItsLimitHandsTheWorkOnToTheNextModel: the model's failure on a limit goes on on the next model with the
// same input, told what was done; the feed says so, the model waits for its reset, the cockpit is clear.
func TestAModelAtItsLimitHandsTheWorkOnToTheNextModel(t *testing.T) {
	t.Parallel()
	e, backup, _ := twoModels(t, func(ctx context.Context, c *Call) provider.Result {
		c.Req.Emit(provider.Event{Kind: provider.EventMessage, Text: "rendered the stills of scene 1"})
		c.Req.Emit(provider.Event{Kind: provider.EventTool, Title: "shell", Text: "render --stills scene1"})
		c.Req.Emit(provider.Event{Kind: provider.EventRateLimit, Text: "workspace_member_credits_depleted"})
		return limited(t0)
	}, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	id := e.startWork(7)
	if got := e.waitState(id, "error", "done", "rate_limited"); got.State != "error" || e.fault(id).ErrorType != "model.limit" || e.fault(id).Whose != "model" {
		t.Fatalf("the refused process closes as the model's failure: %+v %+v", got, e.fault(id))
	}
	next := e.following(id)
	got := e.waitState(next, "done", "error")
	var in struct {
		Provider string
		Input    map[string]any
	}
	e.must("deck.process_get", map[string]any{"process": next}, &in)
	if got.State != "done" || in.Provider != "backup" || e.fault(next).Follows != id {
		t.Fatalf("the next model does the work: %+v %+v", got, in)
	}
	calls := backup.Calls()
	if in.Input["n"] != float64(7) || len(calls) != 1 || !strings.Contains(calls[0].Prompt, "continues process #"+itoa(id)) ||
		!strings.Contains(calls[0].Prompt, "rendered the stills of scene 1") || !strings.Contains(calls[0].Prompt, "render --stills scene1") {
		t.Fatalf("the next model gets the same input and is told what the process did: %v %+v", in.Input, calls)
	}
	var waited struct {
		ID    int64
		State string
	}
	e.must("deck.process_get", map[string]any{"process": id, "wait": 30}, &waited)
	if waited.ID != next || waited.State != "done" {
		t.Fatalf("a wait for the refused process goes on through the one that took its work: %+v", waited)
	}
	e.waitNote("goes on with it on backup")
	if h := e.waitHealth("provider:fake", "paused"); !strings.Contains(h.Error, "usage limit") {
		t.Fatalf("the model at its limit waits for its reset: %+v", h)
	}
	if rows := e.failedRuns(); len(rows) != 0 {
		t.Fatalf("a failure another model made good is not in the cockpit: %+v", rows)
	}
}

// TestAModelThatDoesNotAnswerHandsOnButAFailedTaskDoesNot: a model that did not answer hands the work on as a limit
// does; a turn whose task failed stays a failure of the module on its model.
func TestAModelThatDoesNotAnswerHandsOnButAFailedTaskDoesNot(t *testing.T) {
	t.Parallel()
	e, backup, _ := twoModels(t, func(ctx context.Context, c *Call) provider.Result {
		if strings.Contains(c.Req.Prompt, `"n": 2`) || strings.Contains(c.Req.Prompt, `"n":2`) {
			return provider.Result{Outcome: provider.Failed, Text: "the file to edit is not there"}
		}
		return provider.Result{Outcome: provider.Unavailable, Text: "API Error: Can't reach the API server (ENOTFOUND)"}
	}, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	id := e.startWork(1)
	e.waitState(id, "error")
	if got := e.fault(id); got.ErrorType != "model.unavailable" {
		t.Fatalf("a model that did not answer: %+v", got)
	}
	e.waitState(e.following(id), "done")
	calls := len(backup.Calls())
	failed := e.startWork(2)
	e.waitState(failed, "error")
	e.App.Runner.Wait()
	if got := e.fault(failed); got.ErrorType != "task.failed" || len(got.FollowedBy) != 0 || len(backup.Calls()) != calls {
		t.Fatalf("a failed task does not go to another model: %+v", got)
	}
}

// TestAModelAtItsLimitWithoutANextOneWaitsForTheReset: with no other model in the assignment the process pauses as
// before.
func TestAModelAtItsLimitWithoutANextOneWaitsForTheReset(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result { return limited(t0) })
	p := e.createProject("plain", testManifest([]string{agentCommand("work", "")}, nil))
	e.useModels("plain.work", "fake")
	if got := e.waitState(e.run(p, "work"), "rate_limited", "error"); got.State != "rate_limited" {
		t.Fatalf("without a next model the process waits for the reset: %+v", got)
	}
}

// TestTheNextModelHearsWhatTheOwnerAnswered: an answer of the owner to the refused process goes on with its work.
func TestTheNextModelHearsWhatTheOwnerAnswered(t *testing.T) {
	t.Parallel()
	e, backup, _ := twoModels(t, func(ctx context.Context, c *Call) provider.Result {
		c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []any{map[string]any{"question": "Which account?",
			"options": []any{map[string]any{"label": "first"}, map[string]any{"label": "second"}}}}})
		return limited(t0)
	}, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	id := e.startWork(1)
	e.answer(e.waitAsk().ID, "second")
	e.waitState(e.following(id), "done", "error")
	if calls := backup.Calls(); len(calls) != 1 || !strings.Contains(calls[0].Prompt, "Which account?") || !strings.Contains(calls[0].Prompt, "second") {
		t.Fatalf("the next model is told the owner's answer: %+v", calls)
	}
}

// silent is a model that takes the call and gives no sign until the turn is taken from it.
func silent(ctx context.Context, c *Call) provider.Result {
	<-ctx.Done()
	return provider.Result{Outcome: provider.Unavailable, Text: "cut"}
}

// TestAModelThatGivesNoSignHandsTheTurnOnLongBeforeTheCeiling: a model that took the turn and gave no event for five
// minutes loses it to the next model of the assignment; one that speaks keeps it.
func TestAModelThatGivesNoSignHandsTheTurnOnLongBeforeTheCeiling(t *testing.T) {
	t.Parallel()
	clock := &Clock{now: t0}
	e, backup, _ := twoModels(t, func(ctx context.Context, c *Call) provider.Result {
		if strings.Contains(c.Req.Prompt, `"n": 2`) || strings.Contains(c.Req.Prompt, `"n":2`) {
			c.Req.Emit(provider.Event{Kind: provider.EventThought, Text: "reading the task"})
		}
		<-ctx.Done()
		return provider.Result{}
	}, ok, withClock(clock))
	e.Clock = clock
	id := e.startWork(1)
	e.waitFor("the model takes the call", func() bool { return len(e.Fake.Calls()) == 1 })
	clock.Add(4 * time.Minute)
	e.sweep()
	if got := e.waitState(id, "running"); got.State != "running" {
		t.Fatalf("a model is given its time: %+v", got)
	}
	clock.Add(2 * time.Minute)
	e.sweep()
	e.waitState(id, "error")
	if got := e.fault(id); got.ErrorType != "model.unavailable" || !strings.Contains(got.Error, "gave no sign for 5 minutes") {
		t.Fatalf("a model that gave no sign is the model's failure: %+v", got)
	}
	if got := e.waitState(e.following(id), "done", "error"); got.State != "done" || len(backup.Calls()) != 1 {
		t.Fatalf("the next model does the work: %+v", got)
	}
	e.waitNote("gave no sign for 5 minutes")
	spoke := e.startWork(2)
	e.waitFor("the model speaks", func() bool { return len(e.Fake.Calls()) == 2 })
	clock.Add(time.Hour - time.Minute)
	e.sweep()
	if got := e.waitState(spoke, "running"); got.State != "running" {
		t.Fatalf("a model that gave a sign keeps its turn: %+v", got)
	}
	_ = e.App.Runner.Stop(context.Background(), spoke, "test", true)
}

// TestATurnOnAModelThatWentOnAPauseGoesToTheNextModel: a turn whose model another turn found at its limit leaves it
// for the next model; with no next one it waits for the model in its session.
func TestATurnOnAModelThatWentOnAPauseGoesToTheNextModel(t *testing.T) {
	t.Parallel()
	clock := &Clock{now: t0}
	e, backup, _ := twoModels(t, func(ctx context.Context, c *Call) provider.Result {
		if strings.Contains(c.Req.Prompt, `"n": 2`) || strings.Contains(c.Req.Prompt, `"n":2`) {
			return limited(t0.Add(time.Hour))
		}
		c.Req.Emit(provider.Event{Kind: provider.EventMessage, Text: "working"})
		<-ctx.Done()
		return provider.Result{}
	}, ok, withClock(clock))
	e.Clock = clock
	id := e.startWork(1)
	e.waitFor("the model takes the call", func() bool { return len(e.Fake.Calls()) == 1 })
	e.waitState(e.following(e.startWork(2)), "done")
	e.waitHealth("provider:fake", "paused")
	e.sweep()
	e.waitState(id, "error")
	if got := e.fault(id); got.ErrorType != "model.limit" || !strings.Contains(got.Error, "fake went paused during the turn") {
		t.Fatalf("a turn on a paused model leaves it as the model's limit: %+v", got)
	}
	if got := e.waitState(e.following(id), "done", "error"); got.State != "done" || len(backup.Calls()) != 2 {
		t.Fatalf("the next model does the work: %+v", got)
	}
	e.waitNote("goes on with it on backup")

	e.useModels("plain.work", "fake")
	_ = e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		return process.ProviderHealth(ctx, q, "fake", "ok", "")
	})
	alone := e.startWork(3)
	e.waitFor("the model takes the call", func() bool { return len(e.Fake.Calls()) == 3 })
	_ = e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		return process.ProviderHealth(ctx, q, "fake", "paused", "usage limit reached")
	})
	e.sweep()
	if got := e.waitState(alone, "waiting_provider", "error"); got.State != "waiting_provider" {
		t.Fatalf("with no next model the turn waits for its model: %+v %+v", got, e.fault(alone))
	}
	e.waitNote("waits for fake")
}

// TestAnAgentInALoopLeavesItsModel: the same call of a tool with the same answer three times in a row is a loop of
// the agent; the turn goes on on the next model.
func TestAnAgentInALoopLeavesItsModel(t *testing.T) {
	t.Parallel()
	e, backup, _ := twoModels(t, func(ctx context.Context, c *Call) provider.Result {
		for i := range 5 {
			id := itoa(int64(i))
			c.Req.Emit(provider.Event{Kind: provider.EventTool, ID: "a" + id, Title: "mcp__deck__records_list", Text: `{"collection": "searches"}`})
			c.Req.Emit(provider.Event{Kind: provider.EventToolUpdate, ID: "a" + id, Status: "completed", Text: `{"items": []}`})
			c.Req.Emit(provider.Event{Kind: provider.EventTool, ID: "b" + id, Title: "mcp__deck__records_list", Text: `{"collection": "listings"}`})
			c.Req.Emit(provider.Event{Kind: provider.EventToolUpdate, ID: "b" + id, Status: "completed", Text: `{"items": []}`})
		}
		for i := range 3 {
			id := "c" + itoa(int64(i))
			c.Req.Emit(provider.Event{Kind: provider.EventTool, ID: id, Title: "mcp__deck__records_list", Text: `{"collection": "searches"}`})
			c.Req.Emit(provider.Event{Kind: provider.EventToolUpdate, ID: id, Status: "completed", Text: `{"items": []}`})
		}
		<-ctx.Done()
		return provider.Result{}
	}, ok)
	id := e.startWork(1)
	e.waitState(id, "error")
	if got := e.fault(id); got.ErrorType != "agent.loop" || got.Whose != "model" || !strings.Contains(got.Error, "mcp__deck__records_list 3 times in a row") {
		t.Fatalf("a loop is the model's failure of its own type: %+v", got)
	}
	if got := e.waitState(e.following(id), "done", "error"); got.State != "done" || len(backup.Calls()) != 1 {
		t.Fatalf("the next model does the work: %+v", got)
	}
}
