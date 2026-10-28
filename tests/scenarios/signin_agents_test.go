package scenarios

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"deck2/internal/registry"
)

type signInRow struct {
	Provider, URL, Code, Error string
	Paste                      bool
}

func (e *Env) signIns() []signInRow {
	e.T.Helper()
	var rows []signInRow
	e.must("deck.agent_logins", map[string]any{"lang": "ru"}, &rows)
	return rows
}

// TestTheOwnerSignsCodexInFromDeck: the device link and code Codex prints reach the owner in deck, the sign-in ends
// once approved on the page, and the self-check runs then; only the owner signs in.
func TestTheOwnerSignsCodexInFromDeck(t *testing.T) {
	t.Parallel()
	c := newCodex(t)
	e, _ := codexEnv(t, c)
	if _, err := e.App.Registry.Invoke(t.Context(), registry.Actor{Kind: registry.Master, Workspace: 1}, "deck.agent_login", json.RawMessage(`{"provider":"codex"}`)); err == nil {
		t.Fatal("the master does not sign deck's agents in")
	}
	e.refused("deck.agent_login", map[string]any{"provider": "nobody"}, 422, "not a CLI agent")
	var l signInRow
	e.must("deck.agent_login", map[string]any{"provider": "codex"}, &l)
	if l.URL != "https://auth.openai.com/codex/device" || l.Code != "N3NL-OVDO2" || l.Paste {
		t.Fatalf("the link and the code of the device sign-in: %+v", l)
	}
	if rows := e.signIns(); len(rows) != 1 || rows[0].Code != l.Code {
		t.Fatalf("the waiting sign-in is listed: %+v", rows)
	}
	var again signInRow
	e.must("deck.agent_login", map[string]any{"provider": "codex"}, &again)
	if again != l {
		t.Fatalf("a second press gives the sign-in that waits: %+v", again)
	}
	before := c.calls()
	c.stream(before+1, "selfcheck.jsonl")
	c.stream(before+2, "selfcheck_resume.jsonl")
	c.stream(before+3, "selfcheck_offline.jsonl")
	if err := os.WriteFile(filepath.Join(c.dir, "approve"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e.waitWithin("the sign-in to end", 10*time.Second, func() bool { return len(e.signIns()) == 0 })
	e.waitHealth("provider:codex", "ok")
}

// TestTheOwnerSignsClaudeCodeInWithTheCodeOfThePage: Claude Code waits for the code its page gives; a wrong one ends
// the sign-in with its reason, and the right one signs it in.
func TestTheOwnerSignsClaudeCodeInWithTheCodeOfThePage(t *testing.T) {
	t.Parallel()
	c := newCLI(t)
	c.checksPass()
	e, _ := cliEnv(t, c)
	e.refused("deck.agent_login_code", map[string]any{"provider": "claude-code", "code": "x"}, 422, "no sign-in")
	var l signInRow
	e.must("deck.agent_login", map[string]any{"provider": "claude-code"}, &l)
	if !l.Paste || l.Code != "" || l.URL == "" {
		t.Fatalf("the sign-in waits for the code of the page: %+v", l)
	}
	e.must("deck.agent_login_code", map[string]any{"provider": "claude-code", "code": "bad"}, nil)
	e.waitWithin("the wrong code to fail", 10*time.Second, func() bool {
		rows := e.signIns()
		return len(rows) == 1 && rows[0].Error != ""
	})
	if rows := e.signIns(); rows[0].Error[:len("вход в claude-code не удался")] != "вход в claude-code не удался" {
		t.Fatalf("the failure says why, in the owner's language: %+v", rows)
	}
	e.must("deck.agent_login", map[string]any{"provider": "claude-code"}, &l)
	e.must("deck.agent_login_code", map[string]any{"provider": "claude-code", "code": "good-code"}, nil)
	e.waitWithin("the sign-in to end", 10*time.Second, func() bool { return len(e.signIns()) == 0 })
	if _, err := os.Stat(filepath.Join(c.dir, "signed-in")); err != nil {
		t.Fatal("the code reached the CLI")
	}
	e.waitHealth("provider:claude-code", "ok")
}
