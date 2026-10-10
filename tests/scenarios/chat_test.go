package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/internal/registry"
)

func TestOwnerMessageGetsAMasterReplyInTheTopic(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Text: "Put it into the inbox."}
	})
	topic := e.say(0, "Remember to buy milk\nand bread", map[string]any{"screen": `{"project":"master","screen":"documents","record":7}`,
		"retry_key": "k1"})
	said := e.masterSaid(topic, 1)
	if said[0].Text != "Put it into the inbox." {
		t.Fatalf("the final text of the turn is the master's reply: %+v", said)
	}
	var ts []chatTopic
	e.chatTopics(false, &ts)
	if len(ts) != 1 || ts[0].ID != topic || ts[0].Title != "Remember to buy milk" || ts[0].Last != "Put it into the inbox." {
		t.Fatalf("the topic is named by the first line of its first message: %+v", ts)
	}
	var ms []chatMessage
	e.waitFor("the reply done", func() bool {
		ms = e.chatMessages(topic)
		return len(ms) == 2 && ms[0].Reply != nil && ms[0].Reply.State == "done"
	})
	if len(ms) != 2 || ms[0].Role != "owner" || ms[0].Reply == nil || ms[0].Reply.State != "done" ||
		!strings.Contains(string(ms[0].Screen), `"screen":"documents"`) {
		t.Fatalf("the owner's message keeps its screen and the state of its reply: %+v", ms)
	}
	req := e.Fake.Calls()[0]
	for _, want := range []string{`command "reply" of deck project "master"`, "The master: takes the owner's tasks",
		"Answer the owner in a chat topic", "Remember to buy milk\nand bread", `(written on project "master", screen "documents", record 7)`,
		fmt.Sprintf("document topics/%d/summary.md of project master", topic)} {
		if !strings.Contains(req.Prompt, want) {
			t.Fatalf("the prompt of the reply lacks %q:\n%s", want, req.Prompt)
		}
	}
	if strings.Contains(req.Prompt, "did not see the earlier part") {
		t.Fatal("a new topic has nothing to retell")
	}
	p := e.full(ms[0].Process)
	if p.Command != "reply" || p.Project != "master" {
		t.Fatalf("the reply is a process of command reply of project master: %+v", p)
	}
	var again struct{ Topic, Message int64 }
	e.must("deck.chat_send", map[string]any{"text": "Remember to buy milk\nand bread", "retry_key": "k1"}, &again)
	if again.Topic != topic || again.Message != ms[0].ID || len(e.Fake.Calls()) != 1 {
		t.Fatalf("a message sent again with its key is the same message: %+v", again)
	}
	if code, _ := e.call("deck.chat_send", map[string]any{"text": "  "}); code != 422 {
		t.Fatalf("an empty message is refused: %d", code)
	}
}

func TestATopicContinuesTheSameSession(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Text: fmt.Sprintf("reply %d", c.N)}
	})
	topic := e.say(0, "first message")
	e.masterSaid(topic, 1)
	e.say(topic, "second message")
	e.masterSaid(topic, 2)
	calls := e.Fake.Calls()
	if len(calls) != 2 || calls[0].Resume || !calls[1].Resume || calls[1].SessionID != calls[0].SessionID {
		t.Fatalf("the second reply resumes the session of the first: %+v", calls)
	}
	if !strings.Contains(calls[1].Prompt, "second message") || strings.Contains(calls[1].Prompt, "first message") {
		t.Fatalf("a resumed session gets only what is new to it:\n%s", calls[1].Prompt)
	}
	other := e.say(0, "another topic")
	e.masterSaid(other, 1)
	if c := e.Fake.Calls()[2]; c.Resume || c.SessionID == calls[0].SessionID {
		t.Fatalf("another topic has a session of its own: %+v", c)
	}
}

