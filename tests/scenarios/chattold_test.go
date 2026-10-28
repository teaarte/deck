package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/provider"
)

type toldMessage struct {
	Role    string `json:"role"`
	Text    string `json:"text"`
	Process int64  `json:"process"`
	Told    *struct {
		ID      int64  `json:"id"`
		Project string `json:"project"`
		Command string `json:"command"`
		State   string `json:"state"`
		Word    string `json:"word"`
		Tone    string `json:"tone"`
		Reason  string `json:"reason"`
		Error   string `json:"error"`
		Open    struct {
			Screen string `json:"screen"`
			Record int64  `json:"record"`
		} `json:"open"`
	} `json:"told"`
}

// told waits for the message deck writes into topic of a work in state, read in lang.
func (e *Env) told(topic int64, state, lang string) toldMessage {
	e.T.Helper()
	var got toldMessage
	e.waitFor("deck tells the topic of a work "+state, func() bool {
		var view struct{ Messages []toldMessage }
		e.chatRead(map[string]any{"topic": fmt.Sprint(topic), "lang": lang}, &view)
		for _, m := range view.Messages {
			if m.Role == "deck" && m.Told != nil && m.Told.State == state {
				got = m
				return true
			}
		}
		return false
	})
	return got
}

// handOver is a reply of the master that hands the owner's task to master.work with its topic; a resumed reply
// names no command, and its topic is the one the first named.
func handOver(ctx context.Context, c *Call, topic *atomic.Int64) provider.Result {
	var in struct{ Topic int64 }
	if m := inputOf.FindStringSubmatch(c.Req.Prompt); m != nil && json.Unmarshal([]byte(m[1]), &in) == nil && in.Topic != 0 {
		topic.Store(in.Topic)
	}
	if out, bad := c.Tool(ctx, "master__work", map[string]any{"task": "count the records", "topic": topic.Load()}); bad {
		return provider.Result{Text: "refused: " + out}
	}
	return provider.Result{Text: "Handed it over."}
}

// A work the master started from a chat topic tells that topic how it ended, in the owner's language it was told:
// its result when done, why in the owner's words when it failed, and the note it left in the feed is read then.
func TestAWorkFromATopicTellsTheTopicHowItEnded(t *testing.T) {
	t.Parallel()
	var works atomic.Int32
	var replies atomic.Int64
	var from atomic.Int64
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() != "work" {
			if replies.Add(1) == 3 {
				return provider.Result{Text: "ok"}
			}
			return handOver(ctx, c, &from)
		}
		switch works.Add(1) {
		case 2:
			return provider.Result{Outcome: provider.Failed, Text: "the site is down"}
		case 3:
			c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{
				{"question": "Which collection?", "options": []map[string]any{{"label": "tasks"}, {"label": "notes"}}}}})
			return provider.Result{Text: "Waiting for the owner."}
		}
		c.Tool(ctx, "notify", map[string]any{"project": "master", "text": "Counted: 3 records."})
		return provider.Result{Text: "Counted: **3 records**."}
	})
	e.must("deck.settings_set", map[string]any{"name": "language", "value": "ru"}, nil)
	topic := e.say(0, "count the records")
	done := e.told(topic, "done", "ru")
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
	e.waitFor("the note of the work is read", func() bool {
		for _, n := range e.notes() {
			if strings.Contains(n.Text, "Counted: 3 records.") {
				return n.Read
			}
		}
		return false
	})
	var view struct{ Messages []toldMessage }
	e.chatRead(map[string]any{"topic": fmt.Sprint(topic)}, &view)
	deck := 0
	for _, m := range view.Messages {
		if m.Role == "deck" {
			deck++
		}
	}
	if deck != 1 {
		t.Fatalf("one message of deck for one work: %+v", view.Messages)
	}

	e.say(topic, "count again")
	failed := e.told(topic, "error", "ru")
	if failed.Told.Reason != "агент сообщил об ошибке" || !strings.Contains(failed.Told.Error, "the site is down") || failed.Told.Tone != "error" {
		t.Fatalf("a work that failed says why in the owner's words: %+v", failed.Told)
	}
	e.say(topic, "and now?")
	e.masterSaid(topic, 3)
	var after struct {
		Topic struct {
			Reply *topicReply `json:"reply"`
		} `json:"topic"`
	}
	e.chatRead(map[string]any{"topic": fmt.Sprint(topic)}, &after)
	if after.Topic.Reply != nil && after.Topic.Reply.State != "done" {
		t.Fatalf("a message of deck does not make its work the reply of the topic: %+v", after.Topic.Reply)
	}

	e.say(topic, "count the notes")
	waits := e.told(topic, "waiting_answer", "ru")
	if !strings.HasPrefix(waits.Told.Word, "ждёт вашего ответа") || !strings.Contains(waits.Told.Word, "Which collection?") || waits.Told.Tone != "warn" {
		t.Fatalf("a work that waits for the owner says what it waits for: %+v", waits.Told)
	}
}

