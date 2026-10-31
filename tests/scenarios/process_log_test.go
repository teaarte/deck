package scenarios

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"deck2/internal/provider"
)

// TestALongLogComesInPages: a log of hundreds of events comes a page at a time, oldest first, each event with its
// number to page by; only errors, tools or messages; a long text cut with how much, whole on a page of one.
func TestALongLogComesInPages(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("x", 3000)
	e := New(t, WithAgent(func(ctx context.Context, c *Call) provider.Result {
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

	id := e.Run(e.Project("plain", plainManifest), "work")
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
