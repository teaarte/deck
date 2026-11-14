package providers

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"deck2/tests/harness"
)

// TestTheStartOfDeckRunsNoTurnOfACLI: a CLI spends turns of the subscription, so deck does not check it as it starts;
// the owner's button does.
func TestTheStartOfDeckRunsNoTurnOfACLI(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.ChecksPass()
	e := harness.ClockCLIEnv(t, c)
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	e.Settle()
	if n := c.Calls(); n != 0 {
		t.Fatalf("the start of deck runs no turn of the CLI: %d calls", n)
	}
	if h := e.HealthRowOf("provider:claude-code"); h != nil && h["status"] == "checking" {
		t.Fatalf("no self-check of the CLI runs at the start: %v", h)
	}
	e.Must("deck.provider_check", map[string]any{"provider": "claude-code"}, nil)
	e.WaitHealth("provider:claude-code", "ok")
}

// TestTheModelsOfOneConnectionAreCheckedOneAtATime: two models of one connection checked together take turns, so
// their calls never meet the limit a minute of the account at once; the calls of a check are in the journal.
func TestTheModelsOfOneConnectionAreCheckedOneAtATime(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	var inflight, most atomic.Int64
	llm := harness.NewLLM(t, func(int, harness.ChatRequest) harness.ChatReply {
		now := inflight.Add(1)
		defer inflight.Add(-1)
		for m := most.Load(); now > m && !most.CompareAndSwap(m, now); m = most.Load() {
		}
		time.Sleep(50 * time.Millisecond)
		return harness.TextReply("ok")
	})
	for i := 4; i <= 12; i++ {
		f.Stream(i, "selfcheck_"+strconv.Itoa((i-1)%3+1)+".jsonl")
	}
	e := harness.New(t, harness.WithACP(f, llm))
	e.Must("deck.model_set", map[string]any{"connection": "acp", "name": "acp2", "model": "m2", "agent": "goose"}, nil)
	e.WaitHealth("provider:acp2", "ok")
	most.Store(0)
	e.App.Providers.Check("acp")
	e.App.Providers.Check("acp2")
	e.Until("both self-checks end", func() bool { return f.Calls() >= 12 && inflight.Load() == 0 })
	e.WaitHealth("provider:acp", "ok")
	e.WaitHealth("provider:acp2", "ok")
	if n := most.Load(); n != 1 {
		t.Fatalf("the models of one connection are checked one at a time: %d calls at once", n)
	}
	var calls []map[string]any
	e.ModelCalls("", 0, &calls)
	checks := 0
	for _, c := range calls {
		if c["command"] == "self-check" && fmt.Sprint(c["process"]) == "0" {
			checks++
		}
	}
	if checks == 0 {
		t.Fatalf("the calls of a self-check are in the journal of model calls: %+v", calls)
	}
}
