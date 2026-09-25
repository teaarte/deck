package scenarios

import (
	"context"
	"deck2/internal/provider/claudecode"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
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
type fakeCLI struct {
	t   *testing.T
	dir string
}

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
	return &fakeCLI{t: t, dir: dir}
}

// stream sets what call n replays; n = 0 sets it for every call without its own stream.
func (c *fakeCLI) stream(n int, fixture string) {
	var meta map[string]struct{ CLI, Date, Kind string }
	b, _ := os.ReadFile("testdata/claude/fixtures.json")
	if err := json.Unmarshal(b, &meta); err != nil {
		c.t.Fatal(err)
	}
	if m := meta[fixture]; m.CLI == "" || m.Date == "" || (m.Kind != "real" && m.Kind != "synthetic") {
		c.t.Fatalf("fixture %s needs its CLI version, date and kind in fixtures.json", fixture)
	}
	b, err := os.ReadFile(filepath.Join("testdata/claude", fixture))
	if err != nil {
		c.t.Fatal(err)
	}
	c.streamText(n, string(b))
}

func (c *fakeCLI) streamText(n int, text string) {
	name := "stream"
	if n > 0 {
		name += "." + strconv.Itoa(n)
	}
	if err := os.WriteFile(filepath.Join(c.dir, name), []byte(text), 0o644); err != nil {
		c.t.Fatal(err)
	}
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
	d := filepath.Join(c.dir, "calls", strconv.Itoa(n))
	read := func(name string) string {
		b, _ := os.ReadFile(filepath.Join(d, name))
		return string(b)
	}
	if _, err := os.Stat(d); err != nil {
		c.t.Fatalf("the CLI was not started %d times", n)
	}
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

func (c *fakeCLI) calls() int {
	es, _ := os.ReadDir(filepath.Join(c.dir, "calls"))
	return len(es)
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
