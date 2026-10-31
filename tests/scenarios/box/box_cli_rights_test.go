package box

import (
	deckhome "deck2/internal/home"
	"deck2/tests/harness"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestCommandAgentCannotReadSecretsAndGetsEveryDeckTool(t *testing.T) {
	user := t.TempDir()
	_ = os.MkdirAll(filepath.Join(user, ".claude", "projects", "-elsewhere"), 0o700)
	_ = os.WriteFile(filepath.Join(user, ".claude", "settings.json"), []byte("{}"), 0o600)
	t.Setenv("HOME", user)
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	home := e.Home
	p := e.Project("notes", harness.PostManifest("http://127.0.0.1:1", ""))
	if got := e.WaitState(e.Run(p, "work"), "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	call := c.Call(1)
	var set struct {
		Permissions struct{ Deny []string }
	}
	if err := json.Unmarshal([]byte(call.Settings), &set); err != nil {
		t.Fatal(err)
	}
	real, _ := filepath.EvalSymlinks(home)
	for _, h := range []string{home, real} {
		for _, rule := range []string{"Read(/" + deckhome.Layout(h).State() + "/**)", "Edit(/" + deckhome.Layout(h).State() + "/**)"} {
			if !slices.Contains(set.Permissions.Deny, rule) {
				t.Fatalf("the agent may not read %s: %v", rule, set.Permissions.Deny)
			}
		}
	}
	for _, rule := range []string{"Read(/" + filepath.Join(user, ".ssh") + "/**)",
		"Read(/" + filepath.Join(user, ".claude", ".credentials.json") + ")", "Read(/" + filepath.Join(user, ".claude", "settings.json") + ")",
		"Edit(/" + filepath.Join(user, ".codex") + "/**)", "Read(/" + filepath.Join(user, ".local", "share", "opencode") + "/**)",
		"Read(/" + filepath.Join(user, ".config", "goose") + "/**)"} {
		if !slices.Contains(set.Permissions.Deny, rule) {
			t.Fatalf("the agent may not read the owner's %s: %v", rule, set.Permissions.Deny)
		}
	}
	if slices.ContainsFunc(set.Permissions.Deny, func(d string) bool { return strings.Contains(d, "/.claude/projects") }) {
		t.Fatalf("the sessions of the CLI, where it keeps long tool output, stay readable: %v", set.Permissions.Deny)
	}
	allowed, _ := harness.Flag(call.Args, "--allowedTools")
	if !strings.Contains(allowed, "mcp__deck__notes__post") {
		t.Fatalf("deck tools skip the permission prompt, the gateway applies classes itself: %s", allowed)
	}
}
