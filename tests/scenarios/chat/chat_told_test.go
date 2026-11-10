package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/fault"
	"deck2/internal/provider"
	"deck2/tests/harness"
)

// A work the master started from a chat topic tells that topic how it ended, in the owner's language it was told:
// its result when done, why in the owner's words when it failed, and the note it left in the feed is read then.
func TestAWorkFromATopicTellsTheTopicHowItEnded(t *testing.T) {
	t.Parallel()
	var works atomic.Int32
	var replies atomic.Int64
	var from atomic.Int64
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() != "work" {
			if replies.Add(1) == 3 {
				return provider.Result{Text: "ok"}
			}
			return harness.HandOver(ctx, c, &from)
		}
		switch works.Add(1) {
		case 2:
			return provider.Fail(fault.TaskFailed, "the site is down")
		case 3:
			c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{
				{"question": "Which collection?", "options": []map[string]any{{"label": "tasks"}, {"label": "notes"}}}}})
			return provider.Result{Text: "Waiting for the owner."}
		}
		c.Tool(ctx, "owner_notify", map[string]any{"project": "master", "text": "Counted: 3 records."})
		return provider.Result{Text: "Counted: **3 records**."}
	}))

	e.Must("deck.setting_set", map[string]any{"name": "language", "value": "ru"}, nil)
	topic := e.Say(0, "count the records")
	done := e.Told(topic, "done", "ru")
	for _, c := range e.Fake.Calls()[:2] {
		if !strings.Contains(c.Prompt, "The owner reads deck in Russian") {
			t.Fatalf("the reply and the work it handed over are told the owner's language:\n%s", c.Prompt)
		}
	}
	if done.Text != "Counted: **3 records**." || done.Told.Command != "work" || done.Told.Word != "готово" || done.Told.Tone != "ok" {
		t.Fatalf("a work done tells its result and its state in the owner's words: %+v %+v", done, done.Told)
	}
	if done.Told.Open.Screen != "process" || done.Told.Open.Record != done.Process || done.Process == 0 {
		t.Fatalf("the message opens the process of the work: %+v", done.Told)
	}
	e.Until("the note of the work is read", func() bool {
		for _, n := range e.Notes() {
			if strings.Contains(n.Text, "Counted: 3 records.") {
				return n.Read
			}
		}
		return false
	})
	var view struct{ Messages []harness.ToldMessage }
	e.ChatRead(map[string]any{"topic": fmt.Sprint(topic)}, &view)
	deck := 0
	for _, m := range view.Messages {
		if m.Role == "deck" {
			deck++
		}
	}
	if deck != 1 {
		t.Fatalf("one message of deck for one work: %+v", view.Messages)
	}

	e.Say(topic, "count again")
	failed := e.Told(topic, "error", "ru")
	if failed.Told.Reason != "агент сообщил об ошибке" || !strings.Contains(failed.Told.Error, "the site is down") || failed.Told.Tone != "error" {
		t.Fatalf("a work that failed says why in the owner's words: %+v", failed.Told)
	}
	e.Until("the note of the failed work reads in Russian", func() bool {
		var notes []harness.Note
		e.Must("deck.feed_list", map[string]any{"kind": "note", "lang": "ru"}, &notes)
		for _, n := range notes {
			if n.Text == "Команда мастера work не удалась: агент сообщил об ошибке: the site is down" {
				return true
			}
		}
		return false
	})
	e.Say(topic, "and now?")
	e.MasterSaid(topic, 3)
	var after struct {
		Topic struct {
			Reply *harness.TopicReply `json:"reply"`
		} `json:"topic"`
	}
	// The reply's words come a moment before its process ends; a read without a reply leaves none of the last read.
	e.Until("the third reply to end", func() bool {
		after.Topic.Reply = nil
		e.ChatRead(map[string]any{"topic": fmt.Sprint(topic)}, &after)
		return after.Topic.Reply == nil || after.Topic.Reply.State != "running"
	})
	if after.Topic.Reply != nil && after.Topic.Reply.State != "done" {
		t.Fatalf("a message of deck does not make its work the reply of the topic: %+v", after.Topic.Reply)
	}

	e.Say(topic, "count the notes")
	waits := e.Told(topic, "waiting_answer", "ru")
	if !strings.HasPrefix(waits.Told.Word, "ждёт вашего ответа") || !strings.Contains(waits.Told.Word, "Which collection?") || waits.Told.Tone != "warn" {
		t.Fatalf("a work that waits for the owner says what it waits for: %+v", waits.Told)
	}
}