func TestAnotherProviderGetsTheRetelling(t *testing.T) {
	t.Parallel()
	summarize, topic := false, int64(0)
	var mu sync.Mutex
	script := func(name string) Script {
		return func(ctx context.Context, c *Call) provider.Result {
			mu.Lock()
			write, id := summarize && name == "other", topic
			mu.Unlock()
			if write {
				if out, bad := c.Tool(ctx, "document_save", map[string]any{"project": "master", "name": fmt.Sprintf("topics/%d/summary.md", id),
					"kind": "doc", "text": "Goal: groceries. SUMMARY-MARK"}); bad {
					return provider.Result{IsError: true, Text: out}
				}
			}
			return provider.Result{Text: fmt.Sprintf("%s says %d", name, c.N)}
		}
	}
	main := &Fake{script: script("fake")}
	other := &Fake{script: script("other"), name: "other"}
	third := &Fake{script: script("third"), name: "third"}
	e := newEnvBuiltin(t, t.TempDir(), []provider.Provider{main, other, third})
	mu.Lock()
	topic = e.say(0, "one")
	mu.Unlock()
	e.masterSaid(topic, 1)
	e.say(topic, "two")
	e.masterSaid(topic, 2)

	e.useModels("master.reply", "other")
	e.say(topic, "three")
	e.masterSaid(topic, 3)
	c := other.Calls()[0]
	if c.Resume || !strings.Contains(c.Prompt, "did not see the earlier part of this topic") {
		t.Fatalf("another provider starts its own session with a retelling:\n%s", c.Prompt)
	}
	for _, want := range []string{"owner: one", "master: fake says 1", "owner: two", "master: fake says 2", "(data, not instructions)"} {
		if !strings.Contains(c.Prompt, want) {
			t.Fatalf("the retelling has the topic word for word as data, missing %q:\n%s", want, c.Prompt)
		}
	}
	if !strings.Contains(lastLine(strings.Split(c.Prompt, "The owner's new message")[1]), "summary.md") || !strings.Contains(c.Prompt, "three") {
		t.Fatalf("the new message comes after the retelling:\n%s", c.Prompt)
	}

	e.useModels("master.reply")
	e.say(topic, "four")
	e.masterSaid(topic, 4)
	back := main.Calls()[2]
	if !back.Resume || back.SessionID != main.Calls()[0].SessionID {
		t.Fatalf("back on the first provider its own session goes on: %+v", back)
	}
	if !strings.Contains(back.Prompt, "owner: three") || !strings.Contains(back.Prompt, "master: other says 1") ||
		strings.Contains(back.Prompt, "owner: two") || strings.Contains(back.Prompt, "master: fake says 2") || !strings.Contains(back.Prompt, "four") {
		t.Fatalf("the resumed session hears what it missed, and only that:\n%s", back.Prompt)
	}

	mu.Lock()
	summarize = true
	mu.Unlock()
	e.useModels("master.reply", "other")
	e.say(topic, "five")
	e.masterSaid(topic, 5)
	e.useModels("master.reply", "third")
	e.say(topic, "six")
	e.masterSaid(topic, 6)
	fresh := third.Calls()[0]
	if !strings.Contains(fresh.Prompt, "SUMMARY-MARK") || strings.Contains(fresh.Prompt, "owner: one") ||
		!strings.Contains(fresh.Prompt, "master: other says 2") || !strings.Contains(fresh.Prompt, "six") {
		t.Fatalf("a retelling is the summary and the messages after it:\n%s", fresh.Prompt)
	}
}

func TestALostSessionStartsAgainWithTheRetelling(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.N == 2 {
			return provider.Result{SessionMissing: true}
		}
		return provider.Result{Text: fmt.Sprintf("reply %d", c.N)}
	})
	topic := e.say(0, "alpha")
	e.masterSaid(topic, 1)
	e.say(topic, "beta")
	e.masterSaid(topic, 2)
	calls := e.Fake.Calls()
	if len(calls) != 3 || !calls[1].Resume || calls[2].Resume {
		t.Fatalf("a resume that finds no session starts it again: %+v", calls)
	}
	if !strings.Contains(calls[2].Prompt, "owner: alpha") || !strings.Contains(calls[2].Prompt, "beta") {
		t.Fatalf("the new session gets the retelling and the new message:\n%s", calls[2].Prompt)
	}
}

