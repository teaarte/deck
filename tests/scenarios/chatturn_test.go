package scenarios

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"deck2/internal/provider"
)

// chatD5 is what the chat block gives beside the topic: suggestions, decisions of the turn and how replies ended.
type chatD5 struct {
	Suggest []struct{ Icon, Text string } `json:"suggest"`
	Asks    []struct {
		ID      int64
		Kind    string
		What    string
		Actions []struct {
			Verb, Label, Command, Until string
			Primary, Hold               bool
			Params                      map[string]any
		} `json:"actions"`
	} `json:"asks"`
	Messages []struct {
		ID    int64
		Role  string
		Reply *struct {
			Cut *struct {
				Stopped bool
				Reason  string
				Steps   int
				Said    string
				Retry   []struct {
					Verb, Command string
					Params        map[string]any
				}
			}
		}
	} `json:"messages"`
}

func (e *Env) chatBlock(filter string) chatD5 {
	e.T.Helper()
	return blockData[chatD5](e.T, chatData(e.T, e.screenData("", "chat", map[string]any{"filter": filter})))
}

// TestAnEmptyTopicSuggestsWhatTheModuleOfTheScreenDoes: a topic without messages suggests the commands of the module
// of the owner's screen that take no input, its agents first; none on a screen of deck or once the topic has messages.
func TestAnEmptyTopicSuggestsWhatTheModuleOfTheScreenDoes(t *testing.T) {
	t.Parallel()
	e := New(t, WithAgent(func(ctx context.Context, c *Call) provider.Result { return provider.Result{Text: "ok"} }))
	e.Project("jobs", scriptManifest("run", "", ""))
	c := e.chatBlock(`{"b0": {"topic": "new", "project": "jobs"}}`)
	if len(c.Suggest) != 2 || c.Suggest[0].Text != "Publish" || c.Suggest[0].Icon != "sparkles" || c.Suggest[1].Text != "Run" || c.Suggest[1].Icon != "player-play" {
		t.Fatalf("the commands of the module, its agents first: %+v", c.Suggest)
	}
	if c := e.chatBlock(`{"b0": {"topic": "new"}}`); len(c.Suggest) != 0 {
		t.Fatalf("a screen of deck suggests nothing: %+v", c.Suggest)
	}
	topic := e.say(0, "hello")
	e.masterSaid(topic, 1)
	if c := e.chatBlock(fmt.Sprintf(`{"b0": {"topic": "%d", "project": "jobs"}}`, topic)); len(c.Suggest) != 0 {
		t.Fatalf("a topic with messages suggests nothing: %+v", c.Suggest)
	}
}

