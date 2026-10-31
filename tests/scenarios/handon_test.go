package scenarios

import (
	"context"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/internal/store/db"
	"deck2/tests/harness"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// twoModels is deck with the main fake and a backup one, the command work of plain assigned to both in that order.
func twoModels(t *testing.T, main, backup func(context.Context, *harness.Call) provider.Result, opts ...harness.Option) (*harness.Env, *harness.Fake, int64) {
	t.Helper()
	m, b := &harness.Fake{Script: main}, &harness.Fake{ProviderName: "backup", Script: backup}
	e := harness.New(t, append([]harness.Option{harness.WithProvider(m, b)}, opts...)...)
	p := e.Project("plain", harness.TestManifest([]string{harness.AgentCommand("work", `{"type": "object", "properties": {"n": {"type": "integer"}}}`)}, nil))
	e.UseModels("plain.work", "fake", "backup")
	return e, b, p
}

func startWork(e *harness.Env, n int) int64 {
	e.T.Helper()
	var r struct{ Process int64 }
	e.Must("plain.work", map[string]any{"n": n}, &r)
	return r.Process
}

// TestAModelAtItsLimitHandsTheWorkOnToTheNextModel: the model's failure on a limit goes on on the next model with the
// same input, told what was done; the feed says so, the model waits for its reset, the cockpit is clear.
func TestAModelAtItsLimitHandsTheWorkOnToTheNextModel(t *testing.T) {
	t.Parallel()
	e, backup, _ := twoModels(t, func(ctx context.Context, c *harness.Call) provider.Result {
		c.Req.Emit(provider.Event{Kind: provider.EventMessage, Text: "rendered the stills of scene 1"})
		c.Req.Emit(provider.Event{Kind: provider.EventTool, Title: "shell", Text: "render --stills scene1"})
		c.Req.Emit(provider.Event{Kind: provider.EventRateLimit, Text: "workspace_member_credits_depleted"})
		return harness.Limited(harness.T0)
	}, func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	id := startWork(e, 7)
	if got := e.WaitState(id, "error", "done", "rate_limited"); got.State != "error" || e.Fault(id).ErrorType != "model.limit" || e.Fault(id).Whose != "model" {
		t.Fatalf("the refused process closes as the model's failure: %+v %+v", got, e.Fault(id))
	}
	next := e.Following(id)
	got := e.WaitState(next, "done", "error")
	var in struct {
		Provider string
		Input    map[string]any
	}
	e.Must("deck.process_get", map[string]any{"process": next}, &in)
	if got.State != "done" || in.Provider != "backup" || e.Fault(next).Follows != id {
		t.Fatalf("the next model does the work: %+v %+v", got, in)
	}
	calls := backup.Calls()
	if in.Input["n"] != float64(7) || len(calls) != 1 || !strings.Contains(calls[0].Prompt, "continues process #"+harness.Itoa(id)) ||
		!strings.Contains(calls[0].Prompt, "rendered the stills of scene 1") || !strings.Contains(calls[0].Prompt, "render --stills scene1") {
		t.Fatalf("the next model gets the same input and is told what the process did: %v %+v", in.Input, calls)
	}
	var waited struct {
		ID    int64
		State string
	}
	e.Must("deck.process_get", map[string]any{"process": id, "wait": 30}, &waited)
	if waited.ID != next || waited.State != "done" {
		t.Fatalf("a wait for the refused process goes on through the one that took its work: %+v", waited)
	}
	e.WaitNote("backup goes on with it")
	if h := e.WaitHealth("provider:fake", "paused"); !strings.Contains(h.Error, "usage limit") {
		t.Fatalf("the model at its limit waits for its reset: %+v", h)
	}
	if rows := e.FailedRuns(); len(rows) != 0 {
		t.Fatalf("a failure another model made good is not in the cockpit: %+v", rows)
	}
}

// TestAModelThatDoesNotAnswerHandsOnButAFailedTaskDoesNot: a model that did not answer hands the work on as a limit
// does; a turn whose task failed stays a failure of the module on its model.
func TestAModelThatDoesNotAnswerHandsOnButAFailedTaskDoesNot(t *testing.T) {
	t.Parallel()
	e, backup, _ := twoModels(t, func(ctx context.Context, c *harness.Call) provider.Result {
		if strings.Contains(c.Req.Prompt, `"n": 2`) || strings.Contains(c.Req.Prompt, `"n":2`) {
			return provider.Result{Outcome: provider.Failed, Text: "the file to edit is not there"}
		}
		return provider.Result{Outcome: provider.Unavailable, Text: "API Error: Can't reach the API server (ENOTFOUND)"}
	}, func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	id := startWork(e, 1)
	e.WaitState(id, "error")
	if got := e.Fault(id); got.ErrorType != "model.unavailable" {
		t.Fatalf("a model that did not answer: %+v", got)
	}
	e.WaitState(e.Following(id), "done")
	calls := len(backup.Calls())
	failed := startWork(e, 2)
	e.WaitState(failed, "error")
	e.App.Runner.Wait()
	if got := e.Fault(failed); got.ErrorType != "task.failed" || len(got.FollowedBy) != 0 || len(backup.Calls()) != calls {
		t.Fatalf("a failed task does not go to another model: %+v", got)
	}
}

// TestAModelAtItsLimitWithoutANextOneWaitsForTheReset: with no other model in the assignment the process pauses as
// before.
func TestAModelAtItsLimitWithoutANextOneWaitsForTheReset(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result { return harness.Limited(harness.T0) }))
	p := e.Project("plain", harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil))
	e.UseModels("plain.work", "fake")
	if got := e.WaitState(e.Run(p, "work"), "rate_limited", "error"); got.State != "rate_limited" {
		t.Fatalf("without a next model the process waits for the reset: %+v", got)
	}
}