func TestMasterActsAsMasterAcrossProjects(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		out, bad := c.Tool(ctx, "records_upsert", map[string]any{"project": "work", "collection": "tasks", "records": `{"title": "Buy milk"}`})
		if bad {
			return provider.Result{Text: "refused: " + out}
		}
		return provider.Result{Text: "Put it into work."}
	})
	e.createProject("work", tasksManifest)
	e.createProject("other", plainManifest)
	topic := e.say(0, "buy milk")
	if said := e.masterSaid(topic, 1); said[0].Text != "Put it into work." {
		t.Fatalf("the master writes a task into another project: %+v", said)
	}
	var tasks []recordRow
	e.must("deck.records_list", map[string]any{"project": "work", "collection": "tasks"}, &tasks)
	if len(tasks) != 1 || tasks[0].Data["title"] != "Buy milk" {
		t.Fatalf("the task is in work: %+v", tasks)
	}
	found := false
	for _, r := range e.journal() {
		if r.Tool == "deck.records_upsert" && r.Actor == "master" && r.Outcome == "done" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the call is the master's in the journal: %+v", e.journal())
	}
	got := e.waitState(e.run(e.projectID("other"), "work"), "done")
	if !strings.Contains(string(got.Result), "refused") || !strings.Contains(string(got.Result), "another project") {
		t.Fatalf("an agent of another project stays inside its project: %+v", got)
	}

	if code, out := e.call("deck.project_delete", map[string]any{"project": "master"}); code == 200 || !strings.Contains(string(out), "built into deck") {
		t.Fatalf("the built-in master cannot be deleted: %d %s", code, out)
	}
	if code, _ := e.call("deck.project_create", map[string]any{"name": "master"}); code == 200 {
		t.Fatal("no project takes the name of the built-in master")
	}
	var m struct {
		Manifest string `json:"manifest"`
		Revision int64  `json:"revision"`
	}
	e.must("deck.manifest_get", map[string]any{"project": "master"}, &m)
	e.must("deck.manifest_save", map[string]any{"project": "master", "manifest": m.Manifest, "reason": "the owner's edit"}, nil)
	e.must("deck.project_pause", map[string]any{"project": "master", "paused": true}, nil)
}

func TestTheOwnersEditOfTheMasterStaysAcrossARestart(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := newEnvWith(t, home, &Fake{script: func(context.Context, *Call) provider.Result { return provider.Result{Text: "ok"} }})
	e.must("deck.document_save", map[string]any{"project": "master", "name": "prompt.md", "kind": "prompt", "text": "# My own master\n"}, nil)
	_ = e.App.Close()
	e2 := newEnvWith(t, home, &Fake{script: func(context.Context, *Call) provider.Result { return provider.Result{Text: "ok"} }})
	var d struct{ Text string }
	e2.must("deck.document_get", map[string]any{"project": "master", "name": "prompt.md"}, &d)
	if d.Text != "# My own master\n" {
		t.Fatalf("the owner's edit of a built-in file stays theirs: %q", d.Text)
	}
	var ps []struct{ Name, Status string }
	e2.must("deck.projects_list", nil, &ps)
	n := 0
	for _, p := range ps {
		if p.Name == "master" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("one built-in master: %+v", ps)
	}
}

func TestTopicsRunInParallelAndRepliesQueue(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	release := map[string]chan struct{}{}
	gate := func(key string) chan struct{} {
		mu.Lock()
		defer mu.Unlock()
		if release[key] == nil {
			release[key] = make(chan struct{})
		}
		return release[key]
	}
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		head, _, _ := strings.Cut(strings.Replace(c.Req.Prompt, "This topic has no summary", "The summary of this topic", 1), "The summary of this topic")
		key := strings.Fields(lastLine(head))[0]
		select {
		case <-gate(key):
		case <-ctx.Done():
		}
		return provider.Result{Text: "done " + key}
	})
	a := e.say(0, "A1 first")
	b := e.say(0, "B1 first")
	e.waitFor("two replies at once", func() bool { return len(e.Fake.Calls()) == 2 })
	e.say(a, "A2 more")
	e.say(a, "A3 more")
	for _, m := range e.chatMessages(a) {
		if (strings.HasPrefix(m.Text, "A2") || strings.HasPrefix(m.Text, "A3")) && m.Process != 0 {
			t.Fatalf("a waiting message has no reply yet: %+v", m)
		}
	}
	close(gate("A1"))
	e.masterSaid(a, 1)
	e.waitFor("the queued messages", func() bool { return len(e.Fake.Calls()) == 3 })
	third := e.Fake.Calls()[2]
	if !strings.Contains(third.Prompt, "2 new messages") || !strings.Contains(third.Prompt, "A2 more") || !strings.Contains(third.Prompt, "A3 more") {
		t.Fatalf("the waiting messages go to one reply together:\n%s", third.Prompt)
	}
	close(gate("A3"))
	close(gate("B1"))
	e.masterSaid(a, 2)
	e.masterSaid(b, 1)

	e.must("deck.settings_set", map[string]any{"name": "agent_limit", "value": "1"}, nil)
	c := e.say(0, "C1 first")
	e.waitFor("the reply of C", func() bool { return len(e.Fake.Calls()) == 4 })
	d := e.say(0, "D1 first")
	var ts []chatTopic
	e.waitFor("D to wait for a slot", func() bool {
		e.chatTopics(false, &ts)
		for _, t := range ts {
			if t.ID == d && t.Reply != nil && t.Reply.State == "waiting_slot" {
				return true
			}
		}
		return false
	})
	close(gate("C1"))
	close(gate("D1"))
	e.masterSaid(c, 1)
	e.masterSaid(d, 1)
}

