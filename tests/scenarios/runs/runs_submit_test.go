package runs

import (
	"context"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestAResultHandedInStaysWhenACallComesAndTheProcessIsStopped: the step after a result handed in starts at once, a
// call for its record starts a run of its own, a stop ends the hung process done with the result.
func TestAResultHandedInStaysWhenACallComesAndTheProcessIsStopped(t *testing.T) {
	t.Parallel()
	handed := make(chan struct{}, 1)
	fake := &harness.Fake{Submits: true, Script: func(ctx context.Context, c *harness.Call) provider.Result {
		cut := "v2"
		if strings.Contains(c.Req.Prompt, "the sky") {
			cut = "v3"
		}
		if out, bad := c.Tool(ctx, "result_submit", map[string]any{"result": map[string]any{"cut": cut}}); bad || !strings.Contains(out, "accepted") {
			c.Fake.Broke("result_submit: " + out)
		}
		if cut == "v2" {
			handed <- struct{}{}
			<-ctx.Done()
			return provider.Result{}
		}
		return provider.Result{Text: "Done."}
	}}
	e := harness.New(t, harness.WithProvider(fake))
	e.Project("clips", harness.ClipsModule)
	e.SaveScript("clips", "scripts/render.py", harness.ClipsRender)
	a := e.Clip("a")
	first := e.Start("clips", "revise", map[string]any{"record": a, "note": "the crown"})
	<-handed
	if got := e.Proc(first); got.State != "running" || string(got.Result) != `{"cut":"v2"}` {
		t.Fatalf("the result handed in is kept at once: %+v", got)
	}
	e.Until("the render after the result handed in", func() bool { return len(runsOfCommand(e, "render")) == 1 })

	second := e.Start("clips", "revise", map[string]any{"record": a, "note": "the sky"})
	if second == first {
		t.Fatal("a call for the record after the result was handed in starts a run of its own")
	}
	e.WaitState(second, "done")

	var stop map[string]any
	e.Must("deck.process_stop", map[string]any{"process": first}, &stop)
	if stop["state"] != "done" || !strings.Contains(stop["text"].(string), "result") {
		t.Fatalf("the stop says the result stays: %v", stop)
	}
	if got := e.WaitState(first, "done", "stopped", "error"); got.State != "done" || string(got.Result) != `{"cut":"v2"}` {
		t.Fatalf("a process stopped after it handed in its result is done with it: %+v", got)
	}
	e.Until("a render after each revise", func() bool { return len(runsOfCommand(e, "render")) == 2 })
	for _, r := range runsOfCommand(e, "render") {
		if got := e.WaitState(r.ID, "done", "error"); got.State != "done" || harness.ProcessInput(t, e, r.ID)["record"] != float64(a) {
			t.Fatalf("each render goes on with the clip: %+v", got)
		}
	}
	e.Settle()
	if n := len(runsOfCommand(e, "render")); n != 2 {
		t.Fatalf("the step after a result handed in starts once, not again when the process closes: %d renders", n)
	}
}

// TestDeckHandsTheMasterTheIncidentsAroundAProcess: a result handed in by a process that still runs long after, a
// process that handed in a result and failed, a step after that did not start, each go to master.on_incident once.
func TestDeckHandsTheMasterTheIncidentsAroundAProcess(t *testing.T) {
	t.Parallel()
	handed := make(chan struct{}, 1)
	fake := &harness.Fake{Submits: true, Script: func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "" || !strings.Contains(c.Req.Prompt, "Edit the clip") {
			return provider.Result{Text: "ok"}
		}
		_, _ = c.Tool(ctx, "result_submit", map[string]any{"result": map[string]any{"cut": "v2"}})
		switch {
		case strings.Contains(c.Req.Prompt, "the crown"):
			handed <- struct{}{}
			<-ctx.Done()
		case strings.Contains(c.Req.Prompt, "the sky"):
			return provider.Result{Outcome: provider.Failed, Text: "the model broke after the result"}
		}
		return provider.Result{Text: "Done."}
	}}
	e := harness.New(t, harness.WithProvider(fake))
	// A step after revise whose script is missing does not start.
	m := strings.Replace(harness.ClipsModule, `"scripts": [`, `"scripts": [{"name": "label", "file": "scripts/label.py", "runtime": "python"}, `, 1)
	m = strings.Replace(m, `{"after": "revise", "command": "render"}`, `{"after": "revise", "command": "render"}, {"after": "revise", "command": "label"}`, 1)
	m = strings.Replace(m, `"commands": [`, `"commands": [{"name": "label", "title": "Label", "description": "Labels a clip.", "class": "internal",
     "input": {"type": "object", "properties": {"record": {"type": "integer"}, "process": {"type": "integer", "format": "process"}}}, "executor": {"type": "script", "script": "label"}},`, 1)
	e.Project("clips", m)
	e.SaveScript("clips", "scripts/render.py", harness.ClipsRender)
	e.Must("deck.project_pause", map[string]any{"project": "master", "paused": false}, nil)
	e.UseModels("master.on_incident", "quiet")
	incidents := func() map[string]int64 {
		var ps []harness.Proc
		e.Must("deck.processes_list", map[string]any{"project": "master", "command": "on_incident"}, &ps)
		out := map[string]int64{}
		for _, p := range ps {
			in := harness.ProcessInput(t, e, p.ID)
			out[in["incident"].(string)] = int64(in["process"].(float64))
		}
		return out
	}
	hung := e.Start("clips", "revise", map[string]any{"record": e.Clip("a"), "note": "the crown"})
	<-handed
	failed := e.Start("clips", "revise", map[string]any{"record": e.Clip("b"), "note": "the sky"})
	e.WaitState(failed, "error")
	e.Until("the incidents of the failed process and its step after", func() bool {
		got := incidents()
		return got["result_not_done"] == failed && got["after_not_started"] != 0
	})
	if got := incidents(); got["result_running"] != 0 {
		t.Fatalf("a process that handed in its result a moment ago is no incident yet: %v", got)
	}
	e.App.Sweep(t.Context(), time.Now().Add(15*time.Minute))
	e.Until("the incident of the process that still runs", func() bool { return incidents()["result_running"] == hung })
	e.App.Sweep(t.Context(), time.Now().Add(30*time.Minute))
	e.Must("deck.process_stop", map[string]any{"process": hung}, nil)
	e.WaitState(hung, "done")
	e.Settle()
	var ps []harness.Proc
	e.Must("deck.processes_list", map[string]any{"project": "master", "command": "on_incident"}, &ps)
	if len(ps) != 4 {
		t.Fatalf("each incident goes to the master once: the hung, the failed, the two steps after that did not start: %d", len(ps))
	}
}