// TestTheNextModelHearsWhatTheOwnerAnswered: an answer of the owner to the refused process goes on with its work.
func TestTheNextModelHearsWhatTheOwnerAnswered(t *testing.T) {
	t.Parallel()
	e, backup, _ := twoModels(t, func(ctx context.Context, c *harness.Call) provider.Result {
		c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []any{map[string]any{"question": "Which account?",
			"options": []any{map[string]any{"label": "first"}, map[string]any{"label": "second"}}}}})
		return harness.Limited(harness.T0)
	}, func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	id := startWork(e, 1)
	e.Answer(e.WaitAsk().ID, "second")
	e.WaitState(e.Following(id), "done", "error")
	if calls := backup.Calls(); len(calls) != 1 || !strings.Contains(calls[0].Prompt, "Which account?") || !strings.Contains(calls[0].Prompt, "second") {
		t.Fatalf("the next model is told the owner's answer: %+v", calls)
	}
}

// silent is a model that takes the call and gives no sign until the turn is taken from it.
func silent(ctx context.Context, c *harness.Call) provider.Result {
	<-ctx.Done()
	return provider.Result{Outcome: provider.Unavailable, Text: "cut"}
}

// TestAModelThatGivesNoSignHandsTheTurnOnLongBeforeTheCeiling: a model that took the turn and gave no event for five
// minutes loses it to the next model of the assignment; one that speaks keeps it.
func TestAModelThatGivesNoSignHandsTheTurnOnLongBeforeTheCeiling(t *testing.T) {
	t.Parallel()
	clock := &harness.Clock{At: harness.T0}
	e, backup, _ := twoModels(t, func(ctx context.Context, c *harness.Call) provider.Result {
		if strings.Contains(c.Req.Prompt, `"n": 2`) || strings.Contains(c.Req.Prompt, `"n":2`) {
			c.Req.Emit(provider.Event{Kind: provider.EventThought, Text: "reading the task"})
		}
		<-ctx.Done()
		return provider.Result{}
	}, harness.Ok, harness.WithClock(clock))
	id := startWork(e, 1)
	e.Until("the model takes the call", func() bool { return len(e.Fake.Calls()) == 1 })
	clock.Add(4 * time.Minute)
	e.Sweep()
	if got := e.WaitState(id, "running"); got.State != "running" {
		t.Fatalf("a model is given its time: %+v", got)
	}
	clock.Add(2 * time.Minute)
	e.Sweep()
	e.WaitState(id, "error")
	if got := e.Fault(id); got.ErrorType != "model.unavailable" || !strings.Contains(got.Error, "gave no sign for 5 minutes") {
		t.Fatalf("a model that gave no sign is the model's failure: %+v", got)
	}
	if got := e.WaitState(e.Following(id), "done", "error"); got.State != "done" || len(backup.Calls()) != 1 {
		t.Fatalf("the next model does the work: %+v", got)
	}
	e.WaitNote("gave no sign for 5 minutes")
	spoke := startWork(e, 2)
	e.Until("the model speaks", func() bool { return len(e.Fake.Calls()) == 2 && len(e.Log(spoke)) == 1 })
	clock.Add(time.Hour - time.Minute)
	e.Sweep()
	if got := e.WaitState(spoke, "running"); got.State != "running" {
		t.Fatalf("a model that gave a sign keeps its turn: %+v", got)
	}
	_ = e.App.Runner.Stop(context.Background(), spoke, "test", true)
}

