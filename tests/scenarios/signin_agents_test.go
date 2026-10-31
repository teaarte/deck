package scenarios

import (
	"deck2/internal/registry"
	"deck2/tests/harness"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func signIns(e *harness.Env) []harness.SignInRow {
	e.T.Helper()
	var rows []harness.SignInRow
	e.Must("deck.agent_logins", map[string]any{"lang": "ru"}, &rows)
	return rows
}

// TestTheOwnerSignsCodexInFromDeck: the device link and code Codex prints reach the owner in deck, the sign-in ends
// once approved on the page, and the self-check runs then; only the owner signs in.
func TestTheOwnerSignsCodexInFromDeck(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	e := harness.New(t, harness.WithCodex(c))
	if _, err := e.App.Registry.Invoke(t.Context(), registry.Actor{Kind: registry.Master, Workspace: 1}, "deck.agent_login", json.RawMessage(`{"provider":"codex"}`)); err == nil {
		t.Fatal("the master does not sign deck's agents in")
	}
	e.Refused("deck.agent_login", map[string]any{"provider": "nobody"}, 422, "not a CLI agent")
	var l harness.SignInRow
	e.Must("deck.agent_login", map[string]any{"provider": "codex"}, &l)
	if l.URL != "https://auth.openai.com/codex/device" || l.Code != "N3NL-OVDO2" || l.Paste {
		t.Fatalf("the link and the code of the device sign-in: %+v", l)
	}
	if rows := signIns(e); len(rows) != 1 || rows[0].Code != l.Code {
		t.Fatalf("the waiting sign-in is listed: %+v", rows)
	}
	var again harness.SignInRow
	e.Must("deck.agent_login", map[string]any{"provider": "codex"}, &again)
	if again != l {
		t.Fatalf("a second press gives the sign-in that waits: %+v", again)
	}
	before := c.Calls()
	c.Stream(before+1, "selfcheck.jsonl")
	c.Stream(before+2, "selfcheck_resume.jsonl")
	c.Stream(before+3, "selfcheck_offline.jsonl")
	if err := os.WriteFile(filepath.Join(c.Dir, "approve"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e.Until("the sign-in to end", func() bool { return len(signIns(e)) == 0 })
	e.WaitHealth("provider:codex", "ok")
}

// TestTheOwnerSignsClaudeCodeInWithTheCodeOfThePage: Claude Code waits for the code its page gives; a wrong one ends
// the sign-in with its reason, and the right one signs it in.
func TestTheOwnerSignsClaudeCodeInWithTheCodeOfThePage(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.ChecksPass()
	e := harness.New(t, harness.WithCLI(c))
	e.Refused("deck.agent_login_code", map[string]any{"provider": "claude-code", "code": "x"}, 422, "no sign-in")
	var l harness.SignInRow
	e.Must("deck.agent_login", map[string]any{"provider": "claude-code"}, &l)
	if !l.Paste || l.Code != "" || l.URL == "" {
		t.Fatalf("the sign-in waits for the code of the page: %+v", l)
	}
	e.Must("deck.agent_login_code", map[string]any{"provider": "claude-code", "code": "bad"}, nil)
	e.Until("the wrong code to fail", func() bool {
		rows := signIns(e)
		return len(rows) == 1 && rows[0].Error != ""
	})
	if rows := signIns(e); rows[0].Error[:len("вход в claude-code не удался")] != "вход в claude-code не удался" {
		t.Fatalf("the failure says why, in the owner's language: %+v", rows)
	}
	e.Must("deck.agent_login", map[string]any{"provider": "claude-code"}, &l)
	e.Must("deck.agent_login_code", map[string]any{"provider": "claude-code", "code": "good-code"}, nil)
	e.Until("the sign-in to end", func() bool { return len(signIns(e)) == 0 })
	if _, err := os.Stat(filepath.Join(c.Dir, "signed-in")); err != nil {
		t.Fatal("the code reached the CLI")
	}
	e.WaitHealth("provider:claude-code", "ok")
}
