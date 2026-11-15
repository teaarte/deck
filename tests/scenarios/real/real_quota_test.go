package real

import (
	"strings"
	"testing"
	"time"

	"deck2/internal/provider/claudecode"
	"deck2/tests/harness"
)

// TestRealClaudeCodeShowsTheQuotaOfItsSubscription: after one turn of the installed Claude Code the row of its
// subscription tells how much of its windows is used and when they reset.
func TestRealClaudeCodeShowsTheQuotaOfItsSubscription(t *testing.T) {
	realCLI(t)
	home := realHome(t)
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(claudecode.New(home, nil)), harness.WithWait(4*time.Minute))
	p := e.Project("plain", harness.OnLogin(harness.PlainManifest))
	if got := e.WaitState(e.Run(p, "work"), "done", "error"); got.State != "done" {
		t.Fatalf("the turn is done: %+v", got)
	}
	h := e.HealthRowOf("limit:claude-code")
	t.Logf("limit:claude-code: %v", h)
	if h == nil || !strings.Contains(h["error"].(string), "percent used, resets at") {
		t.Fatalf("the row of the subscription tells its windows: %v", h)
	}
}
