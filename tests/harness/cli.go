package harness

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/provider/claudecode"
)

// FakeCLI is a folder with the fake Claude Code CLI and the streams it replays.
type FakeCLI struct{ fakeBin }

func NewCLI(t *testing.T) *FakeCLI {
	t.Helper()
	dir := t.TempDir()
	b, err := os.ReadFile(Testdata("claude/fake.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "claude"), b, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if b, err := os.ReadFile(filepath.Join(dir, "failed")); err == nil {
			t.Errorf("a helper of the fake CLI failed: %s", b)
		}
	})
	return &FakeCLI{fakeBin{T: t, Dir: dir, fixtures: Testdata("claude"), version: "CLI", who: "the CLI"}}
}

// After sets shell code the fake runs once the stream is out; $c is the call folder, $n the call number.
func (c *FakeCLI) After(script string) {
	if err := os.WriteFile(filepath.Join(c.Dir, "after"), []byte(script), 0o644); err != nil {
		c.T.Fatal(err)
	}
}

func (c *FakeCLI) SkipStdin() {
	if err := os.WriteFile(filepath.Join(c.Dir, "nostdin"), nil, 0o644); err != nil {
		c.T.Fatal(err)
	}
}

func (c *FakeCLI) Adapter(home string) *claudecode.Adapter {
	return &claudecode.Adapter{Binary: filepath.Join(c.Dir, "claude"), ConfigDir: deckhome.Layout(home).Sessions(), Linger: 300 * time.Millisecond, TaskLinger: 10 * time.Second}
}

type cliCall struct {
	Settings     string
	SettingsMode string
	Args         []string
	Env          map[string]string
	Stdin        string
	Cwd          string
	McpMode      string
	Mcp          string
}

func (c *FakeCLI) Call(n int) cliCall {
	c.T.Helper()
	read := c.Files(n)
	cc := cliCall{Env: map[string]string{}, Stdin: read("stdin"), Cwd: strings.TrimSpace(read("cwd")),
		McpMode: strings.TrimSpace(read("mcpmode")), Mcp: read("mcpconfig"),
		Settings: read("settings"), SettingsMode: strings.TrimSpace(read("settingsmode"))}
	cc.Args = strings.Split(strings.TrimSuffix(read("args"), "\n"), "\n")
	for _, l := range strings.Split(read("env"), "\n") {
		if k, v, ok := strings.Cut(l, "="); ok && k != "" && !strings.ContainsAny(k, " \t") {
			cc.Env[k] = v
		}
	}
	return cc
}

// Flag returns the value after name in args and whether it was there.
func Flag(args []string, name string) (string, bool) {
	for i, a := range args {
		if a == name && i+1 < len(args) {
			return args[i+1], true
		}
	}
	return "", false
}

// WithCLI runs deck on the fake Claude Code CLI.
func WithCLI(c *FakeCLI) Option {
	return func(s *setup) {
		s.providers = append(s.providers, func(home string) provider.Provider { return c.Adapter(home) })
	}
}

func (e *Env) Session(id int64) string {
	e.T.Helper()
	var p struct {
		SessionID string `json:"session_id"`
	}
	e.Must("deck.process_get", map[string]any{"process": id}, &p)
	return p.SessionID
}

// Models sets what the fake CLI lists as its models, from a recorded answer to initialize.
func (c *FakeCLI) Models(fixture string) {
	b, err := os.ReadFile(filepath.Join(c.fixtures, fixture))
	if err != nil {
		c.T.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.Dir, "models"), b, 0o644); err != nil {
		c.T.Fatal(err)
	}
}

// ChecksPass makes every self-check of the fake CLI pass however its calls are numbered: the CLI acts as in auto
// mode and replays the recorded self-check turn and its resume.
func (c *FakeCLI) ChecksPass() {
	c.Approving()
	first := Testdata("claude/selfcheck.jsonl")
	resume := Testdata("claude/selfcheck_resume.jsonl")
	b, _ := os.ReadFile(filepath.Join(c.Dir, "before"))
	c.Before(string(b) + `
if grep -q 'deck-check-' "$c/stdin"; then stream='` + first + `'; fi
if grep -q 'Give the same word and answer again' "$c/stdin"; then stream='` + resume + `'; fi`)
}

// Approving makes the fake CLI act as the real one does in auto mode on the self-check: the write in its folder
// runs without asking, only the question reaches deck's permission tool, and curl fails without network.
func (c *FakeCLI) Approving() {
	self := Fakes
	offline := Testdata("claude/selfcheck_offline.jsonl")
	c.Before(`if grep -q -- --tools "$c/args" && grep -q 'self-check of deck' "$c/stdin"; then
  sed "s/{{session}}/$session/g" '` + offline + `'
  exit 0
fi
if [ -n "$(grep -o 'deck-check-' "$c/stdin")" ] && ! grep -q -- --resume "$c/args"; then
  echo "$word" > word.txt
  url=$(sed -n 's/.*"url":"\([^"]*\)".*/\1/p' "$c/mcpconfig")
  DECK_FAKE_MCP=approve '` + self + `' "$url" AskUserQuestion '{"questions":[{"question":"Which answer should the result carry?"}]}'
fi`)
}

func (c *FakeCLI) Before(script string) {
	if err := os.WriteFile(filepath.Join(c.Dir, "before"), []byte(script), 0o644); err != nil {
		c.T.Fatal(err)
	}
}

func ClockCLIEnv(t *testing.T, c *FakeCLI) *Env {
	home := t.TempDir()
	clock := &Clock{At: T0}
	e := New(t, WithHome(home), WithProvider(c.Adapter(home)), WithClock(clock))
	return e
}
