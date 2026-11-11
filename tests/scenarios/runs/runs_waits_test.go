package runs

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// waitsManifest has an agent work, an agent child, and scripts that end at once, take long and need the owner.
var waitsManifest = harness.Manifest(`{"collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}],
  "commands": [
    {"name": "work", "title": "Work", "description": "Does work.", "class": "internal",
     "executor": {"type": "agent", "prompt": "Do the work."}, "result": {"type": "object", "properties": {"n": {"type": "integer"}}}},
    {"name": "child", "title": "Child", "description": "Does a part.", "class": "internal",
     "executor": {"type": "agent", "prompt": "Do a part."}, "result": {"type": "object", "properties": {"n": {"type": "integer"}}}},
    {"name": "quick", "title": "Quick", "description": "Ends at once.", "class": "internal",
     "result": {"type": "object"}, "executor": {"type": "script", "script": "quick"}},
    {"name": "slow", "title": "Slow", "description": "Takes long.", "class": "internal",
     "result": {"type": "object"}, "executor": {"type": "script", "script": "slow"}},
    {"name": "send", "title": "Send", "description": "Sends outside.", "class": "external",
     "result": {"type": "object"}, "executor": {"type": "script", "script": "quick"}}],
  "scripts": [{"name": "quick", "file": "scripts/quick.py", "runtime": "python"},
    {"name": "slow", "file": "scripts/slow.py", "runtime": "python"}]}`)

// toolCall makes a call of a deck tool the way a provider shows it, so deck's watch of the turn sees it.
func toolCall(ctx context.Context, c *harness.Call, id, name string, args map[string]any) string {
	in, _ := json.Marshal(args)
	c.Req.Emit(provider.Event{Kind: provider.EventTool, ID: id, Title: "mcp__deck__" + name, Text: string(in)})
	out, _ := c.Tool(ctx, name, args)
	c.Req.Emit(provider.Event{Kind: provider.EventToolUpdate, ID: id, Status: "completed", Text: out})
	return out
}

// TestAModelThatLooksWithoutAWaitIsToldHowToWait: a look at a running process without a wait answers at once and
// says how to wait; the look with a wait gives the result when the process ends.
func TestAModelThatLooksWithoutAWaitIsToldHowToWait(t *testing.T) {
	t.Parallel()
	release, looked := make(chan struct{}), make(chan string, 1)
	answers := make(chan string, 1)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "child" {
			<-release
			return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
		}
		out := toolCall(ctx, c, "start", "waits__child", map[string]any{})
		var started struct{ Process int64 }
		_ = json.Unmarshal([]byte(out), &started)
		looked <- toolCall(ctx, c, "look", "process_get", map[string]any{"process": started.Process})
		answers <- toolCall(ctx, c, "wait", "process_get", map[string]any{"process": started.Process, "wait": 600})
		return provider.Result{Structured: json.RawMessage(`{"n": 2}`)}
	}))
	e.Project("waits", waitsManifest)
	id := e.Start("waits", "work", nil)
	if first := <-looked; !strings.Contains(first, `"hint"`) || !strings.Contains(first, "process_get with wait=600") {
		t.Fatalf("a look without a wait at a running process says how to wait: %s", first)
	}
	time.AfterFunc(time.Second, func() { close(release) })
	if got := <-answers; !strings.Contains(got, `"state":"done"`) {
		t.Fatalf("the look with a wait gives the result when the process ends: %s", got)
	}
	if p := e.WaitState(id, "done", "error"); p.State != "done" {
		t.Fatalf("the turn that waited ends its work: %+v", e.Fault(id))
	}
}

// TestPollingARunningProcessWithoutAWaitStopsAsALoop: looks without a wait at a process that has not ended answer
// the same each time, so the sixth stops the turn as a loop; the process it looked at goes on.
func TestPollingARunningProcessWithoutAWaitStopsAsALoop(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	child := make(chan int64, 1)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "child" {
			<-release
			return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
		}
		var started struct{ Process int64 }
		_ = json.Unmarshal([]byte(toolCall(ctx, c, "start", "waits__child", map[string]any{})), &started)
		child <- started.Process
		for i := range 10 {
			if ctx.Err() != nil {
				break
			}
			toolCall(ctx, c, "look"+harness.Itoa(int64(i)), "process_get", map[string]any{"process": started.Process})
		}
		return provider.Result{}
	}))
	e.Project("waits", waitsManifest)
	id := e.Start("waits", "work", nil)
	kid := <-child
	e.WaitState(id, "error")
	if got := e.Fault(id); got.ErrorType != "agent.loop" || !strings.Contains(got.Error, "process_get 6 times in a row") {
		t.Fatalf("the sixth look without a wait stops the turn as a loop: %+v", got)
	}
	close(release)
	e.WaitState(kid, "done")
}

