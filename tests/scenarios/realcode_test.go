package scenarios

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/internal/provider/claudecode"
	"deck2/internal/provider/codex"
)

// TestRealCodeModule rehearses the code module on the installed CLIs in a fresh repo with uncommitted work of
// the owner: a run of two cards, a merge asked for by a remark, then a revert by the button's command.
func TestRealCodeModule(t *testing.T) {
	for _, c := range []struct {
		name, env, bin string
		new            func(home string) provider.Provider
	}{
		{"claude-code", "DECK_REAL_CLI", "claude", func(home string) provider.Provider { return claudecode.New(filepath.Join(home, "mcp")) }},
		{"codex", "DECK_REAL_CODEX", "codex", func(home string) provider.Provider { return codex.New(home) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			if os.Getenv(c.env) != "1" {
				t.Skipf("set %s=1 to rehearse the code module on the installed %s", c.env, c.name)
			}
			if _, err := exec.LookPath(c.bin); err != nil {
				t.Skipf("%s is not installed", c.bin)
			}
			rehearseCodeModule(t, c.new)
		})
	}
}

func rehearseCodeModule(t *testing.T, newProvider func(home string) provider.Provider) {
	repo := filepath.Join(t.TempDir(), "calc")
	git := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, text string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git("init", "-q", "-b", "main")
	git("config", "user.email", "owner@example.com")
	git("config", "user.name", "Owner")
	rules := "# calc\n\nTests: `python3 -m unittest -q` in the repo root; run them before each commit.\nCommit each change with a short message.\n"
	write("CLAUDE.md", rules)
	write("AGENTS.md", rules)
	write("README.md", "A tiny calculator.\n")
	write("calc.py", "def add(a, b):\n    return a + b\n")
	write("test_calc.py", "import unittest\n\nfrom calc import add\n\n\nclass TestCalc(unittest.TestCase):\n    def test_add(self):\n        self.assertEqual(add(2, 3), 5)\n\n\nif __name__ == \"__main__\":\n    unittest.main()\n")
	git("add", "-A")
	git("commit", "-q", "-m", "start")
	start := git("rev-parse", "HEAD")
	write("README.md", "A tiny calculator.\nOwner's note, not committed.\n")
	write("notes.txt", "owner's untracked notes\n")
	status := git("status", "--porcelain")

	home := t.TempDir()
	e := newEnvWith(t, home, newProvider(home), realWaits)
	e.importEnabled(codeModule(t, ""), "code")
	e.must("deck.folder_add", map[string]any{"project": "code", "path": repo}, nil)
	for _, c := range []struct{ title, text string }{
		{"Add subtract", "Add a function subtract(a, b) to calc.py that returns a - b, with a test in test_calc.py."},
		{"Add multiply", "Add a function multiply(a, b) to calc.py that returns a * b, with a test in test_calc.py."},
	} {
		e.must("deck.card_create", map[string]any{"project": "code", "title": c.title, "text": c.text, "lane": "ready"}, nil)
	}
	owner := func(what string) {
		t.Helper()
		if got := git("status", "--porcelain"); got != status {
			t.Fatalf("after %s the owner's uncommitted work is as it was: %q, want %q", what, got, status)
		}
		if b, _ := os.ReadFile(filepath.Join(repo, "README.md")); !strings.Contains(string(b), "Owner's note") {
			t.Fatalf("after %s README keeps the owner's edit: %s", what, b)
		}
		if got := git("rev-parse", "--abbrev-ref", "HEAD"); got != "main" {
			t.Fatalf("after %s the checkout is back on main: %s", what, got)
		}
		if got := git("stash", "list"); got != "" {
			t.Fatalf("after %s no stash is left: %s", what, got)
		}
	}
	wait := func(id int64, what string) proc {
		t.Helper()
		got := e.pollState(id, 25*time.Minute, 2*time.Second, "done", "error", "stopped", "interrupted")
		t.Logf("%s: %s %s %s", what, got.State, got.Result, got.Error)
		if got.State != "done" {
			t.Fatalf("%s: %+v", what, got)
		}
		return got
	}
	lanes := func() map[string][]string {
		t.Helper()
		var b struct {
			Lanes []struct {
				Name  string
				Cards []struct{ Title, Text string }
			}
		}
		e.must("deck.cards_list", map[string]any{"project": "code"}, &b)
		out := map[string][]string{}
		for _, l := range b.Lanes {
			for _, c := range l.Cards {
				out[l.Name] = append(out[l.Name], c.Title+"\n"+c.Text)
			}
		}
		return out
	}

	var run struct{ Process int64 }
	e.must("code.run", map[string]any{"do": "run"}, &run)
	wait(run.Process, "run")
	owner("the run")
	branch := "deck/run-" + itoa(run.Process)
	commits := git("log", "--oneline", "main.."+branch)
	calc := git("show", branch+":calc.py")
	t.Logf("%s:\n%s", branch, commits)
	if commits == "" || !strings.Contains(calc, "def subtract") || !strings.Contains(calc, "def multiply") || git("rev-parse", "main") != start {
		t.Fatalf("the run commits both cards on its branch and leaves main: %s\n%s", commits, calc)
	}
	review := lanes()["review"]
	if len(review) != 2 || !strings.Contains(review[0], "**Run "+itoa(run.Process)+"**") || !strings.Contains(review[1], "**Run "+itoa(run.Process)+"**") {
		t.Fatalf("both cards are in Review with a report: %v", lanes())
	}
	t.Logf("report: %s", review[0])

	e.must("deck.process_rework", map[string]any{"process": run.Process, "remark": "Looks good. Merge this run into main."}, nil)
	e.waitState(run.Process, "running", "done")
	wait(run.Process, "merge by a remark")
	owner("the merge")
	subject := git("log", "-1", "--format=%s", "main")
	if !strings.HasPrefix(subject, "deck run "+itoa(run.Process)+":") || git("rev-list", "--count", start+"..main") != "1" ||
		!strings.Contains(git("show", "main:calc.py"), "def multiply") {
		t.Fatalf("the merge is one squash commit on main: %s\n%s", subject, git("log", "--oneline", "main"))
	}
	if done := lanes()["done"]; len(done) != 2 {
		t.Fatalf("merged cards are done: %v", lanes())
	}

	var revert struct{ Process int64 }
	e.must("code.run", map[string]any{"do": "revert"}, &revert)
	got := wait(revert.Process, "revert")
	owner("the revert")
	if diff := git("diff", start, "main", "--", "calc.py", "test_calc.py"); diff != "" {
		t.Fatalf("after the revert main is as it was: %s", diff)
	}
	var res struct{ Summary string }
	_ = json.Unmarshal(got.Result, &res)
	if res.Summary == "" {
		t.Fatalf("the revert says what it did: %s", got.Result)
	}

	e.must("deck.project_autonomy", map[string]any{"project": "code", "autonomy": "auto"}, nil)
	e.must("deck.card_create", map[string]any{"project": "code", "title": "Add divide", "lane": "ready",
		"text": "Add a function divide(a, b) to calc.py that returns a / b, with a test in test_calc.py."}, nil)
	before := git("rev-parse", "main")
	var auto struct{ Process int64 }
	e.must("code.run", map[string]any{"do": "run"}, &auto)
	wait(auto.Process, "run in auto")
	owner("the run in auto")
	if subject := git("log", "-1", "--format=%s", "main"); !strings.HasPrefix(subject, "deck run "+itoa(auto.Process)+":") ||
		git("rev-list", "--count", before+"..main") != "1" || !strings.Contains(git("show", "main:calc.py"), "def divide") {
		t.Fatalf("in auto the agent merges its run itself: %s\n%s", subject, git("log", "--oneline", "main"))
	}
	var notes []string
	rows, err := e.App.Store.DB.Query(`SELECT text FROM notification`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var s string
		_ = rows.Scan(&s)
		notes = append(notes, s)
	}
	_ = rows.Close()
	t.Logf("notifications: %v", notes)
	if len(notes) != 1 {
		t.Fatalf("in auto the merge is one line in the cockpit feed: %v", notes)
	}
}
