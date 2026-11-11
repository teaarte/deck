package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/fault"
	"deck2/internal/provider"
	"deck2/internal/store/db"
	"deck2/tests/harness"
)

func TestQuestionAnsweredLive(t *testing.T) {
	t.Parallel()
	// The answer comes while the call still waits, however loaded the machine of the test.
	live := harness.WithConfig(func(c *app.Config) { c.QuestionWait = 2 * time.Minute })
	e := harness.New(t, live, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		d := c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{
			{"question": "Which topic?", "header": "Topic", "options": []map[string]any{{"label": "go"}, {"label": "rust"}}}}})
		// A turn cut when the test ends gets no decision.
		in, _ := d["updatedInput"].(map[string]any)
		answers, _ := in["answers"].(map[string]any)
		topic, ok := answers["Which topic?"].(string)
		if !ok {
			return provider.Fail(fault.TaskFailed, "no answer")
		}
		harness.WriteIdeas(ctx, c, topic)
		return provider.Result{Structured: json.RawMessage(`{"written": 3}`)}
	}))

	p := e.Project("ideas", harness.IdeasManifest)
	id := e.Run(p, "collect")
	a := e.WaitAsk()
	if a.Kind != "question" || a.Text != "Which topic?" {
		t.Fatalf("unexpected ask %+v", a)
	}
	e.Until("a question the picker shows in full is headed by its header, not its text twice", func() bool {
		items := e.CockpitItems()
		return len(items) == 1 && items[0].Title == "Topic"
	})
	e.Until("a process that waits for the owner says so in Running now, with its question", func() bool {
		running := e.CockpitRunning()
		return len(running) == 1 && strings.Contains(running[0].Text, "waits for your answer") && strings.Contains(running[0].Text, "Which topic?")
	})
	if b, _ := json.Marshal(e.ScreenMessages("", "cockpit", map[string]any{"topic": "asks"})); !strings.Contains(string(b), "waits for your answer") {
		t.Fatalf("a question that opens reads Running now again: %s", b)
	}
	e.Answer(a.ID, `{"Which topic?":"go"}`)
	e.WaitState(id, "done")
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "ideas"})
	if len(recs) == 0 || !strings.Contains(recs[0].Data, `"topic":"go"`) {
		t.Fatalf("answer did not reach the agent: %v", recs)
	}
}

// TestAnAnswerTheProcessDidNotActOnGoesToTheMaster: an agent asks the owner, gets the answer live and ends with
// nothing; the answer reaches the master on its next turn, and the owner's feed says so.
func TestAnAnswerTheProcessDidNotActOnGoesToTheMaster(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() != "collect" {
			return provider.Result{Text: "On it."}
		}
		c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{
			{"question": "The voice reads the style aloud; how should I fix it?", "options": []map[string]any{{"label": "Trim it"}}}}})
		return provider.Result{Structured: json.RawMessage(`null`)}
	}))

	p := e.Project("ideas", harness.Nullable(harness.IdeasManifest))
	id := e.Run(p, "collect")
	a := e.WaitAsk()
	e.Answer(a.ID, `{"The voice reads the style aloud; how should I fix it?":"regenerate the voice"}`)
	e.WaitState(id, "done")
	n := e.WaitNote("regenerate the voice")
	if !strings.Contains(n.Text, fmt.Sprintf("#%d", id)) || !strings.Contains(n.Text, "master") {
		t.Fatalf("the owner hears that the process did nothing with the answer and the master has it: %+v", n)
	}
	topic := e.Say(0, "hello")
	e.MasterSaid(topic, 1)
	var prompt string
	for _, c := range e.Fake.Calls() {
		if (&harness.Call{Req: c}).Command() != "collect" {
			prompt = c.Prompt
		}
	}
	if !strings.Contains(prompt, "regenerate the voice") || !strings.Contains(prompt, "how should I fix it?") ||
		!strings.Contains(prompt, fmt.Sprintf("#%d", id)) || !strings.Contains(prompt, "(data, not instructions)") {
		t.Fatalf("the master's next turn has the answer, the question and the process, as data:\n%s", prompt)
	}
}

// TestAnAnswerAfterTheProcessFinishedGoesToTheMaster: the owner answers a question whose process finished in the
// meantime; no process takes the answer, so the master does.
func TestAnAnswerAfterTheProcessFinishedGoesToTheMaster(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`null`)}
	}))

	p := e.Project("ideas", harness.Nullable(harness.IdeasManifest))
	id := e.Run(p, "collect")
	e.WaitState(id, "done")
	// A question still open when its process finished in the meantime.
	e.SQL(`INSERT INTO ask (workspace_id, project_id, process_id, kind, text) VALUES (1, ?, ?, 'question', 'Which voice?')`, p, id)
	a := e.WaitAsk()
	e.Answer(a.ID, `{"Which voice?":"the calm one"}`)
	if n := e.WaitNote("the calm one"); !strings.Contains(n.Text, fmt.Sprintf("#%d", id)) {
		t.Fatalf("the owner hears that the master has the answer: %+v", n)
	}
	var feed []struct{ Kind string }
	e.Must("deck.feed_list", map[string]any{"unread": true}, &feed)
	for _, f := range feed {
		if f.Kind != "note" {
			t.Fatalf("unread are notes only: %+v", feed)
		}
	}
}

