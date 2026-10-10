package scenarios

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"deck2/internal/provider"
)

// TestADraftsScheduleSaysItDoesNotRun: a trigger of a draft has no next run and says why.
func TestADraftsScheduleSaysItDoesNotRun(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	draft := strings.Replace(plainManifest, `"triggers": []`, `"triggers": [{"command": "work", "cron": "30 * * * *"}]`, 1)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(draft), 0o600); err != nil {
		t.Fatal(err)
	}
	e.must("deck.project_import", map[string]any{"folder": dir, "name": "bench"}, nil)
	var ts []struct {
		Project   string
		NextRunAt string `json:"next_run_at"`
		Draft     bool
	}
	e.must("deck.triggers_list", map[string]any{"project": "bench"}, &ts)
	if len(ts) != 1 || !ts[0].Draft || ts[0].NextRunAt != "" {
		t.Fatalf("a draft's trigger shows no next run: %+v", ts)
	}
}

// TestANoteOfAScriptIsSignedByTheScript: a note a script writes is signed as a script, not an agent.
func TestANoteOfAScriptIsSignedByTheScript(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("tools", scriptManifest("run", "", ""))
	e.saveScript("tools", "scripts/main.py", pythonLib+`
code, out = deck("deck.notify", {"text": "The report is ready"})
print(json.dumps({"code": code}))
`)
	id := e.start("tools", "run", nil)
	if got := e.waitScript(id, "done", "error"); got.State != "done" || !strings.Contains(string(got.Result), "200") {
		t.Fatalf("the script wrote its note: %+v", got)
	}
	var author string
	_ = e.App.Store.DB.QueryRow(`SELECT author FROM feed WHERE text = 'The report is ready'`).Scan(&author)
	if author != "script:"+itoa(id) {
		t.Fatalf("the note is signed by the script: %q", author)
	}
}

// TestALineOfMediaSaysWhyItHasNoFileInWords: an expression of a line that fails names itself and the fault,
// without the drawing of where in it.
func TestALineOfMediaSaysWhyItHasNoFileInWords(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("btc", strings.Replace(forecastManifest, `"item": {"file": "file", "title": "title"}`, `"item": {"file": "int(file)", "title": "title"}`, 1))
	e.must("deck.records_upsert", map[string]any{"project": "btc", "collection": "attachments",
		"records": jsonl(map[string]any{"title": "a", "file": "image-1b946491.png"})}, nil)
	raw, _ := json.Marshal(e.screenMessages("btc", "files", nil))
	if s := string(raw); !strings.Contains(s, `the file of this line: int(file): `) || strings.Contains(s, `| ^`) || strings.Contains(s, `(1:1)`) {
		t.Fatalf("the line says in words why it has no file: %s", s)
	}
}

// TestTheScreenOfAScriptShowsItsInputAndResult: a script has no log of its own, so its screen shows what it got and
// gave, and says why the log is empty.
func TestTheScreenOfAScriptShowsItsInputAndResult(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("tools", scriptManifest("run", "", ""))
	e.saveScript("tools", "scripts/main.py", `import json, sys
print(json.dumps({"added": 7}))
`)
	id := e.start("tools", "run", map[string]any{"n": 3})
	e.waitScript(id, "done")
	raw, _ := json.Marshal(e.screenMessages("", "process", map[string]any{"record": id}))
	s := string(raw)
	if !strings.Contains(s, `\"n\": 3`) || !strings.Contains(s, `\"added\": 7`) || !strings.Contains(s, "Nothing in the log") {
		t.Fatalf("the screen shows the input, the result and why the log is empty: %s", s)
	}
}

// TestAThoughtWithoutTextIsNoLineOfTheLog: a thinking block a model keeps to itself reaches the log as nothing,
// not as an empty line.
func TestAThoughtWithoutTextIsNoLineOfTheLog(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		c.Req.OnEvent(provider.Event{Kind: provider.EventThought, Text: ""})
		c.Req.OnEvent(provider.Event{Kind: provider.EventThought, Text: "Checking the dates."})
		c.Req.OnEvent(provider.Event{Kind: provider.EventMessage, Text: "  "})
		return provider.Result{Text: "ok"}
	})
	id := e.run(e.createProject("plain", plainManifest), "work")
	e.waitState(id, "done", "error")
	var lines []struct{ Kind, Text string }
	e.must("deck.process_log", map[string]any{"process": id}, &lines)
	for _, l := range lines {
		if strings.TrimSpace(l.Text) == "" && (l.Kind == "thought" || l.Kind == "message") {
			t.Fatalf("an empty thought or message is no line: %+v", lines)
		}
	}
	if len(lines) == 0 {
		t.Fatal("the thought with text is a line")
	}
}