// A work sent back for rework tells its topic again how the new run ended.
func TestAWorkSentBackTellsItsTopicAgain(t *testing.T) {
	t.Parallel()
	var from atomic.Int64
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch {
		case strings.Contains(c.Req.Prompt, "count the drafts too"):
			return provider.Result{Text: "Recounted: 4 records."}
		case c.Command() == "work":
			return provider.Result{Text: "Counted: 3 records."}
		}
		return harness.HandOver(ctx, c, &from)
	}))

	topic := e.Say(0, "count the records")
	done := e.Told(topic, "done", "")
	e.Must("deck.process_rework", map[string]any{"process": done.Process, "remark": "count the drafts too"}, nil)
	e.Until("the new result in the topic", func() bool {
		var view struct{ Messages []harness.ToldMessage }
		e.ChatRead(map[string]any{"topic": fmt.Sprint(topic)}, &view)
		var said []string
		for _, m := range view.Messages {
			if m.Role == "deck" {
				said = append(said, m.Text)
			}
		}
		return strings.Join(said, " | ") == "Counted: 3 records. | Recounted: 4 records."
	})
}

// A long result of a work comes to its topic whole, and the master's next turn reads it whole.
func TestAWorkTellsItsTopicItsWholeResult(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("Шаг сделан, одна фраза — одно дело. ", 90) + "Что заводить первым?"
	var from atomic.Int64
	var replies atomic.Int32
	var prompt atomic.Value
	worked := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "work" {
			defer close(worked)
			return provider.Result{Text: long}
		}
		if replies.Add(1) == 1 {

			res := harness.HandOver(ctx, c, &from)
			<-worked
			time.Sleep(300 * time.Millisecond)
			return res
		}
		prompt.Store(c.Req.Prompt)
		return provider.Result{Text: "ok"}
	}))

	topic := e.Say(0, "plan it")
	if done := e.Told(topic, "done", ""); done.Text != long {
		t.Fatalf("the topic gets the whole result of %d bytes, not %d", len(long), len(done.Text))
	}
	e.Say(topic, "and?")
	e.MasterSaid(topic, 2)
	if p, _ := prompt.Load().(string); !strings.Contains(p, "Что заводить первым?") {
		t.Fatalf("the master reads the whole result:\n%s", p)
	}
}

// A work from a topic that failed tells what it left and goes on in its session at the owner's press.
func TestAFailedWorkGoesOnFromItsTopic(t *testing.T) {
	t.Parallel()
	var from atomic.Int64
	var works atomic.Int32
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch {
		case strings.Contains(c.Req.Prompt, "go on where you stopped"):
			return provider.Result{Text: "Finished the rest."}
		case c.Command() != "work":
			return harness.HandOver(ctx, c, &from)
		case works.Add(1) == 1:
			return provider.Fail(fault.TaskFailed, "the site is down")
		}
		return provider.Result{Text: "a second fresh run"}
	}))

	topic := e.Say(0, "do it")
	failed := e.Told(topic, "error", "")
	var view struct{ Messages []json.RawMessage }
	e.ChatRead(map[string]any{"topic": fmt.Sprint(topic)}, &view)
	var goOn struct {
		Told struct {
			Go struct {
				Command string         `json:"command"`
				Params  map[string]any `json:"params"`
			} `json:"go"`
		} `json:"told"`
	}
	for _, raw := range view.Messages {
		if strings.Contains(string(raw), `"go"`) {
			_ = json.Unmarshal(raw, &goOn)
		}
	}
	if goOn.Told.Go.Command != "deck.process_rework" {
		t.Fatalf("a failed work offers to go on: %+v", view.Messages)
	}
	e.Must(goOn.Told.Go.Command, goOn.Told.Go.Params, nil)
	if done := e.Told(topic, "done", ""); done.Text != "Finished the rest." || done.Process != failed.Process {
		t.Fatalf("the same work goes on in its session: %+v", done)
	}
}

// TestAWorkHandedOnTellsItsTopicWhereItWentOn: a work of a topic whose model refused it tells the topic it restarted
// as which process on which model, and then how that one ended.
func TestAWorkHandedOnTellsItsTopicWhereItWentOn(t *testing.T) {
	t.Parallel()
	var from atomic.Int64
	main := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() != "work" {
			return harness.HandOver(ctx, c, &from)
		}
		return harness.Limited(harness.T0)
	}}
	backup := &harness.Fake{ProviderName: "backup", Script: func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Text: "Counted: 3 records."}
	}}
	e := harness.New(t, harness.WithProvider(main, backup))
	e.UseModels("master.work", "fake", "backup")
	e.Must("deck.setting_set", map[string]any{"name": "language", "value": "ru"}, nil)
	topic := e.Say(0, "count the records")
	failed := e.Told(topic, "error", "ru")
	next := e.Following(failed.Process)
	if want := fmt.Sprintf("перезапущена как #%d на модели backup", next); !strings.Contains(failed.Told.Error, want) {
		t.Fatalf("the topic reads where the work went on: %q, want %q", failed.Told.Error, want)
	}
	if done := e.Told(topic, "done", "ru"); done.Process != next || done.Text != "Counted: 3 records." {
		t.Fatalf("then how the work that went on ended: %+v", done)
	}
}

