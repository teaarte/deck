package scenarios

import (
	"context"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestADraftsScheduleSaysItDoesNotRun: a trigger of a draft has no next run and says why.
func TestADraftsScheduleSaysItDoesNotRun(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	draft := strings.Replace(harness.PlainManifest, `"triggers": []`, `"triggers": [{"command": "work", "cron": "30 * * * *"}]`, 1)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(draft), 0o600); err != nil {
		t.Fatal(err)
	}
	e.Must("deck.project_import", map[string]any{"folder": dir, "name": "bench"}, nil)
	var ts []struct {
		Project   string
		NextRunAt string `json:"next_at"`
		Draft     bool
	}
	e.Must("deck.triggers_list", map[string]any{"project": "bench"}, &ts)
	if len(ts) != 1 || !ts[0].Draft || ts[0].NextRunAt != "" {
		t.Fatalf("a draft's trigger shows no next run: %+v", ts)
	}
}

// TestALineOfMediaSaysWhyItHasNoFileInWords: an expression of a line that fails names itself and the fault,
// without the drawing of where in it.
func TestALineOfMediaSaysWhyItHasNoFileInWords(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("btc", strings.Replace(forecastManifest, `"item": {"file": "file", "title": "title"}`, `"item": {"file": "int(file)", "title": "title"}`, 1))
	e.Must("deck.records_upsert", map[string]any{"project": "btc", "collection": "attachments",
		"records": harness.Jsonl(map[string]any{"title": "a", "file": "image-1b946491.png"})}, nil)
	raw, _ := json.Marshal(e.ScreenMessages("btc", "files", nil))
	if s := string(raw); !strings.Contains(s, `the file of this line: int(file): `) || strings.Contains(s, `| ^`) || strings.Contains(s, `(1:1)`) {
		t.Fatalf("the line says in words why it has no file: %s", s)
	}
}

// TestTheScreenOfAScriptShowsItsInputAndResult: a script has no log of its own, so its screen shows what it got and
// gave, and says why the log is empty.
func TestTheScreenOfAScriptShowsItsInputAndResult(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("tools", harness.ScriptManifest("run", "", ""))
	e.SaveScript("tools", "scripts/main.py", `import json, sys
print(json.dumps({"added": 7}))
`)
	id := e.Start("tools", "run", map[string]any{"n": 3})
	e.WaitState(id, "done")
	raw, _ := json.Marshal(e.ScreenMessages("", "process", map[string]any{"record": id}))
	s := string(raw)
	if !strings.Contains(s, `\"n\": 3`) || !strings.Contains(s, `\"added\": 7`) || !strings.Contains(s, "Nothing in the log") {
		t.Fatalf("the screen shows the input, the result and why the log is empty: %s", s)
	}
}

// TestAThoughtWithoutTextIsNoLineOfTheLog: a thinking block a model keeps to itself reaches the log as nothing,
// not as an empty line.
func TestAThoughtWithoutTextIsNoLineOfTheLog(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		c.Req.OnEvent(provider.Event{Kind: provider.EventThought, Text: ""})
		c.Req.OnEvent(provider.Event{Kind: provider.EventThought, Text: "Checking the dates."})
		c.Req.OnEvent(provider.Event{Kind: provider.EventMessage, Text: "  "})
		return provider.Result{Text: "ok"}
	}))

	id := e.Run(e.Project("plain", harness.PlainManifest), "work")
	e.WaitState(id, "done", "error")
	var lines []struct{ Kind, Text string }
	e.Must("deck.process_log", map[string]any{"process": id}, &lines)
	for _, l := range lines {
		if strings.TrimSpace(l.Text) == "" && (l.Kind == "thought" || l.Kind == "message") {
			t.Fatalf("an empty thought or message is no line: %+v", lines)
		}
	}
	if len(lines) == 0 {
		t.Fatal("the thought with text is a line")
	}
}