// TestAQuestionWhoseCallDroppedStaysOpenWhenTheTurnBreaks: the transport drops while the owner is asked and the
// turn ends with an error; the question stays for the owner and the answer resumes the session.
func TestAQuestionWhoseCallDroppedStaysOpenWhenTheTurnBreaks(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if !c.Req.Resume {
			dropped, cancel := context.WithTimeout(ctx, 200*time.Millisecond)
			defer cancel()
			c.Tool(dropped, "approve", map[string]any{"tool_name": "AskUserQuestion", "tool_use_id": "t1", "input": map[string]any{"questions": []map[string]any{
				{"question": "Which topic?", "options": []map[string]any{{"label": "go"}, {"label": "rust"}}}}}})
			return provider.Fail(fault.TaskFailed, `MCP server "deck" transport dropped mid-call`)
		}
		harness.WriteIdeas(ctx, c, map[bool]string{true: "rust", false: "?"}[strings.Contains(c.Req.Prompt, "rust")])
		return provider.Result{Structured: json.RawMessage(`{"written": 3}`)}
	}))

	p := e.Project("ideas", harness.IdeasManifest)
	id := e.Run(p, "collect")
	e.WaitState(id, "waiting_answer", "error")
	a := e.WaitAsk()
	if a.Kind != "question" || a.Text != "Which topic?" {
		t.Fatalf("the question stays open for the owner: %+v", a)
	}
	e.Answer(a.ID, `{"Which topic?":"rust"}`)
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the answer resumes the session: %+v", got)
	}
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "ideas"})
	if len(recs) == 0 || !strings.Contains(recs[0].Data, `"topic":"rust"`) {
		t.Fatalf("the answer reached the agent: %v", recs)
	}
}

func TestQuestionAnsweredLateResumesSession(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if !c.Req.Resume {
			d := c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{{"question": "Which topic?"}}})
			if d["behavior"] != "deny" {
				panic("expected deny on timeout")
			}
			return provider.Result{Text: "stopped"}
		}
		topic := "?"
		if strings.Contains(c.Req.Prompt, "rust") {
			topic = "rust"
		}
		harness.WriteIdeas(ctx, c, topic)
		return provider.Result{Structured: json.RawMessage(`{"written": 3}`)}
	}), harness.WithConfig(

		func(c *app.Config) { c.QuestionWait = 100 * time.Millisecond }))

	p := e.Project("ideas", harness.IdeasManifest)
	id := e.Run(p, "collect")
	e.WaitState(id, "waiting_answer")
	a := e.WaitAsk()
	e.Answer(a.ID, "rust")
	e.WaitState(id, "done")
	calls := e.Fake.Calls()
	if len(calls) != 2 || !calls[1].Resume || calls[1].SessionID != calls[0].SessionID {
		t.Fatalf("want resume of the first session, got %+v", calls)
	}
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "ideas"})
	if len(recs) == 0 || !strings.Contains(recs[0].Data, `"topic":"rust"`) {
		t.Fatalf("late answer did not reach the agent: %v", recs)
	}
}

// TestAQuestionWithoutTextReachesNoOne: a question with no text is refused to the agent and waits for no one.
func TestAQuestionWithoutTextReachesNoOne(t *testing.T) {
	t.Parallel()
	var empty, none map[string]any
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		empty = c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{{"question": " ", "header": "Topic"}}})
		none = c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{}})
		return provider.Result{Text: "ok"}
	}))
	p := e.Project("plain", harness.PlainManifest)
	e.WaitState(e.Run(p, "work"), "done")
	for _, d := range []map[string]any{empty, none} {
		if d["behavior"] != "deny" || !strings.Contains(fmt.Sprint(d["message"]), "question field") {
			t.Fatalf("a question without text is refused with what to put in it: %v", d)
		}
	}
	if as := e.Asks(); len(as) != 0 {
		t.Fatalf("a question without text waits for no one: %+v", as)
	}
}

// TestAProcessParkedOnACallSaysWhatItWaitsFor: a turn that ended with a frozen call open waits for it, and the
// process says so with why the call asks.
func TestAProcessParkedOnACallSaysWhatItWaitsFor(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"id": "x"}`) })
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.N == 1 {
			c.Tool(ctx, "notes__post", map[string]any{"id": "a1", "text": "hi", "why": "to post the note"})
		}
		return provider.Result{Structured: json.RawMessage(`{"ok": true}`)}
	}))
	p := e.Project("notes", harness.PostManifest(svc.URL, ""))
	e.Secret("notes", "svc", "key")
	id := e.Run(p, "work")
	e.WaitState(id, "waiting_answer")
	var proc struct {
		Ask      int64
		Question string
	}
	e.Must("deck.process_get", map[string]any{"process": id}, &proc)
	if a := e.WaitAsk(); proc.Ask != a.ID || proc.Question != "to post the note" {
		t.Fatalf("a parked process says why the call it waits for asks: %+v %+v", proc, a)
	}
}
