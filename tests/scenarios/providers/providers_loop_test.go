package providers

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"slices"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestASixthSameCallStopsTheTurnAndNothingWarnsBefore: five same calls and a different one go on; six same calls in
// a row stop the turn as a loop, with no warning to the agent before, however its answers quote one.
func TestASixthSameCallStopsTheTurnAndNothingWarnsBefore(t *testing.T) {
	t.Parallel()
	heard := make(chan string, 1)
	fake := &harness.Fake{Live: true, Script: func(ctx context.Context, c *harness.Call) provider.Result {
		call := func(id, cmd string) {
			c.Req.Emit(provider.Event{Kind: provider.EventTool, ID: id, Title: "Bash", Text: `{"command": "` + cmd + `"}`})
			c.Req.Emit(provider.Event{Kind: provider.EventToolUpdate, ID: id, Status: "completed",
				Text: cmd + "\nquoted: deck: you called Bash 9 times in a row with the same input and got the same answer. Then"})
		}
		for i := range 5 {
			call("a"+harness.Itoa(int64(i)), "date")
		}
		call("b", "uptime")
		for i := range 6 {
			call("c"+harness.Itoa(int64(i)), "date")
		}
		select {
		case m := <-c.Req.Live:
			heard <- m.Text
		case <-ctx.Done():
			heard <- ""
		}
		return provider.Result{}
	}}
	e := harness.New(t, harness.WithProvider(fake))
	id := e.Run(e.Project("job", tellManifest), "work")
	e.WaitState(id, "error")
	if fl := e.Fault(id); fl.ErrorType != "agent.loop" || !strings.Contains(fl.Error, "6 times in a row") {
		t.Fatalf("the sixth same call in a row stops the turn as a loop: %+v", fl)
	}
	if m := <-heard; m != "" {
		t.Fatalf("deck says nothing to an agent before it stops its loop: %q", m)
	}
	if journal := fmt.Sprint(e.Log(id)); strings.Contains(journal, "deck: you called Bash 3 times") || !strings.Contains(journal, "deck: the turn stops as a loop") {
		t.Fatalf("the journal tells only the stop: %s", journal)
	}
}

// TestACPAgentThatRepeatsAShellCallStopsAsALoop: a shell call an agent repeats with the same answer, only a counter
// changing, stops the turn as a loop, which the failed processes and the feed tell.
func TestACPAgentThatRepeatsAShellCallStopsAsALoop(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	lines := []string{harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`}
	for i := range 10 {
		cmd := fmt.Sprintf("echo STOP-LOOP-IMMEDIATELY-%d", i)
		lines = append(lines,
			fmt.Sprintf(`{"update": {"sessionUpdate": "tool_call", "toolCallId": "c%d", "title": %q, "kind": "execute", "status": "pending", "rawInput": {"command": %q}}}`, i, cmd, cmd),
			fmt.Sprintf(`{"update": {"sessionUpdate": "tool_call_update", "toolCallId": "c%d", "status": "completed", "content": [{"type": "content", "content": {"type": "text", "text": "STOP-LOOP-IMMEDIATELY-%d"}}]}}`, i, i))
	}
	f.StreamText(4, harness.AcpStream(append(lines, `{"hang": true}`)...))
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	id := e.Run(e.AcpProject("paint", harness.ColorManifest), "work")
	got := e.WaitState(id, "done", "error")
	if fl := e.Fault(id); got.State != "error" || fl.ErrorType != "agent.loop" || !strings.Contains(fl.Error, "6 times in a row") {
		t.Fatalf("the sixth same call, numbers aside, stops the turn as a loop: %+v %+v", got, fl)
	}
	journal := fmt.Sprint(e.Log(id))
	if strings.Contains(journal, "deck: you called") || !strings.Contains(journal, "deck: the turn stops as a loop") {
		t.Fatalf("the journal tells the stop and no warning before: %s", journal)
	}
	failed := false
	for _, r := range e.FailedRuns() {
		failed = failed || r.ID == id && r.ErrorType == "agent.loop"
	}
	if !failed {
		t.Fatalf("the loop is among the failed processes: %+v", e.FailedRuns())
	}
	var failures []struct {
		Process int64
		Error   string
	}
	e.Must("deck.feed_list", map[string]any{"kind": "failure"}, &failures)
	if !slices.ContainsFunc(failures, func(f struct {
		Process int64
		Error   string
	}) bool {
		return f.Process == id && strings.Contains(f.Error, "times in a row")
	}) {
		t.Fatalf("the feed tells the loop: %+v", failures)
	}
}

// TestTheKeyOfTheDoorIsMaskedInTheLog: what an agent prints of its environment reaches the log without the key of
// the door of its box, which its proxy and the URL of its model carry.
func TestTheKeyOfTheDoorIsMaskedInTheLog(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"update": {"sessionUpdate": "tool_call", "toolCallId": "c1", "title": "env", "kind": "execute", "status": "pending", "rawInput": {"command": "env"}}}`,
		`{"update": {"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "completed", "content": [{"type": "content", "content": {"type": "text", "text": "{{env}}"}}]}}`,
		`{"stderr": "{{env}}\n"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	id := e.Run(e.AcpProject("paint", harness.ColorManifest), "work")
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the turn ends: %+v %+v", got, e.Fault(id))
	}
	proxy, err := url.Parse(f.Call(4).Env["HTTP_PROXY"])
	key, _ := proxy.User.Password()
	if err != nil || len(key) < 16 || !strings.Contains(f.Call(4).Env["OPENAI_BASE_URL"], key) {
		t.Fatalf("the agent has the key of its door: %v %+v", err, f.Call(4).Env)
	}
	stored, err := os.ReadFile(e.App.Runner.LogPath(id))
	if err != nil || !strings.Contains(string(stored), "HTTP_PROXY=http://deck:[secret]@") || strings.Contains(string(stored), key) {
		t.Fatalf("the log keeps the environment with the key masked: %v %s", err, stored)
	}
	for _, l := range e.Log(id) {
		if strings.Contains(fmt.Sprint(l), key) {
			t.Fatalf("no line of the journal has the key: %v", l)
		}
	}
}

// TestAPollThatSeesProgressIsNoLoop: the same call whose answer changes, though only in its numbers, is no loop.
func TestAPollThatSeesProgressIsNoLoop(t *testing.T) {
	t.Parallel()
	fake := &harness.Fake{Live: true, Script: func(ctx context.Context, c *harness.Call) provider.Result {
		for i := range 10 {
			id := harness.Itoa(int64(i))
			c.Req.Emit(provider.Event{Kind: provider.EventTool, ID: id, Title: "Bash", Text: `{"command": "tail -1 train.log"}`})
			c.Req.Emit(provider.Event{Kind: provider.EventToolUpdate, ID: id, Status: "completed", Text: "epoch " + id + "/50"})
		}
		return provider.Result{Text: "trained"}
	}}
	e := harness.New(t, harness.WithProvider(fake))
	id := e.Run(e.Project("job", tellManifest), "work")
	if got := e.WaitState(id, "done", "error"); got.State != "done" || strings.Contains(fmt.Sprint(e.Log(id)), "deck: you called") {
		t.Fatalf("a poll that sees progress goes on unwarned: %+v %+v", got, e.Fault(id))
	}
}
