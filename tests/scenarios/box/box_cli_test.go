package box

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/sandbox"
	"deck2/tests/harness"
)

// TestTheCLIOfAnAgentReadsNeitherDecksStateNorTheDataOfAnotherProject: on Linux Claude Code runs in its box, in its
// project's home with the one login, reads neither deck's state nor another's data, reaches no host without a right.
func TestTheCLIOfAnAgentReadsNeitherDecksStateNorTheDataOfAnotherProject(t *testing.T) {
	if !sandbox.Homed() {
		t.Skip("Claude Code runs in a box of its own on Linux")
	}
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	h := deckhome.Layout(e.Home)
	b := e.Project("b", harness.PlainManifest)
	theirs := filepath.Join(h.Data(b), "secret.txt")
	if err := os.MkdirAll(h.Data(b), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(theirs, []byte("data-of-b"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(h.Login("claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Login("claude"), ".credentials.json"), []byte(`{"login":"one"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	c.Before(`cat '` + h.DB() + `' > /dev/null 2>&1; echo $? > "$c/state"
cat '` + theirs + `' > "$c/theirs" 2>&1
curl -s -m 5 -o /dev/null -w '%{http_code}' https://example.com > "$c/net"
cat "$CLAUDE_CONFIG_DIR/.credentials.json" > "$c/login"`)
	a := e.Project("a", harness.PlainManifest)
	if got := e.WaitState(e.Run(a, "work"), "done", "error"); got.State != "done" {
		t.Fatalf("the turn runs in its box: %+v", got)
	}
	read := c.Files(1)
	if strings.TrimSpace(read("state")) == "0" || strings.Contains(read("theirs"), "data-of-b") {
		t.Fatalf("the shell of project a read deck's state (%s) or the data of b (%s)", read("state"), read("theirs"))
	}
	if got := strings.TrimSpace(read("net")); got != "000" {
		t.Fatalf("a host without a right is refused at the door: %q", got)
	}
	if read("login") != `{"login":"one"}` || c.Call(1).Env["CLAUDE_CONFIG_DIR"] != filepath.Join(h.AgentLock("claude"), "home") {
		t.Fatalf("the CLI finds the one login in its home: %q %v", read("login"), c.Call(1).Env["CLAUDE_CONFIG_DIR"])
	}
	if _, err := os.Stat(h.Agent("claude", a)); err != nil {
		t.Fatalf("the home of the CLI for project a is deck's: %v", err)
	}
}

// TestTheCodexACrashedDeckLeftIsKilledByTheMarkOfItsBox: an app-server a deck left running when it crashed carries the
// mark of its box, and the deck that starts again kills it.
func TestTheCodexACrashedDeckLeftIsKilledByTheMarkOfItsBox(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	raw, err := os.ReadFile(harness.Testdata("codex/hang.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	c.StreamText(1, strings.Join(append(lines[:6:6], `{"hang": true}`), "\n"))
	home := t.TempDir()
	work := filepath.Join(home, "work", "left")
	if err := os.MkdirAll(work, 0o700); err != nil {
		t.Fatal(err)
	}
	// The turn of a deck that is gone: nothing ends it but its mark.
	left := make(chan struct{})
	go func() {
		_, _ = c.Adapter(home).Run(context.Background(), provider.Request{Project: 1, Cwd: work, SessionID: "s-left",
			MCPURL: "http://127.0.0.1:1/mcp/x", Prompt: "Paint."})
		close(left)
	}()
	mark := sandbox.New(deckhome.Layout(home)).Mark(sandbox.KindAgent, "s-left")
	marked := func() bool {
		out, _ := exec.Command("ps", "-A", "-ww", "-o", "args=").Output()
		return strings.Contains(string(out), mark)
	}
	harness.Until(t, "the box of the app-server", marked)
	harness.New(t, harness.WithHome(home))
	harness.Until(t, "the end of the app-server the crashed deck left", func() bool { return !marked() })
	harness.Receive(t, "the end of the turn of the app-server", left)
}

// TestTheSessionsThatMayGoOnMoveIntoTheHomesOfTheirProjects: once, at its start, deck copies the session of a turn
// that may go on from the user's own Claude Code into the home of its project; a finished one stays the user's.
func TestTheSessionsThatMayGoOnMoveIntoTheHomesOfTheirProjects(t *testing.T) {
	if !sandbox.Homed() {
		t.Skip("Claude Code has homes of projects on Linux")
	}
	user := t.TempDir()
	t.Setenv("HOME", user)
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	p := e.Project("notes", harness.PlainManifest)
	ended, waits := e.Run(p, "work"), int64(0)
	e.WaitState(ended, "done")
	waits = e.Run(p, "work")
	e.WaitState(waits, "done")
	// A turn an older deck left waiting for the owner, its session in the user's own home of the CLI.
	e.SQL(`UPDATE process SET state = 'waiting_answer' WHERE id = ?`, waits)
	sessions := filepath.Join(user, ".claude", "projects", "-work-runs")
	for _, f := range []string{e.Session(ended) + ".jsonl", e.Session(waits) + ".jsonl", filepath.Join(e.Session(waits), "subagents", "a.jsonl")} {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(sessions, f)), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sessions, f), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// The base of a deck before the homes of projects, whose sessions have not moved.
	e.SQL(`DELETE FROM mark WHERE name = 'providers:sessions_moved'`)

	e = e.Restart()
	moved := filepath.Join(deckhome.Layout(e.Home).Agent("claude", p), "projects", "-work-runs")
	for _, f := range []string{e.Session(waits) + ".jsonl", filepath.Join(e.Session(waits), "subagents", "a.jsonl")} {
		if _, err := os.Stat(filepath.Join(moved, f)); err != nil {
			t.Fatalf("the session that may go on is in the home of its project: %v", err)
		}
	}
	if _, err := os.Stat(filepath.Join(moved, e.Session(ended)+".jsonl")); err == nil {
		t.Fatal("a finished session stays the user's")
	}
	if _, err := os.Stat(filepath.Join(sessions, e.Session(waits)+".jsonl")); err != nil {
		t.Fatalf("the user's own copy stays: %v", err)
	}
}

// TestALinkAnAgentPutsInTheSharedFolderOpensNothing: the folder every box of a CLI writes holds no file of deck's
// another box takes in: a link an agent leaves there is not followed, and the one login stays the only file shared.
func TestALinkAnAgentPutsInTheSharedFolderOpensNothing(t *testing.T) {
	if !sandbox.Homed() {
		t.Skip("Claude Code runs in a box of its own on Linux")
	}
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	h := deckhome.Layout(e.Home)
	secret := filepath.Join(h.State(), "planted-secret")
	if err := os.WriteFile(secret, []byte("deck-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	c.Before(`ln -sf '` + secret + `' "$CLAUDE_CONFIG_DIR/../.credentials.json"; ls -la "$CLAUDE_CONFIG_DIR/.." > "$c/lock"
cat "$CLAUDE_CONFIG_DIR/.credentials.json" > "$c/login" 2>&1`)
	p := e.Project("a", harness.PlainManifest)
	for range 2 {
		if got := e.WaitState(e.Run(p, "work"), "done", "error"); got.State != "done" {
			t.Fatalf("the turn runs: %+v", got)
		}
	}
	if got := c.Files(2)("login"); strings.Contains(got, "deck-only") {
		t.Fatalf("a link in the shared folder brings no file of deck into a box: %q", got)
	}
	if b, err := os.ReadFile(secret); err != nil || string(b) != "deck-only" {
		t.Fatalf("deck's file stays as it was: %q %v", b, err)
	}
}
