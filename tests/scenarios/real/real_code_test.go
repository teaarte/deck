package real

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"deck2/internal/provider"
	"deck2/internal/provider/claudecode"
	"deck2/internal/provider/codex"
	"deck2/tests/harness"
)

// TestRealCodeModule rehearses the code module on the installed CLIs in a fresh repo with uncommitted work of
// the owner: a run of two tasks, a merge asked for by a remark, then a revert by the button's command.
func TestRealCodeModule(t *testing.T) {
	for _, c := range []struct {
		name, env, bin string
		new            func(home string) provider.Provider
	}{
		{"claude-code", "DECK_REAL_CLI", "claude", func(home string) provider.Provider { return claudecode.New(home) }},
		{"codex", "DECK_REAL_CODEX", "codex", func(home string) provider.Provider { return codex.New(home) }},
	} {
		t.Run(c.name, func(t *testing.T) {
			harness.Needs(t, c.env)
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
	write("TODO.md", "- nothing yet\n")
	write("calc.py", "def add(a, b):\n    return a + b\n")
	write("test_calc.py", "import unittest\n\nfrom calc import add\n\n\nclass TestCalc(unittest.TestCase):\n    def test_add(self):\n        self.assertEqual(add(2, 3), 5)\n\n\nif __name__ == \"__main__\":\n    unittest.main()\n")
	git("add", "-A")
	git("commit", "-q", "-m", "start")
	start := git("rev-parse", "HEAD")
	write("README.md", "A tiny calculator.\nOwner's note, not committed.\n")
	write("notes.txt", "owner's untracked notes\n")
	git("add", "README.md")
	write("TODO.md", "- nothing yet\n- the owner's unstaged line\n")
	status := git("status", "--porcelain")
	if status != "M  README.md\n M TODO.md\n?? notes.txt" {
		t.Fatalf("the owner's work: %q", status)
	}

	home := t.TempDir()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(newProvider(home)), harness.WithConfig(realWaits))
	e.ImportEnabled(harness.CodeModule(t, ""), "code")
	e.Must("deck.folder_add", map[string]any{"project": "code", "path": repo}, nil)
	for _, c := range []struct{ title, text string }{
		{"Add subtract", "Add a function subtract(a, b) to calc.py that returns a - b, with a test in test_calc.py."},
		{"Add multiply", "Add a function multiply(a, b) to calc.py that returns a * b, with a test in test_calc.py."},
	} {
		e.Must("deck.records_upsert", map[string]any{"project": "code", "collection": "tasks",
			"records": harness.Jsonl(map[string]any{"title": c.title, "text": c.text, "status": "ready"})}, nil)
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
	wait := func(id int64, what string) harness.Proc {
		t.Helper()
		got := e.WaitState(id, "done", "error", "stopped", "interrupted")
		t.Logf("%s: %s %s %s", what, got.State, got.Result, got.Error)
		if got.State != "done" {
			t.Fatalf("%s: %+v", what, got)
		}
		return got
	}
	lanes := func() map[string][]string {
		t.Helper()
		var rs []harness.RecordRow
		e.Must("deck.records_list", map[string]any{"project": "code", "collection": "tasks"}, &rs)
		out := map[string][]string{}
		for _, r := range rs {
			status := fmt.Sprint(r.Data["status"])
			out[status] = append(out[status], fmt.Sprint(r.Data["title"])+"\n"+fmt.Sprint(r.Data["text"]))
		}
		return out
	}

	var run struct{ Process int64 }
	e.Must("code.run", map[string]any{"do": "run"}, &run)
	wait(run.Process, "run")
	owner("the run")
	branch := "deck/run-" + harness.Itoa(run.Process)
	commits := git("log", "--oneline", "main.."+branch)
	calc := git("show", branch+":calc.py")
	t.Logf("%s:\n%s", branch, commits)
	if commits == "" || !strings.Contains(calc, "def subtract") || !strings.Contains(calc, "def multiply") || git("rev-parse", "main") != start {
		t.Fatalf("the run commits both tasks on its branch and leaves main: %s\n%s", commits, calc)
	}
	review := lanes()["review"]
	if len(review) != 2 || !strings.Contains(review[0], "**Run "+harness.Itoa(run.Process)+"**") || !strings.Contains(review[1], "**Run "+harness.Itoa(run.Process)+"**") {
		t.Fatalf("both tasks are in review with a report: %v", lanes())
	}
	t.Logf("report: %s", review[0])

	e.Must("deck.process_rework", map[string]any{"process": run.Process, "remark": "Looks good. Merge this run into main."}, nil)
	e.WaitState(run.Process, "running", "done")
	wait(run.Process, "merge by a remark")
	owner("the merge")
	subject := git("log", "-1", "--format=%s", "main")
	if !strings.HasPrefix(subject, "deck run "+harness.Itoa(run.Process)+":") || git("rev-list", "--count", start+"..main") != "1" ||
		!strings.Contains(git("show", "main:calc.py"), "def multiply") {
		t.Fatalf("the merge is one squash commit on main: %s\n%s", subject, git("log", "--oneline", "main"))
	}
	if got := git("branch", "--list", branch); got != "" {
		t.Fatalf("the merged branch is deleted: %s", got)
	}
	if done := lanes()["done"]; len(done) != 2 {
		t.Fatalf("merged tasks are done: %v", lanes())
	}

	var revert struct{ Process int64 }
	e.Must("code.run", map[string]any{"do": "revert"}, &revert)
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

	var prompt struct{ Text string }
	e.Must("deck.document_get", map[string]any{"project": "code", "name": "prompt.md"}, &prompt)
	e.Must("deck.document_save", map[string]any{"project": "code", "name": "prompt.md", "kind": "prompt", "text": prompt.Text + "\nMerge: yourself.\n"}, nil)
	e.Must("deck.records_upsert", map[string]any{"project": "code", "collection": "tasks", "records": harness.Jsonl(map[string]any{"title": "Add divide",
		"status": "ready", "text": "Add a function divide(a, b) to calc.py that returns a / b, with a test in test_calc.py."})}, nil)
	before := git("rev-parse", "main")
	var auto struct{ Process int64 }
	e.Must("code.run", map[string]any{"do": "run"}, &auto)
	wait(auto.Process, "run in auto")
	owner("the run in auto")
	if subject := git("log", "-1", "--format=%s", "main"); !strings.HasPrefix(subject, "deck run "+harness.Itoa(auto.Process)+":") ||
		git("rev-list", "--count", before+"..main") != "1" || !strings.Contains(git("show", "main:calc.py"), "def divide") {
		t.Fatalf("in auto the agent merges its run itself: %s\n%s", subject, git("log", "--oneline", "main"))
	}
	var notes []string
	for _, n := range e.Notes() {
		notes = append(notes, n.Text)
	}
	t.Logf("notifications: %v", notes)
	if len(notes) != 1 {
		t.Fatalf("in auto the merge is one line in the cockpit feed: %v", notes)
	}
}
