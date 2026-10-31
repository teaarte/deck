package runs

import (
	"context"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

// TestALongLogComesInPages: a log of hundreds of events comes a page at a time, oldest first, each event with its
// number to page by; only errors, tools or messages; a long text cut with how much, whole on a page of one.
func TestALongLogComesInPages(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 3000)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		for i := range 180 {
			id := fmt.Sprintf("t%d", i)
			c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: id, Title: "read", Text: `{"n": ` + fmt.Sprint(i) + `}`})
			status := "completed"
			if i%10 == 9 {
				status = "failed"
			}
			c.Req.OnEvent(provider.Event{Kind: provider.EventToolUpdate, ID: id, Status: status, Text: long})
			if i%10 == 0 {
				c.Req.OnEvent(provider.Event{Kind: provider.EventMessage, Text: fmt.Sprintf("step %d", i)})
			}
		}
		c.Req.OnEvent(provider.Event{Kind: provider.EventStderr, Text: "warning: disk"})
		return provider.Result{Text: "ok"}
	}))

	id := e.Run(e.Project("plain", harness.PlainManifest), "work")
	e.WaitState(id, "done", "error")

	type event struct {
		N                      int64
		Kind, ID, Status, Text string
	}
	page := func(in map[string]any) []event {
		in["process"] = id
		var out []event
		e.Must("deck.process_log", in, &out)
		return out
	}
	last := page(map[string]any{})
	if len(last) != 50 || last[49].N != 379 || last[0].N != 330 || last[49].Kind != "stderr" {
		t.Fatalf("the last 50 events, oldest first, numbered in the log: %d, %+v .. %+v", len(last), last[0], last[len(last)-1])
	}
	for i := 1; i < len(last); i++ {
		if last[i].N != last[i-1].N+1 {
			t.Fatalf("a page goes in order: %d after %d", last[i].N, last[i-1].N)
		}
	}
	cut := ""
	for _, ev := range last {
		if ev.Kind == "tool_update" {
			cut = ev.Text
			break
		}
	}
	if !strings.Contains(cut, "[1000 of 3000 bytes;") || len(cut) > 1100 {
		t.Fatalf("a long output is cut and says how much: %q", cut[max(0, len(cut)-80):])
	}
	before := page(map[string]any{"before": last[0].N})
	if len(before) != 50 || before[49].N != last[0].N-1 {
		t.Fatalf("the page before one read: %+v", before[len(before)-1])
	}
	first := page(map[string]any{"after": 0, "limit": 3})
	if len(first) != 3 || first[0].N != 1 || first[0].Kind != "tool" || first[1].Kind != "tool_update" {
		t.Fatalf("after 0 reads from the start: %+v", first)
	}
	whole := page(map[string]any{"after": 1, "limit": 1})
	if len(whole) != 1 || whole[0].Text != long {
		t.Fatalf("a page of one event gives its text whole: %d bytes", len(whole[0].Text))
	}
	errs := page(map[string]any{"only": "errors", "limit": 1000})
	if len(errs) != 37 || errs[0].Kind != "tool" || errs[0].ID != "t9" || errs[1].Status != "failed" || errs[36].Kind != "stderr" {
		t.Fatalf("errors are the failed calls with their calls and stderr: %d, %+v", len(errs), errs[:2])
	}
	msgs := page(map[string]any{"only": "messages", "limit": 1000})
	if len(msgs) != 18 || msgs[0].Text != "step 0" {
		t.Fatalf("only the messages: %d", len(msgs))
	}
	tools := page(map[string]any{"only": "tools", "after": 0, "limit": 1000})
	if len(tools) != 360 {
		t.Fatalf("only the tool calls and their results: %d", len(tools))
	}
	var short []map[string]any
	e.Must("deck.process_log", map[string]any{"process": id, "fields": []string{"n", "kind"}, "limit": 2}, &short)
	if len(short) != 2 || len(short[0]) != 2 {
		t.Fatalf("fields cut each event: %v", short)
	}
}

// TestALineOfMediaSaysWhyItHasNoFileInWords: an expression of a line that fails names itself and the fault,
// without the drawing of where in it.
func TestALineOfMediaSaysWhyItHasNoFileInWords(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("btc", strings.Replace(harness.ForecastManifest, `"item": {"file": "file", "title": "title"}`, `"item": {"file": "int(file)", "title": "title"}`, 1))
	e.Must("deck.records_upsert", map[string]any{"project": "btc", "collection": "attachments",
		"records": harness.Jsonl(map[string]any{"title": "a", "file": "image-1b946491.png"})}, nil)
	raw, _ := json.Marshal(e.ScreenMessages("btc", "files", nil))
	if s := string(raw); !strings.Contains(s, `the file of this line: int(file): `) || strings.Contains(s, `| ^`) || strings.Contains(s, `(1:1)`) {
		t.Fatalf("the line says in words why it has no file: %s", s)
	}
}

// TestTheScreenOfAScriptShowsItsInputAndResult: a script has no log of its own, so its screen shows what it got and
// gave, and says why the log is empty.
func TestTheScreenOfAScriptShowsItsInputAndResult(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("tools", harness.ScriptManifest("run", "", ""))
	e.SaveScript("tools", "scripts/main.py", `import json, sys
print(json.dumps({"added": 7}))
`)
	id := e.Start("tools", "run", map[string]any{"n": 3})
	e.WaitState(id, "done")
	raw, _ := json.Marshal(e.ScreenMessages("", "process", map[string]any{"record": id}))
	s := string(raw)
	if !strings.Contains(s, `\"n\": 3`) || !strings.Contains(s, `\"added\": 7`) || !strings.Contains(s, "Nothing in the log") {
		t.Fatalf("the screen shows the input, the result and why the log is empty: %s", s)
	}
}

// TestAThoughtWithoutTextIsNoLineOfTheLog: a thinking block a model keeps to itself reaches the log as nothing,
// not as an empty line.
func TestAThoughtWithoutTextIsNoLineOfTheLog(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		c.Req.OnEvent(provider.Event{Kind: provider.EventThought, Text: ""})
		c.Req.OnEvent(provider.Event{Kind: provider.EventThought, Text: "Checking the dates."})
		c.Req.OnEvent(provider.Event{Kind: provider.EventMessage, Text: "  "})
		return provider.Result{Text: "ok"}
	}))

	id := e.Run(e.Project("plain", harness.PlainManifest), "work")
	e.WaitState(id, "done", "error")
	var lines []struct{ Kind, Text string }
	e.Must("deck.process_log", map[string]any{"process": id}, &lines)
	for _, l := range lines {
		if strings.TrimSpace(l.Text) == "" && (l.Kind == "thought" || l.Kind == "message") {
			t.Fatalf("an empty thought or message is no line: %+v", lines)
		}
	}
	if len(lines) == 0 {
		t.Fatal("the thought with text is a line")
	}
}
