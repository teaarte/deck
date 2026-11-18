package runs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// The hooks of a closed process hear it again after a fall and after a hook that failed: each acts once however often
// it hears it.

// TestAStepAfterAProcessHeardTwiceStartsOnce: the after trigger of a process heard twice starts one run.
func TestAStepAfterAProcessHeardTwiceStartsOnce(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	p := e.Project("chain", harness.TestManifest([]string{harness.AgentCommand("job", ""), harness.AgentCommand("next", harness.ProcessField)},
		[]string{`{"command": "next", "after": "job"}`}))
	id := e.Run(p, "job")
	e.WaitState(id, "done")
	e.Until("the after run", func() bool { return e.CountOf("next") == 1 })
	e.HearAgain(id)
	if n := e.CountOf("next"); n != 1 {
		t.Fatalf("a process heard twice starts its step after once: %d", n)
	}
}

// TestAnIncidentOfAProcessHeardTwiceReachesTheOwnerOnce: the incident of a process the paused master does not take is
// one note however often deck hears the process.
func TestAnIncidentOfAProcessHeardTwiceReachesTheOwnerOnce(t *testing.T) {
	t.Parallel()
	fake := &harness.Fake{Submits: true, Script: func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "" || !strings.Contains(c.Req.Prompt, "Edit the clip") {
			return provider.Result{Text: "ok"}
		}
		_, _ = c.Tool(ctx, "result_submit", map[string]any{"result": map[string]any{"cut": "v2", "files": []map[string]any{{"path": "cut.mp4"}}}})
		return provider.Result{Text: "Done."}
	}}
	e := harness.New(t, harness.WithProvider(fake))
	e.Project("clips", harness.ClipsModule)
	e.SaveScript("clips", "scripts/render.py", harness.ClipsRender)
	failed := e.Start("clips", "revise", map[string]any{"record": e.Clip("b"), "note": "the sky"})
	e.WaitState(failed, "error")
	e.WaitNote("the master did not take it: it is paused")
	e.HearAgain(failed)
	if n := e.Warnings("the master did not take it"); n != 1 {
		t.Fatalf("an incident heard twice is one note: %d", n)
	}
}

// TestAnAnswerHeardTwiceReachesTheMasterOnce: an answer its process ended without acting on goes to the master and the
// owner's feed once, however often deck hears the process.
func TestAnAnswerHeardTwiceReachesTheMasterOnce(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() != "collect" {
			return provider.Result{Text: "On it."}
		}
		c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{
			{"question": "Which voice?", "options": []map[string]any{{"label": "The calm one"}}}}})
		return provider.Result{Structured: json.RawMessage(`null`)}
	}))
	p := e.Project("ideas", harness.Nullable(harness.IdeasManifest))
	id := e.Run(p, "collect")
	e.Answer(e.WaitAsk().ID, `{"Which voice?":"the calm one"}`)
	e.WaitState(id, "done")
	e.WaitNote("the calm one")
	e.HearAgain(id)
	if n := e.Warnings("the calm one"); n != 1 {
		t.Fatalf("an answer heard twice is one note: %d", n)
	}
	var master int
	// The notes of the master are rows of the feed for it, which no read of the owner lists.
	if err := e.SQLRow(`SELECT count(*) FROM feed WHERE for_master = 1 AND text LIKE '%the calm one%'`).Scan(&master); err != nil || master != 1 {
		t.Fatalf("an answer heard twice is one note of the master: %d %v", master, err)
	}
}
