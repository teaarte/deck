package scenarios

import (
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/internal/provider/claudecode"
	"deck2/internal/provider/codex"
)

const liveManifest = `{"version": 6, "commands": [{"name": "wait", "title": "Wait", "description": "Waits for a code word", "class": "internal",
  "requires": ["sandbox"],
  "executor": {"type": "agent", "prompt": "Run the shell command sleep 20; echo waited with your shell tool in the foreground, not as a background task. When it finishes, reply with exactly the code word the owner gave you meanwhile, or NONE if the owner gave none. Do nothing else."}}]}`

// realLive starts the waiting command on a real provider, tells it the code word while it sleeps and reads the result.
func realLive(t *testing.T, pv provider.Provider, home string) {
	e := newEnvWith(t, home, pv, realWaits)
	p := e.createProject("live", liveManifest)
	id := e.run(p, "wait")
	deadline := time.Now().Add(2 * time.Minute)
	delivered := ""
	for delivered != "now" && time.Now().Before(deadline) {
		time.Sleep(4 * time.Second)
		if e.process(id).State != "running" {
			break
		}
		code, body := e.call("deck.process_tell", map[string]any{"process": id, "text": "The code word is OCTOPUS."})
		if code == 200 && strings.Contains(string(body), `"now"`) {
			delivered = "now"
		}
	}
	if delivered != "now" {
		t.Fatalf("the running turn did not take the message: %+v", e.process(id))
	}
	got := e.pollState(id, 4*time.Minute, time.Second, "done", "error")
	t.Logf("result: %s", got.Result)
	if got.State != "done" || !strings.Contains(strings.ToUpper(string(got.Result)), "OCTOPUS") {
		t.Fatalf("the turn answers with the word told in it: %+v", got)
	}
}

// TestRealClaudeCodeLiveMessage tells the installed Claude Code a code word on stdin while its turn runs.
func TestRealClaudeCodeLiveMessage(t *testing.T) {
	realCLI(t)
	home := t.TempDir()
	realLive(t, claudecode.New(home), home)
}

// TestRealCodexSteer tells the installed Codex a code word by turn/steer while its turn runs.
func TestRealCodexSteer(t *testing.T) {
	realCodex(t)
	home := t.TempDir()
	realLive(t, codex.New(home), home)
}
