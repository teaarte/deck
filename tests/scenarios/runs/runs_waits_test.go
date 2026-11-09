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

// TestAWeakModelThatPollsWithoutAWaitIsToldHowAndGetsItsResult: an agent that looks at a running process again and
// again without a wait is not stopped as a loop: its first look tells it how to wait, the next waits in deck.
func TestAWeakModelThatPollsWithoutAWaitIsToldHowAndGetsItsResult(t *testing.T) {
	t.Parallel()
	release, looked := make(chan struct{}), make(chan string, 1)
	answers := make(chan []string, 1)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "child" {
			<-release
			return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
		}
		out := toolCall(ctx, c, "start", "waits__child", map[string]any{})
		var started struct{ Process int64 }
		_ = json.Unmarshal([]byte(out), &started)
		var seen []string
		for i := range 6 {
			out := toolCall(ctx, c, "look"+harness.Itoa(int64(i)), "process_get", map[string]any{"process": started.Process})
			seen = append(seen, out)
			if i == 0 {
				looked <- out
			}
			if strings.Contains(out, `"state":"done"`) {
				break
			}
		}
		answers <- seen
		return provider.Result{Structured: json.RawMessage(`{"n": 2}`)}
	}))
	e.Project("waits", waitsManifest)
	id := e.Start("waits", "work", nil)
	if first := <-looked; !strings.Contains(first, `"hint"`) || !strings.Contains(first, "process_get with wait=600") {
		t.Fatalf("a look without a wait at a running process says how to wait: %s", first)
	}
	at := time.Now()
	time.AfterFunc(time.Second, func() { close(release) })
	seen := <-answers
	if len(seen) != 2 || !strings.Contains(seen[1], `"state":"done"`) || time.Since(at) > 30*time.Second {
		t.Fatalf("the second look waits in deck until the process ends: %d looks in %s: %q", len(seen), time.Since(at), seen)
	}
	if p := e.WaitState(id, "done", "error"); p.State != "done" {
		t.Fatalf("the turn that polled is not stopped as a loop: %+v", e.Fault(id))
	}
}

// TestTheSameReadInARowIsWarnedOnceBeforeTheTurnStops: the third same read with the same answer gets a warning with
// how to wait instead of a stop; an agent that heeds it ends its work, one that goes on is stopped as a loop.
func TestTheSameReadInARowIsWarnedOnceBeforeTheTurnStops(t *testing.T) {
	t.Parallel()
	answers := make(chan []string, 2)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		heed := c.N == 1
		var seen []string
		for i := range 8 {
			out := toolCall(ctx, c, "read"+harness.Itoa(int64(i)), "records_list", map[string]any{"project": "waits", "collection": "items"})
			seen = append(seen, out)
			if heed && strings.Contains(out, "deck: you called") {
				break
			}
			if ctx.Err() != nil {
				break
			}
		}
		answers <- seen
		if ctx.Err() != nil {
			return provider.Result{}
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))
	e.Project("waits", waitsManifest)

	heeds := e.Start("waits", "work", nil)
	seen := <-answers
	if len(seen) != 3 || !strings.Contains(seen[2], "deck: you called records_list 3 times in a row") ||
		!strings.Contains(seen[2], "process_get with wait=600") || strings.Contains(seen[1], "deck: you called") {
		t.Fatalf("the third same read is warned of once: %q", seen)
	}
	if p := e.WaitState(heeds, "done", "error"); p.State != "done" {
		t.Fatalf("a warned agent that stops reading ends its work: %+v", e.Fault(heeds))
	}

	goesOn := e.Start("waits", "work", nil)
	seen = <-answers
	e.WaitState(goesOn, "error")
	if got := e.Fault(goesOn); got.ErrorType != "agent.loop" {
		t.Fatalf("an agent that goes on after the warning is stopped as a loop: %+v", got)
	}
	warned := 0
	for _, s := range seen {
		warned += strings.Count(s, "deck: you called")
	}
	if warned != 1 || len(seen) < 6 {
		t.Fatalf("one warning, then the stop after three more same reads: %d warnings in %d reads", warned, len(seen))
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
	if json.Unmarshal([]byte(got["slow"]), &slow) != nil || slow.Process == 0 || slow.State != "running" && slow.State != "waiting_slot" || slow.Result != nil {
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
	if p := e.WaitState(id, "done", "error", "waiting_answer"); p.State == "error" {
		t.Fatalf("the work ends: %+v", e.Fault(id))
	}
}
