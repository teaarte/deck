package scenarios

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

func TestCommandAgentCannotReadSecretsAndGetsEveryDeckTool(t *testing.T) {
	user := t.TempDir()
	_ = os.MkdirAll(filepath.Join(user, ".claude", "projects", "-elsewhere"), 0o700)
	_ = os.WriteFile(filepath.Join(user, ".claude", "settings.json"), []byte("{}"), 0o600)
	t.Setenv("HOME", user)
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
		for _, rule := range []string{"Read(/" + filepath.Join(h, "secrets") + "/**)", "Edit(/" + filepath.Join(h, "deck.db") + "*)",
			"Read(/" + filepath.Join(h, "sessions") + "/**)"} {
			if !slices.Contains(set.Permissions.Deny, rule) {
				t.Fatalf("the agent may not read %s: %v", rule, set.Permissions.Deny)
			}
		}
	}
	for _, rule := range []string{"Read(/" + filepath.Join(home, "tailscale") + "/**)", "Read(/" + filepath.Join(user, ".ssh") + "/**)",
		"Read(/" + filepath.Join(user, ".claude", "projects", "-elsewhere") + "/**)", "Read(/" + filepath.Join(user, ".claude", "settings.json") + ")",
		"Edit(/" + filepath.Join(user, ".codex") + "/**)"} {
		if !slices.Contains(set.Permissions.Deny, rule) {
			t.Fatalf("the agent may not read the owner's %s: %v", rule, set.Permissions.Deny)
		}
	}
	runs := regexp.MustCompile(`[^A-Za-z0-9]`).ReplaceAllString(filepath.Join(home, "runs"), "-")
	if longer := "Read(/" + filepath.Join(user, ".claude", "projects", runs+"-??*-work") + "/**)"; !slices.Contains(set.Permissions.Deny, longer) {
		t.Fatalf("the sessions of other runs of deck, made later too, stay hidden: want %s in %v", longer, set.Permissions.Deny)
	}
	if slices.ContainsFunc(set.Permissions.Deny, func(d string) bool { return strings.HasSuffix(d, "/.claude/projects/**)") }) {
		t.Fatalf("the folder of its own session, where the CLI keeps long tool output, stays readable: %v", set.Permissions.Deny)
	}
	allowed, _ := flag(call.args, "--allowedTools")
	if !strings.Contains(allowed, "mcp__deck__notes__post") {
		t.Fatalf("deck tools skip the permission prompt, the gateway applies classes itself: %s", allowed)
	}
}
