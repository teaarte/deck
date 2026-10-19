package scenarios

import (
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/internal/provider/claudecode"
	"deck2/internal/provider/codex"
)

// realModelResume runs plain.work on a model of a CLI, moves the model to another one, and sends the process back:
// the session resumes on the other model. It gives the response models of the two turns.
func realModelResume(t *testing.T, e *Env, connection, name, first, second string) (string, string) {
	t.Helper()
	e.must("deck.model_set", map[string]any{"connection": connection, "model": first, "name": name, "options": `{"effort": "low"}`}, nil)
	h := e.waitHealthLong("provider:"+name, 10*time.Minute)
	t.Logf("%s on %s low: self-check %s %s", name, first, h.Status, h.Error)
	if h.Status != "ok" {
		t.Fatalf("the self-check of %s on %s is green: %+v", name, first, h)
	}
	p := e.createProject("plain", strings.Replace(plainManifest, "Do the work.", "Answer with the one word done. Do not ask anything and run no tool.", 1))
	e.useModels("plain.work", name)
	id := e.run(p, "work")
	if got := e.pollState(id, 10*time.Minute, time.Second, "done", "error"); got.State != "done" {
		t.Fatalf("the turn on %s is done: %+v", first, got)
	}
	e.must("deck.model_set", map[string]any{"connection": connection, "model": second, "name": name}, nil)
	if h := e.waitHealthLong("provider:"+name, 10*time.Minute); h.Status != "ok" {
		t.Fatalf("the self-check of %s on %s is green: %+v", name, second, h)
	}
	e.must("deck.process_rework", map[string]any{"process": id, "remark": "Say in one word that you are done."}, nil)
	if got := e.pollState(id, 10*time.Minute, time.Second, "done", "error"); got.State != "done" {
		t.Fatalf("the session resumes on %s: %+v", second, got)
	}
	var calls []map[string]any
	e.modelCalls("", id, &calls)
	if len(calls) < 2 {
		t.Fatalf("two turns, two calls: %v", calls)
	}
	last, before := calls[0]["gen_ai.response.model"].(string), calls[len(calls)-1]["gen_ai.response.model"].(string)
	t.Logf("the first turn ran on %s, the resumed one on %s", before, last)
	return before, last
}

// waitHealthLong waits for a health row out of its self-check, green or not.
func (e *Env) waitHealthLong(subject string, wait time.Duration) healthRow {
	e.T.Helper()
	deadline := time.Now().Add(wait)
	for time.Now().Before(deadline) {
		var hs []healthRow
		e.must("deck.health_list", nil, &hs)
		for _, h := range hs {
			if h.Subject == subject && h.Status != "checking" {
				return h
			}
		}
		time.Sleep(time.Second)
	}
	e.T.Fatalf("the self-check of %s did not end", subject)
	return healthRow{}
}

// TestRealClaudeCodeModels: on the installed CLI, Haiku is refused for want of auto mode, Sonnet on low effort
// passes its self-check and works, and its session resumes on Opus.
func TestRealClaudeCodeModels(t *testing.T) {
	realCLI(t)
	home := t.TempDir()
	e := newEnvWith(t, home, claudecode.New(home), realWaits)
	e.refused("deck.model_set", map[string]any{"connection": "claude-code", "model": "haiku", "name": "cc-haiku"}, 422, "no auto mode")
	first, second := realModelResume(t, e, "claude-code", "cc-live", "sonnet", "opus")
	if !strings.Contains(first, "sonnet") || !strings.Contains(second, "opus") {
		t.Fatalf("each turn ran on its model: %s, %s", first, second)
	}
}

// TestRealCodexModels: on the installed Codex, a fast model on low effort passes its self-check and works, and its
// thread resumes on another model.
func TestRealCodexModels(t *testing.T) {
	realCodex(t)
	home := t.TempDir()
	var pv provider.Provider = codex.New(home)
	e := newEnvWith(t, home, pv, realWaits)
	e.refused("deck.model_set", map[string]any{"connection": "codex", "model": "gpt-6-luna", "name": "cx-x", "options": `{"effort": "minimal"}`}, 422, "takes effort")
	first, second := realModelResume(t, e, "codex", "cx-live", "gpt-6-luna", "gpt-6-sol")
	if first != "gpt-6-luna" || second != "gpt-6-sol" {
		t.Fatalf("each turn ran on its model: %s, %s", first, second)
	}
}
