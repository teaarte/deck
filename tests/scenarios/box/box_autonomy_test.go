package box

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	deckhome "deck2/internal/home"
	"deck2/internal/sandbox"
	"deck2/tests/harness"
)

// denied reports whether a deny rule of the permission rule syntax covers path or a folder above it.
func denied(rules []string, path string) bool {
	for _, r := range rules {
		_, pat, ok := strings.Cut(strings.TrimSuffix(r, ")"), "(/")
		if !ok {
			continue
		}
		dir, tree := strings.CutSuffix(pat, "/**")
		for p := path; p != filepath.Dir(p); p = filepath.Dir(p) {
			if ok, _ := filepath.Match(dir, p); ok && (tree || p == path) {
				return true
			}
		}
	}
	return false
}

type cliSettings struct {
	Permissions struct{ Deny []string }
	Sandbox     *struct {
		Enabled                  bool
		FailIfUnavailable        bool
		AllowUnsandboxedCommands *bool
		Network                  struct {
			AllowedDomains  []string
			StrictAllowlist bool
		}
	}
}

// On Linux Claude Code runs in a box of its own, without its web, which passes the door; on macOS outside one, with its
// web and with rules that close deck's state.
func TestCommandAgentRunsInAutoModeInItsFolders(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	home := e.Home
	_ = os.WriteFile(filepath.Join(home, "deck.log"), []byte("deck listening url=http://127.0.0.1:7700/?token=x"), 0o600)
	e.Project("other", harness.PlainManifest)
	p := e.Project("notes", harness.PostManifest("http://127.0.0.1:1", ""))
	var id int64
	for id < 10 {
		id = e.Run(p, "work")
		if got := e.WaitState(id, "done", "error"); got.State != "done" {
			t.Fatalf("want done, got %+v", got)
		}
	}
	call := c.Call(c.Calls())
	for n := c.Calls(); slices.Contains(call.Args, "--resume"); n-- {
		call = c.Call(n - 1)
	}
	data := filepath.Join(deckhome.Layout(home).Work(), "projects", fmt.Sprint(p), "data")
	if mode, _ := harness.Flag(call.Args, "--permission-mode"); mode != "auto" {
		t.Fatalf("the agent runs in the auto mode of the CLI: %v", call.Args)
	}
	if off, _ := harness.Flag(call.Args, "--disallowedTools"); sandbox.Homed() != (off == "WebFetch,WebSearch") {
		t.Fatalf("the agent has the web of the CLI only outside a box of its own: %v", call.Args)
	}
	if dir, _ := harness.Flag(call.Args, "--add-dir"); dir != data || !strings.Contains(call.Stdin, data) {
		t.Fatalf("the agent writes the data folder of its project and knows where it is: %v", call.Args)
	}
	if st, err := os.Stat(data); err != nil || !st.IsDir() {
		t.Fatalf("the data folder exists before the agent starts: %v", err)
	}
	allowed, _ := harness.Flag(call.Args, "--allowedTools")
	for _, tool := range []string{"Bash", "Write", "Edit", "NotebookEdit", "WebFetch", "WebSearch"} {
		if slices.Contains(strings.Split(allowed, ","), tool) {
			t.Fatalf("%s is left to the auto mode of the CLI, not allowed outright: %s", tool, allowed)
		}
	}
	var set cliSettings
	if err := json.Unmarshal([]byte(call.Settings), &set); err != nil {
		t.Fatal(err)
	}
	if set.Sandbox != nil {
		t.Fatalf("the sandbox of the CLI is off: %s", call.Settings)
	}
	if sandbox.Homed() {
		if len(set.Permissions.Deny) > 0 || call.Env["CLAUDE_CONFIG_DIR"] == "" {
			t.Fatalf("in its box the CLI has its home of the project and no rules of deck's: %v %v", call.Args, set.Permissions.Deny)
		}
		return
	}
	real, _ := filepath.EvalSymlinks(home)
	for _, h := range []string{home, real} {
		mine := []string{filepath.Join(deckhome.Layout(h).Work(), "runs", fmt.Sprint(id), "work", "a.txt"), filepath.Join(deckhome.Layout(h).Work(), "projects", fmt.Sprint(p), "data", "index")}
		for _, path := range mine {
			if denied(set.Permissions.Deny, path) {
				t.Fatalf("the agent reads and writes its own folders: %s", path)
			}
		}
		theirs := []string{harness.DeckSecretPath(h), filepath.Join(deckhome.Layout(h).State(), "mcp", "mcp-1.json"), filepath.Join(deckhome.Layout(h).State(), "files", "3", "cv.pdf"),
			filepath.Join(deckhome.Layout(h).State(), "secrets", "svc"), filepath.Join(deckhome.Layout(h).State(), "deck.db-wal"), filepath.Join(deckhome.Layout(h).State(), "backups", "x"),
			filepath.Join(deckhome.Layout(h).State(), "mcp", "mcp-1.tmp"), filepath.Join(h, "deck.log")}
		// The one rule of its work is that it edits no file given with its messages, in its own input folder.
		given := func(r string) bool {
			for _, at := range []string{home, real} {
				if r == "Edit(/"+filepath.Join(deckhome.Layout(at).Work(), "runs", fmt.Sprint(id), "input")+"/**)" {
					return true
				}
			}
			return false
		}
		if slices.ContainsFunc(set.Permissions.Deny, func(r string) bool { return strings.Contains(r, deckhome.Layout(h).Work()) && !given(r) }) {
			t.Fatalf("the rules keep only deck's state from the agent, not masks of other runs and projects its shell passes anyway: %v", set.Permissions.Deny)
		}
		for _, path := range theirs {
			for _, tool := range []string{"Read", "Edit"} {
				if !slices.ContainsFunc(set.Permissions.Deny, func(r string) bool { return strings.HasPrefix(r, tool+"(") && denied([]string{r}, path) }) {
					t.Fatalf("%s of %s is denied to the agent: %v", tool, path, set.Permissions.Deny)
				}
			}
		}
	}
}