// TestSixSameReadsInARowStopTheTurnAsALoop: the same read with the same answer six times in a row stops the turn as a
// loop; deck does not warn the agent before, nor change its answers.
func TestSixSameReadsInARowStopTheTurnAsALoop(t *testing.T) {
	t.Parallel()
	answers := make(chan []string, 1)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		var seen []string
		for i := range 8 {
			if ctx.Err() != nil {
				break
			}
			seen = append(seen, toolCall(ctx, c, "read"+harness.Itoa(int64(i)), "records_list", map[string]any{"project": "waits", "collection": "items"}))
		}
		answers <- seen
		return provider.Result{}
	}))
	e.Project("waits", waitsManifest)
	id := e.Start("waits", "work", nil)
	seen := <-answers
	e.WaitState(id, "error")
	if got := e.Fault(id); got.ErrorType != "agent.loop" || !strings.Contains(got.Error, "records_list 6 times in a row") {
		t.Fatalf("the sixth same read stops the turn as a loop: %+v", got)
	}
	for _, s := range seen[:min(len(seen), 6)] {
		if strings.Contains(s, "deck: you called") || s != seen[0] {
			t.Fatalf("every read answers the same, without a warning: %q", seen)
		}
	}
}

// TestAShortCommandFromATurnGivesItsResultAtOnce: a script an agent calls gives its result when it ends soon, its
// process when it takes long, the ask when it needs the owner; an agent command gives its process at once.
func TestAShortCommandFromATurnGivesItsResultAtOnce(t *testing.T) {
	t.Parallel()
	outs := make(chan map[string]string, 1)
	release := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "child" {
			<-release
			return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
		}
		got := map[string]string{}
		for _, name := range []string{"quick", "slow", "send", "child"} {
			at := time.Now()
			out, _ := c.Tool(ctx, "waits__"+name, map[string]any{})
			got[name] = out
			got[name+"_took"] = time.Since(at).String()
		}
		outs <- got
		close(release)
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))
	e.Project("waits", waitsManifest)
	e.SaveScript("waits", "scripts/quick.py", "import json\nprint(json.dumps({\"task\": 41}))\n")
	e.SaveScript("waits", "scripts/slow.py", "import time\ntime.sleep(120)\n")
	id := e.Start("waits", "work", nil)
	got := <-outs
	var quick struct {
		Process int64
		State   string
		Result  struct{ Task int }
	}
	if json.Unmarshal([]byte(got["quick"]), &quick) != nil || quick.Process == 0 || quick.State != "done" || quick.Result.Task != 41 {
		t.Fatalf("a short script gives its result in the call: %s", got["quick"])
	}
	var slow struct {
		Process int64
		State   string
		Result  json.RawMessage
	}
	if json.Unmarshal([]byte(got["slow"]), &slow) != nil || slow.Process == 0 || slow.State != "running" && slow.State != "waiting" || slow.Result != nil {
		t.Fatalf("a long script gives its process when the short wait ends: %s (%s)", got["slow"], got["slow_took"])
	}
	if !strings.Contains(got["send"], "owner") {
		t.Fatalf("a call that needs the owner gives what it waits for: %s", got["send"])
	}
	var child struct {
		Process int64
		State   string
	}
	if json.Unmarshal([]byte(got["child"]), &child) != nil || child.Process == 0 || child.State != "" {
		t.Fatalf("an agent command gives its process at once: %s", got["child"])
	}
	if d, _ := time.ParseDuration(got["child_took"]); d > harness.Within(5*time.Second) {
		t.Fatalf("an agent command does not wait: %s", got["child_took"])
	}
	e.Must("deck.process_stop", map[string]any{"process": slow.Process}, nil)
	if p := e.WaitState(id, "done", "error", "waiting:answer"); p.State == "error" {
		t.Fatalf("the work ends: %+v", e.Fault(id))
	}
}
