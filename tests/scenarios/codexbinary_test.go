package scenarios

import (
	"context"
	"os/exec"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/internal/provider/codex"
)

// TestTheCodexProgramPassesItsSelfCheckOnAModelOfTheTest runs the self-check on the installed Codex in its own
// sandbox, its model an API of the test: a write, the skill deck gives the turn, a hidden file, ask, a resume, no network.
func TestTheCodexProgramPassesItsSelfCheckOnAModelOfTheTest(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex is not installed")
	}
	m := newResponses(t)
	t.Setenv("CODEX_HOME", m.codexHome(t))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	c := codex.New(homeTemp(t)).SelfCheck(ctx)
	if c.Err != nil || c.Outcome != provider.Done {
		t.Fatalf("codex %s self-check: %+v\nthe model did:\n%s", c.Version, c, m.log())
	}
}
