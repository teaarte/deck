package scenarios

import (
	"context"
	"deck2/internal/provider/claudecode"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const plainManifest = `{
  "version": 4,
  "collections": [],
  "commands": [{
    "name": "work", "title": "Work", "description": "Does work", "class": "internal",
    "executor": {"type": "agent", "prompt": "Do the work."}
  }],
  "triggers": [],
  "screens": []
}`

// fakeCLI is a folder with the fake Claude Code CLI and the streams it replays.
type fakeCLI struct{ fakeBin }

func newCLI(t *testing.T) *fakeCLI {
	t.Helper()
	dir := t.TempDir()
	b, err := os.ReadFile("testdata/claude/fake.sh")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claude"), b, 0o755); err != nil {
		t.Fatal(err)
	}
	return &fakeCLI{fakeBin{t: t, dir: dir, fixtures: "testdata/claude", version: "CLI", who: "the CLI"}}
}

// after sets shell code the fake runs once the stream is out; $c is the call folder, $n the call number.
func (c *fakeCLI) after(script string) {
	if err := os.WriteFile(filepath.Join(c.dir, "after"), []byte(script), 0o644); err != nil {
		c.t.Fatal(err)
	}
}

func (c *fakeCLI) skipStdin() {
	if err := os.WriteFile(filepath.Join(c.dir, "nostdin"), nil, 0o644); err != nil {
		c.t.Fatal(err)
	}
}

func (c *fakeCLI) adapter(home string) *claudecode.Adapter {
	return &claudecode.Adapter{Binary: filepath.Join(c.dir, "claude"), ConfigDir: filepath.Join(home, "mcp"), Linger: 300 * time.Millisecond}
}

type cliCall struct {
	settings     string
	settingsMode string
	args         []string
	env          map[string]string
	stdin        string
	cwd          string
	mcpMode      string
	mcp          string
}

func (c *fakeCLI) call(n int) cliCall {
	c.t.Helper()
	read := c.files(n)
	cc := cliCall{env: map[string]string{}, stdin: read("stdin"), cwd: strings.TrimSpace(read("cwd")),
		mcpMode: strings.TrimSpace(read("mcpmode")), mcp: read("mcpconfig"),
		settings: read("settings"), settingsMode: strings.TrimSpace(read("settingsmode"))}
	cc.args = strings.Split(strings.TrimSuffix(read("args"), "\n"), "\n")
	for _, l := range strings.Split(read("env"), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok && k != "" && !strings.ContainsAny(k, " \t") {
			cc.env[k] = v
		}
	}
	return cc
}

// flag returns the value after name in args and whether it was there.
func flag(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

func cliEnv(t *testing.T, c *fakeCLI) (*Env, string) {
	home := t.TempDir()
	return newEnvWith(t, home, c.adapter(home)), home
}

func (e *Env) session(id int64) string {
	p, err := e.App.Store.Q.GetProcess(context.Background(), id)
	if err != nil {
		e.T.Fatal(err)
	}
	return p.SessionID
}

func waitCalls(t *testing.T, c *fakeCLI, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for c.calls() < n {
		if time.Now().After(deadline) {
			t.Fatalf("the CLI was started %d times, want %d", c.calls(), n)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
