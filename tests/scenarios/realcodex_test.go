package scenarios

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider/codex"
)

func realCodex(t *testing.T) {
	t.Helper()
	if os.Getenv("DECK_REAL_CODEX") != "1" {
		t.Skip("set DECK_REAL_CODEX=1 to run against the installed Codex")
	}
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex is not installed")
	}
}

// TestRealCodexSelfCheck runs the self-check of the Codex adapter on the installed CLI and the owner's login.
func TestRealCodexSelfCheck(t *testing.T) {
	realCodex(t)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	start := time.Now()
	c := codex.New(t.TempDir()).SelfCheck(ctx)
	t.Logf("codex %s: self-check in %s", c.Version, time.Since(start).Round(time.Second))
	if c.Err != nil || c.RateLimited || c.NeedsLogin {
		t.Fatalf("self-check: %+v", c)
	}
}

// TestRealCodexQuestion has a command agent on the installed Codex ask the owner through the ask tool of deck
// and carry the answer into its result.
func TestRealCodexQuestion(t *testing.T) {
	realCodex(t)
	home := t.TempDir()
	e := newEnvWith(t, home, codex.New(home), realWaits)
	p := e.createProject("ask", `{"version": 5,
  "commands": [{"name": "work", "title": "Work", "description": "Asks the owner", "class": "internal",
    "result": {"type": "object", "required": ["color"], "properties": {"color": {"type": "string"}}},
    "executor": {"type": "agent", "prompt": "Ask the owner which color the report should use, with the options red and blue. The result is the color the owner picked."}}]}`)
	id := e.run(p, "work")
	deadline := time.Now().Add(5 * time.Minute)
	asked := false
	for st := e.process(id).State; time.Now().Before(deadline) && st != "done" && st != "error"; st = e.process(id).State {
		var asks []askView
		_, out := e.call("deck.asks_list", nil)
		_ = json.Unmarshal(out, &asks)
		for _, ask := range asks {
			if ask.Kind != "question" || !strings.Contains(strings.ToLower(ask.Text), "color") {
				t.Fatalf("only the question waits for the owner, got %+v", ask)
			}
			t.Logf("question: %s", ask.Text)
			e.answer(ask.ID, "blue")
			asked = true
		}
		time.Sleep(500 * time.Millisecond)
	}
	got := e.process(id)
	t.Logf("state %s, result %s, error %s", got.State, got.Result, got.Error)
	if !asked || got.State != "done" || !strings.Contains(string(got.Result), `"blue"`) {
		t.Fatalf("want the owner's answer in the result: %+v", got)
	}
}