func TestAMessageReachesAReplyThatParkedForTheOwner(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		switch c.N {
		case 1:
			out, _ := c.Tool(ctx, "ext__work", map[string]any{})
			if !strings.Contains(out, "waits for the owner's confirmation") {
				return provider.Result{Text: "not held: " + out}
			}
			return provider.Result{Text: "Sending it needs your yes."}
		case 2:
			return provider.Result{SessionMissing: true}
		}
		return provider.Result{Text: "Put it on the market list."}
	})
	e.createProject("ext", strings.Replace(plainManifest, `"class": "internal"`, `"class": "external"`, 1))
	topic := e.say(0, "buy apples")
	e.masterSaid(topic, 1)
	e.waitFor("the reply parked", func() bool {
		ms := e.chatMessages(topic)
		return ms[0].Reply != nil && ms[0].Reply.State == "waiting_answer"
	})
	e.say(topic, "the market one")
	said := e.masterSaid(topic, 2)
	if said[0].Text != "Sending it needs your yes." || said[1].Text != "Put it on the market list." {
		t.Fatalf("the reply that parked speaks, then answers the message sent meanwhile: %+v", said)
	}
	calls := e.Fake.Calls()
	if len(calls) != 3 || !calls[1].Resume || calls[2].Resume || !strings.Contains(calls[2].Prompt, "the market one") ||
		!strings.Contains(calls[2].Prompt, "buy apples") {
		t.Fatalf("the message goes into the parked process, and a lost session starts again with it: %d calls\n%s", len(calls), calls[len(calls)-1].Prompt)
	}
	ms := e.chatMessages(topic)
	for _, m := range ms {
		if m.Role == "owner" && m.Process != ms[0].Process {
			t.Fatalf("both messages belong to the one reply process: %+v", ms)
		}
	}
}

func TestMessagesOfAFailedReplyReachTheNextTurn(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.N == 2 {
			return provider.Result{IsError: true, Text: "the model is overloaded"}
		}
		return provider.Result{Text: fmt.Sprintf("reply %d", c.N)}
	})
	topic := e.say(0, "first")
	e.masterSaid(topic, 1)
	e.say(topic, "second, lost")
	e.waitFor("the failed reply", func() bool {
		ms := e.chatMessages(topic)
		return ms[len(ms)-1].Reply != nil && ms[len(ms)-1].Reply.State == "error"
	})
	e.say(topic, "third")
	e.masterSaid(topic, 2)
	c := e.Fake.Calls()[2]
	if !c.Resume || !strings.Contains(c.Prompt, "owner: second, lost") || strings.Contains(c.Prompt, "master: reply 1") || !strings.Contains(c.Prompt, "third") {
		t.Fatalf("what a failed turn did not answer comes again, the session's own reply does not:\n%s", c.Prompt)
	}
}

