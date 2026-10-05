package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/internal/registry"
)

func TestOwnerMessageGetsAMasterReplyInTheTopic(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Text: "Put it into the inbox."}
	})
	topic := e.say(0, "Remember to buy milk\nand bread", map[string]any{"screen": `{"project":"master","screen":"documents","card":7}`,
		"retry_key": "k1"})
	said := e.masterSaid(topic, 1)
	if said[0].Text != "Put it into the inbox." {
		t.Fatalf("the final text of the turn is the master's reply: %+v", said)
	}
	var ts []chatTopic
	e.must("deck.chat_topics", nil, &ts)
	if len(ts) != 1 || ts[0].ID != topic || ts[0].Title != "Remember to buy milk" || ts[0].Last != "Put it into the inbox." {
		t.Fatalf("the topic is named by the first line of its first message: %+v", ts)
	}
	ms := e.chatMessages(topic)
	if len(ms) != 2 || ms[0].Role != "owner" || ms[0].Reply == nil || ms[0].Reply.State != "done" ||
		!strings.Contains(string(ms[0].Screen), `"screen":"documents"`) {
		t.Fatalf("the owner's message keeps its screen and the state of its reply: %+v", ms)
	}
	req := e.Fake.Calls()[0]
	for _, want := range []string{`command "reply" of deck project "master"`, "The master: takes the owner's tasks",
		"Answer the owner in a chat topic", "Remember to buy milk\nand bread", `(written on project "master", screen "documents", card 7)`,
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
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		out, bad := c.Tool(ctx, "card_create", map[string]any{"project": "work", "title": "Buy milk"})
		if bad {
			return provider.Result{Text: "refused: " + out}
		}
		return provider.Result{Text: "Put it into work."}
	})
	e.must("deck.project_create", map[string]any{"name": "work"}, nil)
	e.createProject("other", plainManifest)
	topic := e.say(0, "buy milk")
	if said := e.masterSaid(topic, 1); said[0].Text != "Put it into work." {
		t.Fatalf("the master creates a card in another project: %+v", said)
	}
	var board struct {
		Lanes []struct {
			Cards []struct{ Title string } `json:"cards"`
		} `json:"lanes"`
	}
	e.must("deck.cards_list", map[string]any{"project": "work"}, &board)
	if len(board.Lanes) == 0 || len(board.Lanes[0].Cards) != 1 || board.Lanes[0].Cards[0].Title != "Buy milk" {
		t.Fatalf("the card is in the inbox lane of work: %+v", board)
	}
	found := false
	for _, r := range e.journal() {
		if r.Tool == "deck.card_create" && r.Actor == "master" && r.Outcome == "done" {
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
	time.Sleep(100 * time.Millisecond)
	if n := len(e.Fake.Calls()); n != 2 {
		t.Fatalf("messages of a topic whose reply runs wait for it: %d calls", n)
	}
	for _, m := range e.chatMessages(a) {
		if strings.HasPrefix(m.Text, "A2") && m.Process != 0 {
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
		e.must("deck.chat_topics", nil, &ts)
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
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.N == 1 {
			return provider.Result{Text: "Hello **there**"}
		}
		c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t1", Title: "card_create", Text: "{}"})
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
		c.Turn.Stop.Command != "deck.process_stop" || c.Turn.Lines[0].Title != "card_create" || c.Turn.State != "running" {
		t.Fatalf("the question has options and an answer, the turn shows its calls and a stop: %+v", c)
	}
	e.must(q.Answer.Command, map[string]any{"ask": q.Answer.Params["ask"], "answer": `{"Which list?": "market"}`}, nil)
	if said := e.masterSaid(topic, 2); said[1].Text != "Picked market" {
		t.Fatalf("the answer from the feed reaches the turn: %+v", said)
	}
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

// TestTheChatReadAndTheTopicsAreOneModel: the chat a screen shows is a read like any other, and its topics are
// the ones chat_topics lists, field for field.
func TestTheChatReadAndTheTopicsAreOneModel(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result { return provider.Result{Text: "Noted."} })
	e.createProject("web", boardWebManifest)
	topic := e.say(0, "look at this", map[string]any{"screen": `{"project":"web","screen":"main"}`})
	e.masterSaid(topic, 1)
	var view struct {
		Topics []json.RawMessage `json:"topics"`
	}
	e.must("deck.chat", nil, &view)
	var listed []json.RawMessage
	e.must("deck.chat_topics", nil, &listed)
	if len(view.Topics) != 1 || len(listed) != 1 || string(view.Topics[0]) != string(listed[0]) {
		t.Fatalf("one topic, one shape:\n%s\n%s", view.Topics, listed)
	}
	if !strings.Contains(string(listed[0]), `"last":"Noted."`) || !strings.Contains(string(listed[0]), `"project":"web"`) {
		t.Fatalf("the topic has its last message and module: %s", listed[0])
	}
}

// TestAReplyThatDoesNotStartIsSeenInTheTopic: when the master's reply cannot start, the owner's message shows why
// in its topic instead of waiting silently.
func TestAReplyThatDoesNotStartIsSeenInTheTopic(t *testing.T) {
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
