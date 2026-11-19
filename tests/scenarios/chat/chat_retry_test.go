package chat

import (
	"context"
	"sync/atomic"
	"testing"

	"deck2/internal/fault"
	"deck2/internal/provider"
	"deck2/tests/harness"
)

// retried breaks the first reply of a topic and has deck stop as the owner answers it again on other: the choice is
// only in the base when deck starts again.
func retried(t *testing.T) (*harness.Env, int64, int64) {
	t.Helper()
	var replies atomic.Int32
	main := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		if replies.Add(1) == 1 {
			return provider.Fail(fault.TaskFailed, "it broke")
		}
		return provider.Result{Text: "main"}
	}}
	other := &harness.Fake{ProviderName: "other", Script: func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Text: "other"}
	}}
	e := harness.New(t, harness.WithClock(), harness.WithProvider(main, other))
	e.Must("deck.assignment_set", map[string]any{"role": "agent", "project": "master", "command": "reply", "models": []string{"fake", "other"}}, nil)
	topic := e.Say(0, "write half a task")
	var m harness.ChatMessage
	e.Until("the reply to break off", func() bool {
		m = e.ChatMessages(topic)[0]
		return m.Reply != nil && m.Reply.Cut != nil && len(m.Reply.Cut.Retry) == 1
	})
	e.App.Stop()
	e.Must(m.Reply.Cut.Retry[0].Command, m.Reply.Cut.Retry[0].Params, nil)
	return e, topic, m.ID
}

// TestARetryOnAProviderSinceGoneGoesOnTheTopicsOwn: a reply the owner answered again on a provider that is gone by the
// time it starts does not start on it for ever: the next try goes on the topic's own provider.
func TestARetryOnAProviderSinceGoneGoesOnTheTopicsOwn(t *testing.T) {
	t.Parallel()
	e, topic, _ := retried(t)
	e = e.Restart(harness.WithProvider(&harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Text: "main"}
	}}))
	e.Until("the reply on the topic's own provider", func() bool {
		e.Clock.Add(e.App.Cfg.QuestionWait)
		e.Tick()
		for _, m := range e.ChatMessages(topic) {
			if m.Role == "master" && m.Text == "main" {
				return true
			}
		}
		return false
	})
	var follows int64
	// The process a reply follows, which deck shows nowhere.
	if err := e.SQLRow(`SELECT COALESCE(MAX(follows_id), 0) FROM process WHERE provider = 'fake' AND state = 'done'`).Scan(&follows); err != nil || follows != 0 {
		t.Fatalf("the reply on the topic's own follows nothing: %d %v", follows, err)
	}
}

// TestADeletedMessageTakesTheChoiceOfItsRetryAlong: the owner deletes the message a retry was for; the next message
// goes on the topic's own provider and follows nothing.
func TestADeletedMessageTakesTheChoiceOfItsRetryAlong(t *testing.T) {
	t.Parallel()
	e, topic, message := retried(t)
	e.Must("deck.chat_message_delete", map[string]any{"message": message}, nil)
	e = e.Restart()
	e.Say(topic, "something else")
	if said := e.MasterSaid(topic, 1); said[0].Text != "main" {
		t.Fatalf("the next message goes on the topic's own provider: %+v", said)
	}
	var follows int64
	// The process a reply follows, which deck shows nowhere.
	if err := e.SQLRow(`SELECT COALESCE(MAX(follows_id), 0) FROM process WHERE provider = 'fake' AND state = 'done'`).Scan(&follows); err != nil || follows != 0 {
		t.Fatalf("the next reply follows nothing: %d %v", follows, err)
	}
}
