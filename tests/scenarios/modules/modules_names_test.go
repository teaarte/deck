package modules_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// TestATrialTakesNoReservedFieldIntoItsInput: the retry key and the calls a press allows are the registry's, so a
// trial that names them starts with neither in its input.
func TestATrialTakesNoReservedFieldIntoItsInput(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("ideas", strings.Replace(harness.IdeasManifest, `"executor": {`,
		`"input": {"type": "object", "properties": {"topic": {"type": "string"}}}, "executor": {`, 1))
	var r struct{ Process int64 }
	e.Must("deck.command_try", map[string]any{"command": "ideas.collect",
		"input": `{"topic": "go", "retry_key": "k", "allows": "[]"}`, "live": true}, &r)
	var p struct{ Input json.RawMessage }
	e.Must("deck.process_get", map[string]any{"process": r.Process}, &p)
	if !strings.Contains(string(p.Input), `"topic"`) || strings.Contains(string(p.Input), "retry_key") || strings.Contains(string(p.Input), "allows") {
		t.Fatalf("the trial's input is the command's own: %s", p.Input)
	}
}

// TestEveryCommandOfDeckItsTextsNameIsOne: a name of deck's command in the code, the client, the texts deck ships
// and its dictionary names a command deck has, so a renamed one leaves no old name behind.
func TestEveryCommandOfDeckItsTextsNameIsOne(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	// Types of a failure, files of deck, a block of the client and deck's name on its machine, not commands.
	others := []string{"deck.tools", "deck.internal", "deck.restart", "deck.stopped", "deck.db", "deck.lock", "deck.app", "deck.unknown", "deck.localhost"}
	name := regexp.MustCompile(`\bdeck\.[a-z][a-z0-9_]*\b[^(.]`)
	seen := map[string]string{}
	for _, dir := range []string{"internal", "cmd", "master", "skills", "starters", "schemas", "web/src", "web/foreign"} {
		_ = filepath.WalkDir(filepath.Join(harness.Root, dir), func(p string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() || strings.HasSuffix(p, "_test.go") || !slices.Contains([]string{".go", ".md", ".json", ".py", ".ts", ".tsx", ".js", ".sh"}, filepath.Ext(p)) {
				return err
			}
			b, _ := os.ReadFile(p)
			for _, m := range name.FindAllString(string(b), -1) {
				if n := m[:len(m)-1]; !slices.Contains(others, n) {
					seen[n] = p
				}
			}
			return nil
		})
	}
	if len(seen) < 50 {
		t.Fatalf("the texts name deck's commands: %d found", len(seen))
	}
	for n, p := range seen {
		if code, out := e.Call("deck.command_describe", map[string]any{"command": n}); code == 404 {
			t.Errorf("%s names %s, which is no command of deck: %s", p, n, out)
		}
	}
}

// TestACommandOfDeckIsNamedByOneThing: deck's command is <entity>_<verb>; a plural names only a list.
func TestACommandOfDeckIsNamedByOneThing(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	// Singular words that end in s.
	singular := []string{"process", "address", "access", "status", "data", "readiness"}
	for _, c := range e.Commands() {
		name, ok := strings.CutPrefix(c.Name, "deck.")
		if !ok {
			continue
		}
		words := strings.Split(name, "_")
		if len(words) < 2 {
			t.Errorf("%s names no verb", c.Name)
		}
		for _, w := range words[:len(words)-1] {
			if strings.HasSuffix(w, "s") && !slices.Contains(singular, w) && words[len(words)-1] != "list" {
				t.Errorf("%s: a plural, %s, names only a list", c.Name, w)
			}
		}
	}
}
