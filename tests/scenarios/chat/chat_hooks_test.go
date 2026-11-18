package chat

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestAWorkHeardTwiceTellsItsTopicOnce: a work from a topic that deck hears twice after it closed tells the topic how
// it ended once.
func TestAWorkHeardTwiceTellsItsTopicOnce(t *testing.T) {
	t.Parallel()
	var replies atomic.Int64
	var from atomic.Int64
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() != "work" {
			if replies.Add(1) > 1 {
				return provider.Result{Text: "ok"}
			}
			return harness.HandOver(ctx, c, &from)
		}
		return provider.Result{Text: "Counted: 3 records."}
	}))
	topic := e.Say(0, "count the records")
	done := e.Told(topic, "done", "")
	e.HearAgain(done.Process)
	if n := deckMessages(e, topic); n != 1 {
		t.Fatalf("a work heard twice tells its topic once: %d", n)
	}
}

// deckMessages is how many messages of deck a topic has.
func deckMessages(e *harness.Env, topic int64) int {
	var view struct{ Messages []harness.ToldMessage }
	e.ChatRead(map[string]any{"topic": fmt.Sprint(topic)}, &view)
	n := 0
	for _, m := range view.Messages {
		if m.Role == "deck" {
			n++
		}
	}
	return n
}
