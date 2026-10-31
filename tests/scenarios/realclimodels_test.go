package scenarios

import (
	"deck2/internal/provider"
	"deck2/internal/provider/claudecode"
	"deck2/internal/provider/codex"
	"deck2/tests/harness"
	"strings"
	"testing"
	"time"
)

// realModelResume runs plain.work on a model of a CLI, moves the model to another one, and sends the process back:
// the session resumes on the other model. It gives the response models of the two turns.
func realModelResume(t *testing.T, e *harness.Env, connection, name, first, second string) (string, string) {
	t.Helper()
	e.Must("deck.model_set", map[string]any{"connection": connection, "model": first, "name": name, "options": `{"effort": "low"}`}, nil)
	e.Must("deck.provider_check", map[string]any{"provider": name}, nil)
	h := checked(e, "provider:"+name)
	t.Logf("%s on %s low: self-check %s %s", name, first, h.Status, h.Error)
	if h.Status != "ok" {
		t.Fatalf("the self-check of %s on %s is green: %+v", name, first, h)
	}
	p := e.Project("plain", strings.Replace(harness.PlainManifest, "Do the work.", "Answer with the one word done. Do not ask anything and run no tool.", 1))
	e.UseModels("plain.work", name)
	id := e.Run(p, "work")
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the turn on %s is done: %+v", first, got)
	}
	e.Must("deck.model_set", map[string]any{"connection": connection, "model": second, "name": name}, nil)
	e.Must("deck.provider_check", map[string]any{"provider": name}, nil)
	if h := checked(e, "provider:"+name); h.Status != "ok" {
		t.Fatalf("the self-check of %s on %s is green: %+v", name, second, h)
	}
	e.Must("deck.process_rework", map[string]any{"process": id, "remark": "Say in one word that you are done."}, nil)
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the session resumes on %s: %+v", second, got)
	}
	var calls []map[string]any
	e.ModelCalls("", id, &calls)
	if len(calls) < 2 {
		t.Fatalf("two turns, two calls: %v", calls)
	}
	last, before := calls[0]["gen_ai.response.model"].(string), calls[len(calls)-1]["gen_ai.response.model"].(string)
	t.Logf("the first turn ran on %s, the resumed one on %s", before, last)
	return before, last
}

// checked waits for a health row out of its self-check, green or not.
func checked(e *harness.Env, subject string) harness.HealthRow {
	e.T.Helper()
	var got harness.HealthRow
	e.Until("the self-check of "+subject+" to end", func() bool {
		for _, h := range e.Health() {
			if h.Subject == subject && h.Status != "checking" {
				got = h
				return true
			}
		}
		return false
	})
	return got
}

// TestRealClaudeCodeModels: on the installed CLI, Haiku is refused for want of auto mode, Sonnet on low effort
// passes its self-check and works, and its session resumes on Opus.
func TestRealClaudeCodeModels(t *testing.T) {
	realCLI(t)
	home := t.TempDir()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(claudecode.New(home)), harness.WithConfig(realWaits), harness.WithWait(10*time.Minute))
	e.Refused("deck.model_set", map[string]any{"connection": "claude-code", "model": "haiku", "name": "cc-haiku"}, 422, "no auto mode")
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
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(pv), harness.WithConfig(realWaits), harness.WithWait(10*time.Minute))
	e.Refused("deck.model_set", map[string]any{"connection": "codex", "model": "gpt-6-luna", "name": "cx-x", "options": `{"effort": "minimal"}`}, 422, "takes effort")
	first, second := realModelResume(t, e, "codex", "cx-live", "gpt-6-luna", "gpt-6-sol")
	if first != "gpt-6-luna" || second != "gpt-6-sol" {
		t.Fatalf("each turn ran on its model: %s, %s", first, second)
	}
}
