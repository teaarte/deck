package runs

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// onceManifest has a lead that calls works of a topic, a review that runs one at a time, a plan without a result and
// a document the master's works edit.
var onceManifest = harness.Manifest(`{"collections": [], "writes": ["notes.md"],
  "commands": [
    {"name": "lead", "title": "Lead", "description": "Leads.", "class": "internal", "runs": "many",
     "executor": {"type": "agent", "prompt": "Lead."}, "result": {"type": "object", "properties": {"n": {"type": "integer"}}}},
    {"name": "work", "title": "Work", "description": "Works on a topic.", "class": "internal", "runs": "many",
     "input": {"type": "object", "properties": {"topic": {"type": "integer"}, "task": {"type": "string"}}},
     "executor": {"type": "agent", "prompt": "Work."}, "result": {"type": "object", "properties": {"n": {"type": "integer"}}}},
    {"name": "review", "title": "Review", "description": "Reviews.", "class": "internal",
     "input": {"type": "object", "properties": {"n": {"type": "integer"}}},
     "executor": {"type": "agent", "prompt": "Review."}, "result": {"type": "object", "properties": {"n": {"type": "integer"}}}},
    {"name": "plan", "title": "Plan", "description": "Plans.", "class": "internal",
     "executor": {"type": "agent", "prompt": "Plan."}}]}`)

type started struct {
	Process int64
	Running bool
	Note    string
}

func startedOf(t *testing.T, out string) started {
	t.Helper()
	var s started
	if json.Unmarshal([]byte(out), &s) != nil || s.Process == 0 {
		t.Fatalf("a call starts a process: %s", out)
	}
	return s
}

// TestTheSameWorkCalledAgainFromATurnGivesTheOneGoing: a turn calling the same work of a topic, or of a command that
// runs one at a time, gets the one going; a retry key of its own starts another; the owner's start waits its turn.
func TestTheSameWorkCalledAgainFromATurnGivesTheOneGoing(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	outs := make(chan []string, 1)
	var once sync.Once
	free := func() { once.Do(func() { close(release) }) }
	defer free()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() != "lead" {
			<-release
			return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
		}
		var got []string
		for _, call := range []map[string]any{{"topic": 5}, {"topic": 5}, {"topic": 6}, {"topic": 5, "retry_key": "beside"}, {"topic": 5, "task": "more"}} {
			out, _ := c.Tool(ctx, "once__work", call)
			got = append(got, out)
		}
		for _, n := range []int{1, 1, 2} {
			out, _ := c.Tool(ctx, "once__review", map[string]any{"n": n})
			got = append(got, out)
		}
		outs <- got
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))
	e.Project("once", onceManifest)
	lead := e.Start("once", "lead", nil)
	got := <-outs
	first, again, other, beside := startedOf(t, got[0]), startedOf(t, got[1]), startedOf(t, got[2]), startedOf(t, got[3])
	if again.Process != first.Process || !again.Running || !strings.Contains(again.Note, "already going,") ||
		!strings.Contains(again.Note, "process_chat_get") || !strings.Contains(again.Note, "retry_key") {
		t.Fatalf("the same work in the same topic gives the one going with what to do: %+v %+v", first, again)
	}
	if other.Process == first.Process || beside.Process == first.Process || beside.Process == other.Process || other.Note != "" {
		t.Fatalf("another topic and a retry key of its own start their own: %+v %+v %+v", first, other, beside)
	}
	if more := startedOf(t, got[4]); more.Process != beside.Process || !strings.Contains(more.Note, "gave it your call as a message") {
		t.Fatalf("another task of the topic goes to the newest work going there as a message: %+v", more)
	}
	e.Until("the work of the topic is told the other task", func() bool {
		var chat struct{ Lines []struct{ Text string } }
		e.Must("deck.process_chat_get", map[string]any{"process": beside.Process}, &chat)
		for _, m := range chat.Lines {
			if strings.Contains(m.Text, "called again in your topic") && strings.Contains(m.Text, "more") {
				return true
			}
		}
		return false
	})
	review, reviewAgain, other2 := startedOf(t, got[5]), startedOf(t, got[6]), startedOf(t, got[7])
	if reviewAgain.Process != review.Process || !strings.Contains(reviewAgain.Note, "already going,") {
		t.Fatalf("a command that runs one at a time gives the one going for the same input: %+v %+v", review, reviewAgain)
	}
	if other2.Process == review.Process || other2.Note != "" {
		t.Fatalf("another input of a command that runs one at a time waits its turn as its own: %+v", other2)
	}
	if owners := e.Start("once", "review", map[string]any{"n": 1}); owners == review.Process {
		t.Fatalf("the owner's start of the command waits its turn as before, not joined")
	}
	free()
	if p := e.WaitState(lead, "done", "error"); p.State != "done" {
		t.Fatalf("the lead ends: %+v", e.Fault(lead))
	}
}

