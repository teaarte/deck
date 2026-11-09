package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

type topicWork struct {
	ID    int64
	Busy  bool
	Waits bool
	Since string
}

type runningWork struct {
	ID        int64
	Project   string
	Command   string
	Word      string
	Tone      string
	Waits     bool
	Model     string
	Logo      string
	StartedAt string `json:"started_at"`
	Open      struct {
		Screen string
		Record int64
	}
}

// TestTheChatShowsTheWorkOfATopicAndWhichTopicsWork: the turn part gives the work of a topic that goes on or waits,
// then what ended; the topics say which work and wait, both read again on processes and asks.
func TestTheChatShowsTheWorkOfATopicAndWhichTopicsWork(t *testing.T) {
	t.Parallel()
	hold := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch {
		case c.Command() == "work" && strings.Contains(c.Req.Prompt, "TASKHOLD"):
			select {
			case <-hold:
			case <-ctx.Done():
			}
			return provider.Result{Text: "held"}
		case c.Command() == "work":
			c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{
				{"question": "Which collection?", "options": []map[string]any{{"label": "tasks"}, {"label": "notes"}}}}})
			return provider.Result{Text: "asked"}
		default:
			var in struct{ Topic int64 }
			if m := harness.InputOf.FindStringSubmatch(c.Req.Prompt); m != nil {
				_ = json.Unmarshal([]byte(m[1]), &in)
			}
			for _, task := range []string{"TASKHOLD", "TASKASK"} {
				if strings.Contains(c.Req.Prompt, strings.ToLower(task)) {
					if out, bad := c.Tool(ctx, "master__work", map[string]any{"task": task, "topic": in.Topic}); bad {
						return provider.Result{Text: "refused: " + out}
					}
				}
			}
		}
		return provider.Result{Text: "ok"}
	}))
	defer func() {
		select {
		case <-hold:
		default:
			close(hold)
		}
	}()

	idle := e.Say(0, "hello")
	e.MasterSaid(idle, 1)
	held := e.Say(0, "do taskhold")
	e.MasterSaid(held, 1)
	asked := e.Say(0, "do taskask")
	e.MasterSaid(asked, 1)
	e.WaitAsk()

	work := func(topic int64) (running, recent []runningWork) {
		var v struct{ Running, Recent []runningWork }
		e.Must("deck.chat_turn_get", map[string]any{"topic": fmt.Sprint(topic)}, &v)
		return v.Running, v.Recent
	}
	topics := func() map[int64]topicWork {
		var list []topicWork
		e.ChatTopics(false, &list)
		out := map[int64]topicWork{}
		for _, x := range list {
			out[x.ID] = x
		}
		return out
	}
	var running []runningWork
	e.Until("the work of the topic runs", func() bool {
		running, _ = work(held)
		return len(running) == 1 && strings.HasPrefix(running[0].Word, "runs on")
	})
	r := running[0]
	if r.Project != "master" || r.Command != "work" || r.Waits || r.Model == "" || r.Logo == "" ||
		!strings.HasPrefix(r.StartedAt, "20") || r.Open.Screen != "process" || r.Open.Record != r.ID {
		t.Fatalf("a work that goes on says its command, model and mark, when it started and opens its process: %+v", r)
	}
	running, _ = work(asked)
	if len(running) != 1 || !running[0].Waits || running[0].Tone != "warn" || running[0].Word != "waits for you" {
		t.Fatalf("a work with an open ask waits for the owner: %+v", running)
	}
	if running, recent := work(idle); len(running) != 0 || len(recent) != 0 {
		t.Fatalf("a topic that started nothing has no work: %+v %+v", running, recent)
	}
	got := topics()
	if !got[held].Busy || got[held].Waits || !strings.HasPrefix(got[held].Since, "20") ||
		!got[asked].Busy || !got[asked].Waits || got[idle].Busy {
		t.Fatalf("the topics say which work and which wait for the owner: %+v", got)
	}

	for _, topic := range []string{"processes", "asks"} {
		var paths []string
		for _, m := range e.ScreenMessages("", "chat", map[string]any{"topic": topic}) {
			var dm struct{ Path string }
			_ = json.Unmarshal(m["updateDataModel"], &dm)
			paths = append(paths, dm.Path)
		}
		if !slices.Contains(paths, "/b0-topics") || !slices.Contains(paths, "/b0-turn") {
			t.Fatalf("%s reads the topics and the work again: %v", topic, paths)
		}
	}

	close(hold)
	var recent []runningWork
	e.Until("the work ended goes to the recent", func() bool {
		running, recent = work(held)
		return len(running) == 0 && len(recent) == 1
	})
	if recent[0].ID != r.ID || recent[0].Tone != "ok" || recent[0].Word != "done" {
		t.Fatalf("a work that ended says how: %+v", recent)
	}
	if got := topics(); got[held].Busy || !got[asked].Busy {
		t.Fatalf("a topic whose work ended works no more: %+v", got)
	}
}
