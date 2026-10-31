package harness

import (
	"os"
	"path/filepath"
	"testing"
)

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