// selfCheckWith runs the self-check on the fake CLI, its first turn changed by before and its folder given the files.
func selfCheckWith(t *testing.T, before string, files []string, status string) string {
	c := harness.NewCLI(t)
	c.ChecksPass()
	if before != "" {
		b, _ := os.ReadFile(filepath.Join(c.Dir, "before"))
		c.Before(strings.Replace(string(b), `echo "$word" > word.txt`, before, 1))
	}
	for _, f := range files {
		if err := os.WriteFile(filepath.Join(c.Dir, f), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	e := harness.ClockCLIEnv(t, c)
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	return e.WaitHealth("provider:claude-code", status).Error
}

func TestSelfCheckIsRedWhenAutoModeStopsWorking(t *testing.T) {
	t.Parallel()
	self := harness.Fakes
	boxed := runtime.GOOS == "linux"
	for _, tc := range []struct {
		name, before string
		files        []string
		want         string
		boxed        bool
	}{
		{"green", "", nil, "", false},
		{"asks about a write in its folder", `echo "$word" > word.txt; DECK_FAKE_MCP=approve '` + self + `' "$url" Bash '{"command":"echo x > word.txt"}'`, nil, "auto mode: claude-code asked deck about a command", false},
		{"write did not run", ":", nil, "auto mode: the write in its own folder did not run", false},
		{"no question", `echo "$word" > word.txt; noask=1`, nil, "question: the answer through the ask tool did not reach", false},
		{"hidden file read", "", []string{"leak"}, "secrets: the shell of claude-code read a file deck hides", true},
		{"a host of no right", "", []string{"online"}, "turn without network: curl in the box of claude-code printed", true},
		{"secret leaked", `echo "$word" > word.txt; word=$(cat "$(dirname "$(grep -o '/[^ ]*/probe.sh' "$c/stdin" | head -1)")/../secrets/key.txt")`, nil, "the agent read a file deck hides", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.boxed && !boxed {
				t.Skip("Claude Code runs in a box of its own only on Linux")
			}
			if tc.name == "secret leaked" && boxed {
				t.Skip("in its box the CLI has no hidden file to read")
			}
			status := "error"
			if tc.want == "" {
				status = "ok"
			}
			got := selfCheckWith(t, tc.before, tc.files, status)
			if tc.want == "" && got != "" || !strings.Contains(got, tc.want) {
				t.Fatalf("self-check error %q, want %q", got, tc.want)
			}
		})
	}
}
