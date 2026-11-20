package runs

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/fault"
	"deck2/internal/provider"
	"deck2/tests/harness"
)

// outcomeEnv is a work command assigned to the fake model first and to backup after it, the fake handing its result in
// with result_submit; backup answers {"n": 2}.
func outcomeEnv(t *testing.T, main harness.Script, opts ...harness.Option) (*harness.Env, *harness.Fake, *harness.Fake) {
	t.Helper()
	m := &harness.Fake{Submits: true, Script: main}
	b := &harness.Fake{ProviderName: "backup", Script: func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"n": 2}`)}
	}}
	e := harness.New(t, append([]harness.Option{harness.WithProvider(m, b)}, opts...)...)
	e.Project("plain", harness.TestManifest([]string{harness.Many(harness.AgentCommand("work", `{"type": "object", "properties": {"n": {"type": "integer"}}}`))}, nil))
	e.UseModels("plain.work", "fake", "backup")
	return e, m, b
}

// TestASubmittedResultBeatsAFailureAfterIt: a turn that handed in its result and then failed in a way that hands the
// work on — its model unavailable, past a limit of deck — is done with its result on its own model.
func TestASubmittedResultBeatsAFailureAfterIt(t *testing.T) {
	t.Parallel()
	failures := map[string]provider.Result{
		"unavailable": provider.Fail(fault.ModelUnavailable, "the model is overloaded"),
		"past":        {Past: "the call would pass the limit of 1000 tokens"},
	}
	for name, failure := range failures {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			e, _, backup := outcomeEnv(t, func(ctx context.Context, c *harness.Call) provider.Result {
				c.Tool(ctx, "result_submit", map[string]any{"result": map[string]any{"n": 1}})
				return failure
			})
			id := e.StartWork(1)
			got := e.WaitState(id, "done", "error")
			e.Settle()
			if got.State != "done" || string(got.Result) != `{"n":1}` || len(e.Fault(id).FollowedBy) != 0 || len(backup.Calls()) != 0 {
				t.Fatalf("the result handed in beats the failure after it: %+v, followed by %v, %d turns of backup",
					got, e.Fault(id).FollowedBy, len(backup.Calls()))
			}
		})
	}
}

// TestAWaitingQuestionBeatsAHandOn: a turn whose model fails while its question waits for the owner parks for the
// answer instead of handing the work on; the answer resumes it in its session on the same model.
func TestAWaitingQuestionBeatsAHandOn(t *testing.T) {
	t.Parallel()
	e, main, backup := outcomeEnv(t, func(ctx context.Context, c *harness.Call) provider.Result {
		if !c.Req.Resume {
			c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{{"question": "Which n?"}}})
			return provider.Fail(fault.ModelUnavailable, "the model is overloaded")
		}
		c.Tool(ctx, "result_submit", map[string]any{"result": map[string]any{"n": 1}})
		return provider.Result{Text: "Done."}
	}, harness.WithConfig(func(c *app.Config) { c.QuestionWait = 100 * time.Millisecond }))
	id := e.StartWork(1)
	e.WaitState(id, "waiting:answer")
	if len(e.Fault(id).FollowedBy) != 0 || len(backup.Calls()) != 0 {
		t.Fatalf("a waiting question keeps the work on its model: followed by %v, %d turns of backup", e.Fault(id).FollowedBy, len(backup.Calls()))
	}
	e.Answer(e.WaitAsk().ID, "one")
	got := e.WaitState(id, "done", "error")
	calls := main.Calls()
	if got.State != "done" || string(got.Result) != `{"n":1}` || len(calls) != 2 || !calls[1].Resume || len(backup.Calls()) != 0 {
		t.Fatalf("the answer resumes the session on the same model: %+v, %d turns, %d of backup", got, len(calls), len(backup.Calls()))
	}
}
