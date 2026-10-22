package scenarios

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"deck2/internal/provider"
)

// twoModels is deck with the main fake and a backup one, the command work of plain assigned to both in that order.
func twoModels(t *testing.T, main, backup func(context.Context, *Call) provider.Result) (*Env, *Fake, int64) {
	t.Helper()
	m, b := &Fake{script: main}, &Fake{name: "backup", script: backup}
	e := newEnvBuiltin(t, t.TempDir(), []provider.Provider{m, b})
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
