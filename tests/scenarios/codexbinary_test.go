package scenarios

import (
	"context"
	"deck2/internal/provider"
	"deck2/internal/provider/codex"
	"deck2/internal/sandbox"
	"deck2/tests/harness"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestTheCodexProgramPassesItsSelfCheckOnAModelOfTheTest runs the self-check on the installed Codex in its own
// sandbox, its model an API of the test: a write, the skill deck gives the turn, a hidden file, ask, a resume, no network.
func TestTheCodexProgramPassesItsSelfCheckOnAModelOfTheTest(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skip("codex is not installed")
	}
	m := harness.NewResponses(t)
	codexHome := m.CodexHome(t)
	t.Setenv("CODEX_HOME", codexHome)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	a := codex.New(harness.HomeTemp(t))
	c := a.SelfCheck(ctx)
	if c.Err != nil || c.Outcome != provider.Done {
		t.Fatalf("codex %s self-check: %+v\nthe model did:\n%s", c.Version, c, m.Log())
	}
	if sandbox.Contained() {
		return
	}
	// The login of the owner's Codex stays closed to its shell, though Codex runs its helpers from its folder.
	login := filepath.Join(codexHome, "auth-probe.json")
	if err := os.WriteFile(login, []byte("codex-login-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	work := filepath.Join(string(a.Home), "work", "probe")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	cs := &provider.CheckServer{Answer: "x"}
	url, stop, err := provider.ServeCheck(cs.Server())
	if err != nil {
		t.Fatal(err)
	}
	defer stop()
	out := filepath.Join(work, "out.txt")
	if _, err := a.Run(ctx, provider.Request{Cwd: work, SessionID: uuid.NewString(), MCPURL: url, PermissionMCP: provider.Qualify("approve"),
		Prompt: "Run exactly this shell command once: cat " + login + " > " + out + " 2>&1"}); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(out); err != nil || strings.Contains(string(got), "codex-login-secret") {
		t.Fatalf("the shell of Codex read its login: %q %v\nthe model did:\n%s", got, err, m.Log())
	}
}
