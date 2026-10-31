package harness

import (
	"os"
	"testing"
)

// needs are what each switch of a test on real programs turns on.
var needs = map[string]string{
	"DECK_REAL_BROWSER":  "drive the real browser of Playwright MCP",
	"DECK_REAL_CLI":      "run against the real Claude Code CLI",
	"DECK_REAL_CODEX":    "run against the installed Codex",
	"DECK_REAL_ACP":      "run the installed ACP agents on a local Ollama",
	"DECK_REAL_PACKAGES": "install packages from PyPI and npm through deck's proxy",
	"DECK_SERVER":        "run deck's image on a bare docker; needs docker with buildx",
}

// Needs skips a test of real programs unless its switch env is set.
func Needs(t *testing.T, env string) {
	t.Helper()
	what, ok := needs[env]
	if !ok {
		t.Fatalf("no switch %s of real programs", env)
	}
	if os.Getenv(env) == "" {
		t.Skipf("set %s=1 to %s", env, what)
	}
}
