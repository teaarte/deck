package scenarios

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
)

// TestDecksOwnSentencesSpeakTheLanguageOfTheScreen: what deck writes itself — the failure of a process, a row of
// health, a note, a phrase inside another — reads in the owner's language; the words of a model API stay as they came.
func TestDecksOwnSentencesSpeakTheLanguageOfTheScreen(t *testing.T) {
	t.Parallel()
	e, _, _ := twoModels(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Outcome: provider.Unavailable, Text: "API Error: overloaded"}
	}, ok)
	id := e.startWork(1)
	e.waitState(e.following(id), "done")
	var run struct{ Error string }
	e.must("deck.process_get", map[string]any{"process": id, "lang": "ru"}, &run)
	if run.Error != "fake не ответила: API Error: overloaded" {
		t.Fatalf("a failure deck wrote reads in Russian, the words of the API as they came: %q", run.Error)
	}
	e.waitFor("a note of deck in Russian, without numbers of processes", func() bool {
		var notes []struct{ Text string }
		e.must("deck.feed_list", map[string]any{"lang": "ru", "kind": "note"}, &notes)
		return slices.ContainsFunc(notes, func(n struct{ Text string }) bool {
			return strings.HasPrefix(n.Text, "fake не закончила «work», продолжает backup: fake не ответила: API Error")
		})
	})
}

// TestADecisionSaysWhoAsksWhyAndWhatChanges: a held call says who asks, why in the agent's last words and what it
// changes; the raw call is only in its details, and a call allowed only once has no button to allow it always.
func TestADecisionSaysWhoAsksWhyAndWhatChanges(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		c.Req.Emit(provider.Event{Kind: provider.EventMessage, Text: "groq answers 413 on this work, so nemotron should take it"})
		c.Tool(ctx, "assignment_set", map[string]any{"role": "agent", "project": "plain", "command": "work", "models": []string{"quiet"}})
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	p := e.createProject("plain", testManifest([]string{agentCommand("work", "")}, nil))
	e.run(p, "work")
	e.waitAsk()
	var asks []struct {
		Who, What, Where, Why, By, Always string
		Payload                           map[string]any
	}
	e.waitFor("the decision", func() bool {
		e.must("deck.asks_list", map[string]any{"lang": "ru"}, &asks)
		return len(asks) == 1 && asks[0].Why != ""
	})
	a := asks[0]
	if a.Who != "work" || a.Why != "groq answers 413 on this work, so nemotron should take it" || !strings.HasPrefix(a.Where, "agent для plain.work: ") ||
		!strings.HasSuffix(a.Where, "→ quiet") || !strings.HasPrefix(a.By, "#") {
		t.Fatalf("who asks in words, why in its own words, was → becomes in Russian, the process apart: %+v", a)
	}
	if a.Always != "" || a.Payload["role"] != "agent" || a.Payload["caller"] != nil || a.Payload["workspace"] != nil {
		t.Fatalf("a change allowed once offers no always, and the details hold only the call: %+v", a)
	}
	for _, it := range e.cockpitItems() {
		for _, act := range it.Actions {
			if act["params"].(map[string]any)["answer"] == "allow_always" {
				t.Fatalf("no button to allow always a change allowed once: %+v", it.Actions)
			}
		}
	}
}

// TestNotesOfOneCauseOfADayAreOneNote: notes of modules naming one cause, and deck's own about the same model, are
// one unread note with each module's part; another cause or another day is a note of its own.
func TestNotesOfOneCauseOfADayAreOneNote(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, nil)
	for _, name := range []string{"shorts", "btc"} {
		e.createProject(name, testManifest([]string{agentCommand("work", "")}, nil))
	}
	e.must("deck.notify", map[string]any{"project": "shorts", "text": "Groq does not answer; move shorts to nemotron", "cause": "provider:groq"}, nil)
	e.must("deck.notify", map[string]any{"project": "btc", "text": "Groq refuses with 413; move btc to local-qwen", "cause": "provider:groq"}, nil)
	e.must("deck.notify", map[string]any{"project": "btc", "text": "The forecast is ready"}, nil)
	var notes []struct {
		Text     string
		Cause    string
		Projects []string
		Parts    []struct{ Project, Text string }
	}
	e.must("deck.feed_list", map[string]any{"kind": "note", "unread": true}, &notes)
	if len(notes) != 2 {
		t.Fatalf("two notes of one cause are one: %+v", notes)
	}
	i := slices.IndexFunc(notes, func(n struct {
		Text     string
		Cause    string
		Projects []string
		Parts    []struct{ Project, Text string }
	}) bool {
		return n.Cause == "provider:groq"
	})
	if i < 0 || !slices.Equal(notes[i].Projects, []string{"shorts", "btc"}) || len(notes[i].Parts) != 2 || !strings.Contains(notes[i].Parts[1].Text, "local-qwen") {
		t.Fatalf("the note of the cause holds each module's part: %+v", notes)
	}
	e.must("deck.notify", map[string]any{"project": "btc", "text": "Groq refuses with 413; move btc to local-qwen", "cause": "provider:groq"}, nil)
	e.must("deck.feed_list", map[string]any{"kind": "note", "unread": true}, &notes)
	if i := slices.IndexFunc(notes, func(n struct {
		Text     string
		Cause    string
		Projects []string
		Parts    []struct{ Project, Text string }
	}) bool {
		return n.Cause == "provider:groq"
	}); len(notes) != 2 || len(notes[i].Parts) != 2 {
		t.Fatalf("the same note again adds no part: %+v", notes)
	}
	e.Clock.Add(24 * time.Hour)
	e.must("deck.notify", map[string]any{"project": "shorts", "text": "Groq does not answer again", "cause": "provider:groq"}, nil)
	e.must("deck.feed_list", map[string]any{"kind": "note", "unread": true}, &notes)
	if len(notes) != 3 {
		t.Fatalf("a note of the cause on another day is its own: %+v", notes)
	}
}