// TestTheTopicShowsWhatItsTurnWaitsToBeAllowed: a call of the turn that waits for the owner is a card of the topic
// with a yes that names it and a no, both held a few seconds; the yes lets the turn go on.
func TestTheTopicShowsWhatItsTurnWaitsToBeAllowed(t *testing.T) {
	t.Parallel()
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{"id": "x"}`) })
	e := New(t, WithAgent(func(ctx context.Context, c *Call) provider.Result {
		if c.N == 1 {
			out, _ := c.Tool(ctx, "notes__post", map[string]any{"id": "a", "text": "hi"})
			return provider.Result{Text: "asked: " + out}
		}
		return provider.Result{Text: "posted"}
	}), WithConfig(

		withTTL))

	e.Must("deck.secret_set", map[string]any{"name": "svc", "value": "k"}, nil)
	e.Project("notes", postManifest(svc.URL, ""))
	topic := e.say(0, "post a note")
	filter := fmt.Sprintf(`{"b0": {"topic": "%d"}}`, topic)
	var c chatD5
	e.Until("the call to wait in the topic", func() bool { c = e.chatBlock(filter); return len(c.Asks) == 1 })
	a := c.Asks[0]
	if a.Kind != "call" || len(a.Actions) < 2 {
		t.Fatalf("the waiting call with its buttons: %+v", a)
	}
	yes, no := a.Actions[0], a.Actions[1]
	if yes.Verb != "allow" || !yes.Primary || !yes.Hold || !strings.HasPrefix(yes.Label, "Allow: ") || yes.Until == "" || yes.Command != "deck.ask_answer" ||
		no.Verb != "deny" || no.Primary || !no.Hold {
		t.Fatalf("a yes that names the call and a no, both held: %+v", a.Actions)
	}
	e.Must(yes.Command, yes.Params, nil)
	e.Until("the call made", func() bool { return len(svc.requests()) == 1 })
	e.Until("the card gone", func() bool { return len(e.chatBlock(filter).Asks) == 0 })
}

// TestAStoppedReplySaysHowFarItGotAndGoesOnOnItsOwnModel: a reply the owner stopped says so with its steps and last
// words, and goes on on its own model; a broken one names why in words.
func TestAStoppedReplySaysHowFarItGotAndGoesOnOnItsOwnModel(t *testing.T) {
	t.Parallel()
	var turns atomic.Int32
	stepped := make(chan struct{})
	e := New(t, WithAgent(func(ctx context.Context, c *Call) provider.Result {
		switch turns.Add(1) {
		case 1:
			c.Req.OnEvent(provider.Event{Kind: provider.EventMessage, Text: "Reading the notes"})
			c.Req.OnEvent(provider.Event{Kind: provider.EventTool, ID: "t1", Title: "projects_list", Text: "{}"})
			close(stepped)
			<-ctx.Done()
			return provider.Result{Outcome: provider.Failed, Text: "stopped"}
		case 2:
			if !strings.Contains(c.Req.Prompt, "This continues process") {
				return provider.Result{Text: "not told"}
			}
			return provider.Result{Text: "went on"}
		}
		return provider.Result{Outcome: provider.Failed, Text: "it broke"}
	}))

	topic := e.say(0, "read the notes")
	filter := fmt.Sprintf(`{"b0": {"topic": "%d"}}`, topic)
	var open []struct{ ID int64 }
	e.Until("the reply to run", func() bool {
		e.Must("deck.processes_list", map[string]any{"open": true}, &open)
		return len(open) == 1 && turns.Load() == 1
	})
	<-stepped
	e.Must("deck.process_stop", map[string]any{"process": open[0].ID}, nil)
	var c chatD5
	e.Until("the stopped reply", func() bool {
		c = e.chatBlock(filter)
		return len(c.Messages) > 0 && c.Messages[0].Reply != nil && c.Messages[0].Reply.Cut != nil
	})
	cut := c.Messages[0].Reply.Cut
	if !cut.Stopped || cut.Steps != 1 || cut.Said != "Reading the notes" || len(cut.Retry) != 1 || cut.Retry[0].Verb != "continue" || cut.Retry[0].Params["provider"] != "fake" {
		t.Fatalf("a stopped reply with its steps, last words and one way on: %+v", cut)
	}
	if code, _ := e.Call("deck.chat_retry", map[string]any{"message": c.Messages[0].ID, "provider": "other"}); code == 200 {
		t.Fatal("a stopped reply goes on on its own model only")
	}
	e.Must(cut.Retry[0].Command, cut.Retry[0].Params, nil)
	if said := e.masterSaid(topic, 1); said[0].Text != "went on" {
		t.Fatalf("the reply goes on, told what the stopped one did: %+v", said)
	}
	e.say(topic, "and again")
	e.Until("the broken reply", func() bool {
		c = e.chatBlock(filter)
		m := c.Messages[len(c.Messages)-1]
		if m.Reply != nil && m.Reply.Cut != nil {
			cut = m.Reply.Cut
			return true
		}
		return false
	})
	if cut.Stopped || cut.Reason != "the agent reported an error" || len(cut.Retry) != 1 || cut.Retry[0].Verb != "retry" {
		t.Fatalf("a broken reply names why in words and the other model: %+v", cut)
	}
}