// TestAWholeTextOverWhatAnotherTurnSavedIsAConflict: a turn of the master that saves a whole document or manifest over a revision
// another saved after the one it read is refused with who saved what and why; a change of parts goes on top.
func TestAWholeTextOverWhatAnotherTurnSavedIsAConflict(t *testing.T) {
	t.Parallel()
	read, saved := make(chan struct{}), make(chan int64, 1)
	outs := make(chan []string, 1)
	var works atomic.Int32
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		var in struct{ Topic int64 }
		_ = json.Unmarshal([]byte(harness.InputOf.FindStringSubmatch(c.Req.Prompt)[1]), &in)
		if c.Command() != "work" {
			c.Tool(ctx, "master__work", map[string]any{"task": "edit the notes", "topic": in.Topic})
			return provider.Result{Text: "On it."}
		}
		doc := map[string]any{"project": "once", "name": "notes.md"}
		if works.Add(1) == 2 {
			<-read
			c.Tool(ctx, "document_get", doc)
			c.Tool(ctx, "document_save", map[string]any{"project": "once", "name": "notes.md", "text": "B wrote this", "reason": "B's view"})
			m, _ := c.Tool(ctx, "manifest_get", map[string]any{"project": "once"})
			var mv struct{ Manifest string }
			_ = json.Unmarshal([]byte(m), &mv)
			c.Tool(ctx, "manifest_save", map[string]any{"project": "once", "reason": "B's lead",
				"manifest": strings.Replace(mv.Manifest, `"Leads."`, `"Leads the work."`, 1)})
			saved <- 1
			return provider.Result{Text: "B saved"}
		}
		c.Tool(ctx, "document_get", doc)
		m, _ := c.Tool(ctx, "manifest_get", map[string]any{"project": "once"})
		var mv struct{ Manifest string }
		_ = json.Unmarshal([]byte(m), &mv)
		close(read)
		<-saved
		var got []string
		out, _ := c.Tool(ctx, "document_save", map[string]any{"project": "once", "name": "notes.md", "text": "A wrote this"})
		got = append(got, out)
		out, _ = c.Tool(ctx, "manifest_save", map[string]any{"project": "once",
			"manifest": strings.Replace(mv.Manifest, `"Plans."`, `"Plans ahead."`, 1)})
		got = append(got, out)
		out, _ = c.Tool(ctx, "document_save", map[string]any{"project": "once", "name": "notes.md", "old": []string{"B wrote this"}, "new": []string{"B wrote this, A added this"}})
		got = append(got, out)
		outs <- got
		return provider.Result{Text: "A saved"}
	}))
	e.Project("once", onceManifest)
	e.Must("deck.document_save", map[string]any{"project": "once", "name": "notes.md", "kind": "doc", "text": "the owner wrote this"}, nil)
	e.Say(0, "edit the notes")
	e.Say(0, "edit the notes too")
	got := <-outs
	if !strings.Contains(got[0], "notes.md changed since you read revision 1") || !strings.Contains(got[0], "revision 2 by master") ||
		!strings.Contains(got[0], "B's view") {
		t.Fatalf("a whole text over another's save is a conflict that names it: %s", got[0])
	}
	if !strings.Contains(got[1], "changed since you read revision") || !strings.Contains(got[1], "by master") || !strings.Contains(got[1], "B's lead") {
		t.Fatalf("a whole manifest over another's save is a conflict that names it: %s", got[1])
	}
	if strings.Contains(got[2], "deck:") || strings.Contains(got[2], "changed since") {
		t.Fatalf("a change of parts goes on top of the other's: %s", got[2])
	}
	var doc struct{ Text string }
	e.Must("deck.document_get", map[string]any{"project": "once", "name": "notes.md"}, &doc)
	if doc.Text != "B wrote this, A added this" {
		t.Fatalf("nothing the other saved is lost: %q", doc.Text)
	}
}

// TestATurnThatEndsWhileItsWorkRunsIsNotDone: a turn that ends without a result while what it started still runs,
// as one that says it waits, is not done but failed for not waiting.
func TestATurnThatEndsWhileItsWorkRunsIsNotDone(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "review" {
			<-release
			return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
		}
		c.Tool(ctx, "once__review", map[string]any{})
		return provider.Result{Text: "Waiting for the review to finish…"}
	}))
	e.Project("once", onceManifest)
	plan := e.Start("once", "plan", nil)
	got := e.WaitState(plan, "done", "error")
	close(release)
	if f := e.Fault(plan); got.State != "error" || f.ErrorType != "agent.left" || !strings.Contains(f.Error, "review") ||
		!strings.Contains(f.Error, "process_get with wait") {
		t.Fatalf("a turn that left its work running is not done: %+v %+v", got, f)
	}
}
