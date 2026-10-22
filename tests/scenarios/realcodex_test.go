package scenarios

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
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
	if c.Err != nil || c.Outcome != provider.Done {
		t.Fatalf("self-check: %+v", c)
	}
}

// TestRealCodexQuestion has a command agent on the installed Codex ask the owner through the ask tool of deck
// and carry the answer into its result.
func TestRealCodexQuestion(t *testing.T) {
	realCodex(t)
	home := t.TempDir()
	e := newEnvWith(t, home, codex.New(home), realWaits)
	p := e.createProject("ask", `{"version": 13,
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

// TestRealCodexCommitsInTheFolder has an agent command on the installed Codex work in a project folder that is a
// repo: it sees the AGENTS.md of the repo, not of the folder above, and commits there, .git included.
func TestRealCodexCommitsInTheFolder(t *testing.T) {
	realCodex(t)
	outer := t.TempDir()
	repo := filepath.Join(outer, "repo")
	git := func(args ...string) string {
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "deck@example.com")
	git("config", "user.name", "deck test")
	for name, text := range map[string]string{"AGENTS.md": "Marker word: OUTERMARK.", "repo/AGENTS.md": "Marker word: REPOMARK.", "repo/README.md": "A test repo."} {
		if err := os.WriteFile(filepath.Join(outer, name), []byte(text+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git("add", "-A")
	git("commit", "-q", "-m", "start")
	home := t.TempDir()
	e := newEnvWith(t, home, codex.New(home), realWaits)
	cmd, _ := json.Marshal(map[string]any{"name": "work", "title": "Work", "description": "Commits a file.", "class": "internal",
		"result": map[string]any{"type": "object", "required": []string{"markers", "commit"},
			"properties": map[string]any{"markers": map[string]any{"type": "string"}, "commit": map[string]any{"type": "string"}}},
		"executor": map[string]any{"type": "agent", "folder": true, "prompt": "Create the file hello.txt with the text hi in the current folder " +
			"and commit it with git with the message 'add hello'. The result: markers are the marker words (ALL CAPS words ending in MARK) " +
			"that appear in your instructions, separated by spaces; commit is the full sha of your commit."}})
	p := e.createProject("code", `{"version": 13, "commands": [`+string(cmd)+`]}`)
	e.must("deck.folder_add", map[string]any{"project": "code", "path": repo}, nil)
	id := e.run(p, "work")
	got := e.pollState(id, 6*time.Minute, time.Second, "done", "error")
	t.Logf("state %s, result %s, error %s", got.State, got.Result, got.Error)
	if got.State != "done" {
		t.Fatalf("want done: %+v", got)
	}
	var res struct{ Markers, Commit string }
	_ = json.Unmarshal(got.Result, &res)
	if !strings.Contains(res.Markers, "REPOMARK") || strings.Contains(res.Markers, "OUTERMARK") {
		t.Errorf("the agent sees the AGENTS.md of the repo only: %q", res.Markers)
	}
	if head := git("rev-parse", "HEAD"); head != res.Commit || git("log", "-1", "--format=%s") != "add hello" ||
		git("show", "--name-only", "--format=", "HEAD") != "hello.txt" {
		t.Fatalf("the agent committed hello.txt in the folder: head %s, result %s, log %s", head, res.Commit, git("log", "--oneline"))
	}
}

// TestRealCodexResolvesItsRunAndKeepsCaches has an agent on the installed Codex, with deck's folder in the owner's
// home as it lives, resolve the folders above its run, write a cache in the data folder and fail to read deck's state.
func TestRealCodexResolvesItsRunAndKeepsCaches(t *testing.T) {
	realCodex(t)
	user, _ := os.UserHomeDir()
	home, err := os.MkdirTemp(user, ".deck-codex-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(home) })
	e := newEnvWith(t, home, codex.New(home), realWaits)
	secret := filepath.Join(home, "state", "probe")
	if err := os.WriteFile(secret, []byte("closed"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := e.createProject("probe", `{"version": 13,
  "commands": [{"name": "work", "title": "Work", "description": "Probes the shell", "class": "internal",
    "result": {"type": "object", "required": ["real", "cache", "state"], "properties": {"real": {"type": "string"}, "cache": {"type": "string"}, "state": {"type": "string"}}},
    "executor": {"type": "agent", "prompt": "Run these shell commands once each, without asking for more rights, and report what each printed. 1. python3 -c \"import os; print(os.path.realpath('.'))\" as real. 2. mkdir -p \"$XDG_CACHE_HOME/probe\" 2>&1 && echo ok > \"$XDG_CACHE_HOME/probe/x\" 2>&1 && cat \"$XDG_CACHE_HOME/probe/x\" as cache, with all it printed. 3. cat `+secret+` as state (empty when it failed)."}}]}`)
	id := e.run(p, "work")
	deadline := time.Now().Add(5 * time.Minute)
	for st := e.process(id).State; time.Now().Before(deadline) && st != "done" && st != "error"; st = e.process(id).State {
		time.Sleep(time.Second)
	}
	got := e.process(id)
	t.Logf("state %s, result %s, error %s", got.State, got.Result, got.Error)
	var r struct{ Real, Cache, State string }
	if got.State != "done" || json.Unmarshal(got.Result, &r) != nil {
		t.Fatalf("the probe ends: %+v", got)
	}
	if !strings.Contains(r.Real, filepath.Join("runs", itoa(id))) || !strings.HasSuffix(strings.TrimSpace(r.Cache), "ok") || strings.Contains(r.State, "closed") {
		t.Fatalf("the run resolves, the cache is written, the state stays closed: %+v", r)
	}
}