// TestATurnOnAModelThatWentOnAPauseGoesToTheNextModel: a turn whose model another turn found at its limit leaves it
// for the next model; with no next one it waits for the model in its session.
func TestATurnOnAModelThatWentOnAPauseGoesToTheNextModel(t *testing.T) {
	t.Parallel()
	clock := &harness.Clock{At: harness.T0}
	e, backup, _ := twoModels(t, func(ctx context.Context, c *harness.Call) provider.Result {
		if strings.Contains(c.Req.Prompt, `"n": 2`) || strings.Contains(c.Req.Prompt, `"n":2`) {
			return harness.Limited(harness.T0.Add(time.Hour))
		}
		c.Req.Emit(provider.Event{Kind: provider.EventMessage, Text: "working"})
		<-ctx.Done()
		return provider.Result{}
	}, harness.Ok, harness.WithClock(clock))
	id := startWork(e, 1)
	e.Until("the model takes the call", func() bool { return len(e.Fake.Calls()) == 1 })
	e.WaitState(e.Following(startWork(e, 2)), "done")
	e.WaitHealth("provider:fake", "paused")
	e.Sweep()
	e.WaitState(id, "error")
	if got := e.Fault(id); got.ErrorType != "model.limit" || !strings.Contains(got.Error, "fake went on a pause during the turn") {
		t.Fatalf("a turn on a paused model leaves it as the model's limit: %+v", got)
	}
	if got := e.WaitState(e.Following(id), "done", "error"); got.State != "done" || len(backup.Calls()) != 2 {
		t.Fatalf("the next model does the work: %+v", got)
	}
	e.WaitNote("backup goes on with it")
	var cmds []struct {
		Name, Model, Why string
	}
	e.Must("deck.commands_list", nil, &cmds)
	if i := slices.IndexFunc(cmds, func(c struct{ Name, Model, Why string }) bool { return c.Name == "plain.work" }); i < 0 ||
		cmds[i].Model != "backup" || !strings.HasPrefix(cmds[i].Why, "fake: usage limit reached") {
		t.Fatalf("the command says why its first model does not do its work: %+v", cmds)
	}

	e.UseModels("plain.work", "fake")
	_ = e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		return process.ProviderHealth(ctx, q, "fake", "ok", "")
	})
	alone := startWork(e, 3)
	e.Until("the model takes the call", func() bool { return len(e.Fake.Calls()) == 3 })
	_ = e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		return process.ProviderHealth(ctx, q, "fake", "paused", "usage limit reached")
	})
	e.Sweep()
	if got := e.WaitState(alone, "waiting_provider", "error"); got.State != "waiting_provider" {
		t.Fatalf("with no next model the turn waits for its model: %+v %+v", got, e.Fault(alone))
	}
	e.Until("a part of the note of the cause of fake that the turn waits for it", func() bool {
		var notes []struct {
			Cause string
			Parts []struct{ Text string }
		}
		e.Must("deck.feed_list", map[string]any{"kind": "note", "unread": true}, &notes)
		return slices.ContainsFunc(notes, func(n struct {
			Cause string
			Parts []struct{ Text string }
		}) bool {
			return n.Cause == "provider:fake" && slices.ContainsFunc(n.Parts, func(p struct{ Text string }) bool { return strings.Contains(p.Text, "waits for fake") })
		})
	})
}

// TestAnAgentInALoopLeavesItsModel: the same call of a tool with the same answer three times in a row is a loop of
// the agent; the turn goes on on the next model.
func TestAnAgentInALoopLeavesItsModel(t *testing.T) {
	t.Parallel()
	e, backup, _ := twoModels(t, func(ctx context.Context, c *harness.Call) provider.Result {
		for i := range 5 {
			id := harness.Itoa(int64(i))
			c.Req.Emit(provider.Event{Kind: provider.EventTool, ID: "a" + id, Title: "mcp__deck__records_list", Text: `{"collection": "searches"}`})
			c.Req.Emit(provider.Event{Kind: provider.EventToolUpdate, ID: "a" + id, Status: "completed", Text: `{"items": []}`})
			c.Req.Emit(provider.Event{Kind: provider.EventTool, ID: "b" + id, Title: "mcp__deck__records_list", Text: `{"collection": "listings"}`})
			c.Req.Emit(provider.Event{Kind: provider.EventToolUpdate, ID: "b" + id, Status: "completed", Text: `{"items": []}`})
		}
		for i := range 3 {
			id := "c" + harness.Itoa(int64(i))
			c.Req.Emit(provider.Event{Kind: provider.EventTool, ID: id, Title: "mcp__deck__records_list", Text: `{"collection": "searches"}`})
			c.Req.Emit(provider.Event{Kind: provider.EventToolUpdate, ID: id, Status: "completed", Text: `{"items": []}`})
		}
		<-ctx.Done()
		return provider.Result{}
	}, harness.Ok)
	id := startWork(e, 1)
	e.WaitState(id, "error")
	if got := e.Fault(id); got.ErrorType != "agent.loop" || got.Whose != "model" || !strings.Contains(got.Error, "mcp__deck__records_list 3 times in a row") {
		t.Fatalf("a loop is the model's failure of its own type: %+v", got)
	}
	if got := e.WaitState(e.Following(id), "done", "error"); got.State != "done" || len(backup.Calls()) != 1 {
		t.Fatalf("the next model does the work: %+v", got)
	}
}
