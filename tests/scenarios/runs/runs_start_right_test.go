package runs

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestAnAllowedStartIsNotToldAsRejected: a start request the owner allows is spent by the turn it starts; the agent's
// next resume does not hear it as a refusal.
func TestAnAllowedStartIsNotToldAsRejected(t *testing.T) {
	t.Parallel()
	for _, answer := range []string{"allow", "allow_work", "allow_always"} {
		t.Run(answer, func(t *testing.T) {
			t.Parallel()
			var mu sync.Mutex
			var resumed string
			e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
				if !c.Req.Resume {
					c.Approve(ctx, "AskUserQuestion", map[string]any{"questions": []map[string]any{{"question": "Which topic?"}}})
					return provider.Result{Text: "stopped"}
				}
				mu.Lock()
				resumed += c.Req.Prompt + "\n"
				mu.Unlock()
				harness.WriteIdeas(ctx, c, "rust")
				return provider.Result{Structured: json.RawMessage(`{"written": 3}`)}
			}), harness.WithConfig(func(c *app.Config) { c.QuestionWait = 100 * time.Millisecond }))

			var p struct{ ID int64 }
			e.Must("deck.project_create", map[string]any{"name": "ideas", "manifest": harness.Declare(harness.IdeasManifest,
				`{"right": "network", "who": "agent", "what": ["example.com"], "why": "reads the news"}`)}, &p)
			e.Names[p.ID] = "ideas"
			id := e.Run(p.ID, "collect")
			start := e.WaitAsk()
			if !strings.Contains(start.Text, "wants to start") {
				t.Fatalf("want the start request first, got %+v", start)
			}
			e.Answer(start.ID, answer)
			e.WaitState(id, "waiting:answer")
			var q harness.Ask
			e.Until("the question", func() bool {
				for _, a := range e.Asks() {
					if a.ID != start.ID {
						q = a
						return true
					}
				}
				return false
			})
			e.Answer(q.ID, "rust")
			e.WaitState(id, "done")
			mu.Lock()
			defer mu.Unlock()
			if strings.Contains(resumed, "wants to start") || strings.Contains(resumed, "Rejected") {
				t.Fatalf("the resume tells the allowed start as something else:\n%s", resumed)
			}
		})
	}
}
