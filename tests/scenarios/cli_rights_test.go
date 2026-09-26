package scenarios

import (
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCommandAgentCannotReadSecretsAndGetsEveryDeckTool(t *testing.T) {
	c := newCLI(t)
	c.stream(0, "ok.jsonl")
	e, home := cliEnv(t, c)
	p := e.createProject("notes", postManifest("http://127.0.0.1:1", ""))
	if got := e.waitState(e.run(p, "work"), "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	call := c.call(1)
	var set struct {
		Permissions struct{ Deny []string }
	}
	if err := json.Unmarshal([]byte(call.settings), &set); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(home)
	for _, h := range []string{home, real} {
		for _, rule := range []string{"Read(/" + filepath.Join(h, "secrets") + "/**)", "Grep(/" + filepath.Join(h, "deck.db") + "*)",
			"Read(/" + filepath.Join(h, "sessions") + "/**)"} {
			if !slices.Contains(set.Permissions.Deny, rule) {
				t.Fatalf("the agent may not read %s: %v", rule, set.Permissions.Deny)
			}
		}
	}
	allowed, _ := flag(call.args, "--allowedTools")
	if !strings.Contains(allowed, "mcp__deck__notes__post") {
		t.Fatalf("deck tools skip the permission prompt, the gateway applies classes itself: %s", allowed)
	}
}