func TestTheChatBlockShowsTheTopicItsQuestionsAndTheTurn(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.N == 1 {
			return provider.Result{Text: "Hello **there**"}
		}
		c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t1", Title: "records_upsert", Text: "{}"})
		d := c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []any{map[string]any{"question": "Which list?",
			"options": []any{map[string]any{"label": "shop"}, map[string]any{"label": "market"}}}}})
		answers, _ := d["updatedInput"].(map[string]any)["answers"].(map[string]any)
		return provider.Result{Text: fmt.Sprint("Picked ", answers["Which list?"])}
	}, func(c *app.Config) { c.QuestionWait = time.Minute })
	check := newCatalogCheck(t)
	topic := e.say(0, "hello", map[string]any{"screen": `{"project":"master","screen":"documents"}`})
	e.masterSaid(topic, 1)
	pick := func(v string) map[string]any {
		return map[string]any{"filter": fmt.Sprintf(`{"b0": {"topic": %q}}`, v)}
	}
	chat := func(v string) ChatV {
		t.Helper()
		check.messages(t, e.screenMessages("", "chat", pick(v)))
		return blockData[ChatV](t, e.screenData("", "chat", pick(v))["b0"]["value"])
	}
	if c := chat(""); c.Topic == nil || c.Topic.ID != topic || len(c.Messages) != 2 || c.Messages[1].Text != "Hello **there**" ||
		c.Send.Params["topic"] != float64(topic) || len(c.Topics) != 1 {
		t.Fatalf("the newest topic with its messages and the send button: %+v", c)
	}
	if c := chat("new"); c.Topic != nil || len(c.Messages) != 0 || len(c.Topics) != 1 {
		t.Fatalf("a new topic shows no messages: %+v", c)
	}
	e.say(topic, "buy apples")
	var c ChatV
	e.waitFor("the question in the feed", func() bool {
		c = chat(fmt.Sprint(topic))
		return len(c.Questions) == 1 && c.Turn != nil && len(c.Turn.Lines) > 0
	})
	q := c.Questions[0]
	if q.Questions[0].Question != "Which list?" || q.Answer.Command != "deck.ask_answer" || q.Answer.Input != "answer" ||
		c.Turn.Stop.Command != "deck.process_stop" || c.Turn.Lines[0].Title != "records_upsert" || c.Turn.State != "running" {
		t.Fatalf("the question has options and an answer, the turn shows its calls and a stop: %+v", c)
	}
	var open []struct{ Topic string }
	e.must("deck.processes_list", map[string]any{"open": true}, &open)
	if len(open) != 1 || open[0].Topic != c.Topic.Title || open[0].Topic == "" {
		t.Fatalf("an open reply names the topic it answers: %+v", open)
	}
	e.must(q.Answer.Command, map[string]any{"ask": q.Answer.Params["ask"], "answer": `{"Which list?": "market"}`}, nil)
	if said := e.masterSaid(topic, 2); said[1].Text != "Picked market" {
		t.Fatalf("the answer from the feed reaches the turn: %+v", said)
	}
	e.waitFor("the turn to leave the block", func() bool { return chat(fmt.Sprint(topic)).Turn == nil })
	if c := chat(fmt.Sprint(topic)); c.Turn != nil || len(c.Questions) != 0 {
		t.Fatalf("a finished turn leaves the block: %+v", c)
	}
	e.must(c.Archive.Command, c.Archive.Params, nil)
	if c := chat(""); c.Topic != nil || len(c.Archived) != 1 || len(c.Topics) != 0 {
		t.Fatalf("an archived topic is listed apart: %+v", c)
	}
	e.must(c.Delete.Command, c.Delete.Params, nil)
	if c := chat(fmt.Sprint(topic)); c.Topic != nil || len(c.Archived) != 0 {
		t.Fatalf("a deleted topic is gone: %+v", c)
	}
}

type ChatV struct {
	Topic    *chatTopicV  `json:"topic"`
	Topics   []chatTopicV `json:"topics"`
	Archived []chatTopicV `json:"archived"`
	Messages []struct {
		Role, Text string
	} `json:"messages"`
	Questions []struct {
		Ask       int64
		Questions []struct{ Question string } `json:"questions"`
		Answer    chatActionV                 `json:"answer"`
	} `json:"questions"`
	Turn *struct {
		State string                   `json:"state"`
		Lines []struct{ Title string } `json:"lines"`
		Stop  chatActionV              `json:"stop"`
	} `json:"turn"`
	Send    chatActionV `json:"send"`
	Archive chatActionV `json:"archive"`
	Delete  chatActionV `json:"delete"`
}