// An agent whose CLI began its session before deck's MCP server answered fails its first call that needs the owner's
// leave and exits; the work goes on in its session, and its topic tells no failure while it lives.
func TestAWorkWhoseAgentBeganWithoutDecksToolsGoesOnAndItsTopicTellsNoFailure(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	c.Before(`
if [ -n "$session" ] && [ ! -f "$d/began" ]; then
  : > "$d/began"
  sed "s/{{session}}/$session/g" <<'L'
{"type":"system","subtype":"init","session_id":"{{session}}","model":"claude-opus-5-5","mcp_servers":[{"name":"deck","status":"failed"}]}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","id":"toolu_1","name":"Bash","input":{"command":"ls"}}]},"session_id":"{{session}}"}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_1","is_error":true,"content":"<tool_use_error>Error calling tool (Bash): Error: MCP tool mcp__deck__approve (passed via --permission-prompt-tool) not found. Available MCP tools: none</tool_use_error>"}]},"session_id":"{{session}}"}
L
  echo "Error: MCP tool mcp__deck__approve (passed via --permission-prompt-tool) not found. Available MCP tools: none" >&2
  exit 1
fi
if [ -n "$session" ]; then
  i=0
  while [ ! -f "$d/go" ] && [ $i -lt 1200 ]; do sleep 0.1; i=$((i+1)); done
fi`)
	var from atomic.Int64
	home := t.TempDir()
	reply := &harness.Fake{Script: func(ctx context.Context, call *harness.Call) provider.Result {
		return harness.HandOver(ctx, call, &from)
	}}
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(reply, c.Adapter(home)))
	e.UseModels("master.reply", "fake")
	e.UseModels("master.work", "claude-code")
	topic := e.Say(0, "count the records")
	var id int64
	e.Until("the work runs again in its session", func() bool {
		if runs := e.RunsOf("master", "work"); len(runs) == 1 {
			id = runs[0].ID
		}
		return id != 0 && c.Calls() >= 2 && strings.Contains(c.Call(2).Stdin, `"type":"user"`)
	})
	if p := e.Proc(id); p.State != "running" {
		t.Fatalf("the work lives on: %+v", p)
	}
	again := c.Call(2)
	if sid, ok := harness.Flag(again.Args, "--resume"); !ok || sid != e.Session(id) {
		t.Fatalf("the work goes on in the session it began: %q", again.Args)
	}
	if !strings.Contains(again.Stdin, "deck's tools reached your session late") {
		t.Fatalf("the agent is told why its call failed: %s", again.Stdin)
	}
	var view struct{ Messages []harness.ToldMessage }
	e.ChatRead(map[string]any{"topic": fmt.Sprint(topic)}, &view)
	for _, m := range view.Messages {
		if m.Role == "deck" {
			t.Fatalf("a work that lives tells its topic no end: %+v", m)
		}
	}
	if err := os.WriteFile(filepath.Join(c.Dir, "go"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if done := e.Told(topic, "done", "en"); done.Process != id {
		t.Fatalf("then how it ended: %+v", done)
	}
	if runs := e.RunsOf("master", "on_error"); len(runs) != 0 {
		t.Fatalf("no failure reached the master: %+v", runs)
	}
}

// A work that failed and was sent on in its session tells its topic how it stands now, not the failure, until it ends
// again.
func TestAFailedWorkSentOnTellsItsTopicItRunsAgain(t *testing.T) {
	t.Parallel()
	var from atomic.Int64
	release := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch {
		case c.Req.Resume:
		case c.Command() != "work" && from.Load() != 0:
			return provider.Result{Text: "ok"}
		case c.Command() != "work":
			return harness.HandOver(ctx, c, &from)
		default:
			return provider.Fail(fault.TaskFailed, "the site is down")
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.Result{Text: "Counted: 3 records."}
	}))
	topic := e.Say(0, "count the records")
	failed := e.Told(topic, "error", "en")
	e.Must("deck.process_rework", map[string]any{"process": failed.Process}, nil)
	e.WaitState(failed.Process, "running")
	if again := e.Told(topic, "running", "en"); again.Text != failed.Text || again.Told.Tone == "error" || !strings.HasPrefix(again.Told.Word, "runs on") {
		t.Fatalf("the message of the failure tells the work runs again: %+v %+v", again, again.Told)
	}
	close(release)
	if done := e.Told(topic, "done", "en"); done.Process != failed.Process || done.Text != "Counted: 3 records." {
		t.Fatalf("then a message of its new end: %+v", done)
	}
	if was := e.Told(topic, "error", "en"); was.Text != failed.Text {
		t.Fatalf("and the first message tells its failure again: %+v", was)
	}
}
