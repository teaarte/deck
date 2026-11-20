package owner

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
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

// TestTheOwnerCallsEveryCommandOfTheSnapshotThroughTheOneDoor: the owner's call of each command of the snapshot goes
// the stages of every call: an input not an object is refused by its schema, nothing done, no line in the journal.
func TestTheOwnerCallsEveryCommandOfTheSnapshotThroughTheOneDoor(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	raw, err := os.ReadFile(filepath.Join(harness.Root, "tests", "surface", "testdata", "surface.json"))
	if err != nil {
		t.Fatal(err)
	}
	var snap struct {
		Commands []struct {
			Name    string
			Callers []string
		}
	}
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatal(err)
	}
	_, tok := e.Token("terminal", "deck.*")
	lines := func() (n int) {
		// The count of the journal's lines, which no read gives whole.
		_ = e.SQLRow(`SELECT count(*) FROM tool_call`).Scan(&n)
		return n
	}
	before, n := lines(), 0
	var masters []string
	for _, c := range snap.Commands {
		if !strings.HasPrefix(c.Name, "deck.") || !slices.Contains(c.Callers, "owner") {
			continue
		}
		n++
		code, body := e.Call(c.Name, []any{})
		if want := "input of " + c.Name; code != 422 || !strings.Contains(string(body), want) {
			t.Errorf("the owner's %s: %d %s, want 422 %q", c.Name, code, body, want)
		}
		if slices.Contains(c.Callers, "master") {
			masters = append(masters, c.Name)
		}
	}
	if got := lines(); got != before {
		t.Fatalf("the owner's refused calls write no line, as before: %d lines, were %d", got, before)
	}
	for _, name := range masters {
		if code, body := e.As(tok).Call(name, []any{}); code != 422 && code != 403 {
			t.Errorf("a token's %s: %d %s", name, code, body)
		}
	}
	if n < 100 {
		t.Fatalf("the snapshot names the commands of deck the owner calls: %d", n)
	}
	if lines() == before {
		t.Fatal("a token's refused calls are in the journal")
	}
}