type chatTopicV struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
}

type chatActionV struct {
	Command string         `json:"command"`
	Params  map[string]any `json:"params"`
	Input   string         `json:"input"`
}

func TestAMessageAnswersTheQuestionOfTheReply(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		d := c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []any{map[string]any{"question": "Which list?"}}})
		answers, _ := d["updatedInput"].(map[string]any)["answers"].(map[string]any)
		return provider.Result{Text: fmt.Sprint("Picked ", answers["Which list?"])}
	}, func(c *app.Config) { c.QuestionWait = time.Minute })
	topic := e.say(0, "buy apples")
	e.waitFor("the question", func() bool {
		_, out := e.call("deck.asks_list", nil)
		return strings.Contains(string(out), "Which list?")
	})
	e.say(topic, "the market one")
	if said := e.masterSaid(topic, 1); said[0].Text != "Picked the market one" {
		t.Fatalf("a message typed while the master asks is the answer: %+v", said)
	}
	out, err := e.App.Registry.Invoke(context.Background(), registry.Actor{Kind: registry.Master, Workspace: 1}, "deck.screen",
		[]byte(`{"screen": "chat"}`))
	if err != nil || !strings.Contains(fmt.Sprint(out), "deck.chat cannot be called by master") {
		t.Fatalf("only the owner reads the chat: %v %v", out, err)
	}
}

// TestAReplyThatDoesNotStartIsSeenInTheTopic: when the master's reply cannot start, the owner's message shows why
// in its topic instead of waiting silently.
func TestAReplyThatDoesNotStartIsSeenInTheTopic(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result { return provider.Result{Text: "Hi."} })
	if _, err := e.App.Store.DB.Exec(`INSERT INTO assignment (workspace_id, project_id, command, role, models)
		SELECT 1, id, 'reply', 'agent', '["gone"]' FROM project WHERE name = 'master'`); err != nil {
		t.Fatal(err)
	}
	topic := e.say(0, "hello")
	e.waitFor("the reply that did not start", func() bool {
		var v struct {
			Messages []struct {
				Role  string
				Reply *struct{ State, Error string }
			}
		}
		e.must("deck.chat", map[string]any{"topic": fmt.Sprint(topic)}, &v)
		return len(v.Messages) == 1 && v.Messages[0].Reply != nil && v.Messages[0].Reply.State == "error" &&
			strings.Contains(v.Messages[0].Reply.Error, "gone")
	})
}

// TestMasterPutsATaskIntoTheRightProject: by the dispatch skill the master picks the project by the first line of
// its md and writes the task into its tasks; a move writes it into the other project and deletes the first, which the
// master's own turn wrote.
func TestMasterPutsATaskIntoTheRightProject(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		list, _ := c.Tool(ctx, "projects_list", map[string]any{})
		var ps []struct{ Name, Description string }
		_ = json.Unmarshal([]byte(list), &ps)
		if strings.Contains(c.Req.Prompt, "move it to garden") {
			c.Tool(ctx, "records_upsert", map[string]any{"project": "garden", "collection": "tasks", "records": `{"title": "Buy a rake"}`})
			if out, bad := c.Tool(ctx, "record_delete", map[string]any{"record": 1}); !bad {
				return provider.Result{Text: "deleted: " + out}
			}
			return provider.Result{Text: "Put it into garden; delete the old one in groceries."}
		}
		for _, p := range ps {
			if strings.Contains(p.Description, "shopping") {
				c.Tool(ctx, "records_upsert", map[string]any{"project": p.Name, "collection": "tasks", "records": `{"title": "Buy a rake"}`})
				return provider.Result{Text: "Put it into " + p.Name + ". Move it?"}
			}
		}
		return provider.Result{Text: "no project"}
	})
	e.must("deck.project_create", map[string]any{"name": "groceries", "prompt": "# The shopping list of the household.\n", "manifest": tasksManifest}, nil)
	e.must("deck.project_create", map[string]any{"name": "garden", "prompt": "# Work in the garden.\n", "manifest": tasksManifest}, nil)
	tasks := func(project string) int {
		var rs []recordRow
		e.must("deck.records_list", map[string]any{"project": project, "collection": "tasks"}, &rs)
		return len(rs)
	}
	topic := e.say(0, "buy a rake")
	if said := e.masterSaid(topic, 1); said[0].Text != "Put it into groceries. Move it?" || tasks("groceries") != 1 {
		t.Fatalf("the master picks the project by the first line of its md: %+v", said)
	}
	if _, ok := e.Fake.Calls()[0].Skills["dispatch/SKILL.md"]; !ok {
		t.Fatalf("the master gets the dispatch skill: %v", e.Fake.Calls()[0].Skills)
	}
	e.say(topic, "no, move it to garden")
	if said := e.masterSaid(topic, 2); !strings.HasPrefix(said[1].Text, "deleted: ") || tasks("garden") != 1 || tasks("groceries") != 0 {
		t.Fatalf("move puts the task into the other project and deletes the old one the master wrote: %+v", said)
	}
}

