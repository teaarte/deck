package owner

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// TestEveryCommandOfDeckIsCalledByAScenario: every command of deck is named by a scenario test, so a change that
// breaks one does not pass unseen.
func TestEveryCommandOfDeckIsCalledByAScenario(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	var cmds []struct{ Name string }
	e.Must("deck.commands_list", nil, &cmds)
	var src strings.Builder
	err := filepath.WalkDir(filepath.Join(harness.Root, "tests", "scenarios"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, "_test.go") {
			return err
		}
		b, err := os.ReadFile(p)
		src.Write(b)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, c := range cmds {
		name, ok := strings.CutPrefix(c.Name, "deck.")
		if !ok {
			continue
		}
		n++
		if !strings.Contains(src.String(), `"`+c.Name+`"`) && !strings.Contains(src.String(), `"`+name+`"`) {
			t.Errorf("no scenario calls %s", c.Name)
		}
	}
	if n < 100 {
		t.Fatalf("the owner lists every command of deck: %d", n)
	}
}