// A work sent back for rework tells its topic again how the new run ended.
func TestAWorkSentBackTellsItsTopicAgain(t *testing.T) {
	t.Parallel()
	var from atomic.Int64
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		switch {
		case strings.Contains(c.Req.Prompt, "count the drafts too"):
			return provider.Result{Text: "Recounted: 4 records."}
		case c.command() == "work":
			return provider.Result{Text: "Counted: 3 records."}
		}
		return handOver(ctx, c, &from)
	})
	topic := e.say(0, "count the records")
	done := e.told(topic, "done", "")
	e.must("deck.process_rework", map[string]any{"process": done.Process, "remark": "count the drafts too"}, nil)
	e.waitFor("the new result in the topic", func() bool {
		var view struct{ Messages []toldMessage }
		e.chatRead(map[string]any{"topic": fmt.Sprint(topic)}, &view)
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
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "work" {
			defer close(worked)
			return provider.Result{Text: long}
		}
		if replies.Add(1) == 1 {
			// The work ends and tells the topic while the reply that started it still runs.
			res := handOver(ctx, c, &from)
			<-worked
			time.Sleep(300 * time.Millisecond)
			return res
		}
		prompt.Store(c.Req.Prompt)
		return provider.Result{Text: "ok"}
	})
	topic := e.say(0, "plan it")
	if done := e.told(topic, "done", ""); done.Text != long {
		t.Fatalf("the topic gets the whole result of %d bytes, not %d", len(long), len(done.Text))
	}
	e.say(topic, "and?")
	e.masterSaid(topic, 2)
	if p, _ := prompt.Load().(string); !strings.Contains(p, "Что заводить первым?") {
		t.Fatalf("the master reads the whole result:\n%s", p)
	}
}

// A work from a topic that failed tells what it left and goes on in its session at the owner's press.
func TestAFailedWorkGoesOnFromItsTopic(t *testing.T) {
	t.Parallel()
	var from atomic.Int64
	var works atomic.Int32
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		switch {
		case strings.Contains(c.Req.Prompt, "go on where you stopped"):
			return provider.Result{Text: "Finished the rest."}
		case c.command() != "work":
			return handOver(ctx, c, &from)
		case works.Add(1) == 1:
			return provider.Result{Outcome: provider.Failed, Text: "the site is down"}
		}
		return provider.Result{Text: "a second fresh run"}
	})
	topic := e.say(0, "do it")
	failed := e.told(topic, "error", "")
	var view struct{ Messages []json.RawMessage }
	e.chatRead(map[string]any{"topic": fmt.Sprint(topic)}, &view)
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
	e.must(goOn.Told.Go.Command, goOn.Told.Go.Params, nil)
	if done := e.told(topic, "done", ""); done.Text != "Finished the rest." || done.Process != failed.Process {
		t.Fatalf("the same work goes on in its session: %+v", done)
	}
}