// TestAReplyTheProviderBrokeOffListsWhatItLeftAndGoesAgainElsewhere: a reply that broke off shows the calls it made,
// the records it left with their undo and the other providers of the master; one of them answers that reply only,
// told what the broken one did.
func TestAReplyTheProviderBrokeOffListsWhatItLeftAndGoesAgainElsewhere(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var told string
	var turns atomic.Int32
	main := &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		if turns.Add(1) == 1 {
			c.Tool(ctx, "records_upsert", map[string]any{"project": "notes", "collection": "ideas", "records": `{"title": "half a ta", "score": 1}`})
			return provider.Result{IsError: true, Text: "the turn was stopped mid-way"}
		}
		return provider.Result{Text: "main again"}
	}}
	other := &Fake{name: "other", script: func(ctx context.Context, c *Call) provider.Result {
		mu.Lock()
		told = c.Req.Prompt
		mu.Unlock()
		return provider.Result{Text: "Done on other."}
	}}
	e := newEnvBuiltin(t, t.TempDir(), []provider.Provider{main, other})
	e.createProject("notes", ideasManifest)
	e.must("deck.assignment_set", map[string]any{"role": "agent", "project": "master", "command": "reply", "models": []string{"fake", "other"}}, nil)
	topic := e.say(0, "write half a task")
	var m chatMessage
	e.waitFor("the reply to break off", func() bool {
		ms := e.chatMessages(topic)
		m = ms[0]
		return m.Reply != nil && m.Reply.Cut != nil
	})
	cut := m.Reply.Cut
	if len(cut.Did) != 1 || !strings.Contains(cut.Did[0], "records_upsert") || len(cut.Undo) != 1 || cut.Undo[0].Action.Command != "deck.record_delete" ||
		len(cut.Retry) != 1 || cut.Retry[0].Label != "other" {
		t.Fatalf("a broken reply lists its calls, the record it left and the other provider: %+v", cut)
	}
	e.must(cut.Undo[0].Action.Command, cut.Undo[0].Action.Params, nil)
	var recs []recordRow
	if e.must("deck.records_list", map[string]any{"project": "notes", "collection": "ideas"}, &recs); len(recs) != 0 {
		t.Fatalf("the undo deletes the half-written record: %+v", recs)
	}
	e.must(cut.Retry[0].Command, cut.Retry[0].Params, nil)
	if said := e.masterSaid(topic, 1); said[0].Text != "Done on other." {
		t.Fatalf("the other provider answers the messages again: %+v", said)
	}
	mu.Lock()
	if !strings.Contains(told, "broke off") || !strings.Contains(told, "records_upsert") || !strings.Contains(told, "write half a task") {
		t.Fatalf("the other provider is told what the broken reply did: %s", told)
	}
	mu.Unlock()
	e.say(topic, "and one more")
	said := e.masterSaid(topic, 2)
	if said[1].Text != "main again" {
		t.Fatalf("the next reply goes back to the assignment: %+v", said)
	}
	if code, _ := e.call("deck.chat_retry", map[string]any{"message": m.ID, "provider": "other"}); code == 200 {
		t.Fatal("a reply answered again is no broken reply to retry")
	}
}
