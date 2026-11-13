package modules

import (
	"testing"

	"deck2/tests/harness"
)

// TestEnablingGivesInAdvanceOnlyWhatTakesNoPasskey: enabling gives always to named hosts, files and deck's records of
// models; the login, money, the whole internet and another module's data take a passkey, so they wait.
func TestEnablingGivesInAdvanceOnlyWhatTakesNoPasskey(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("diary", harness.PlainManifest)
	m := harness.Declare(harness.PlainManifest,
		`{"right": "network", "what": ["api.example.com", "*"], "why": "reads the news"}`,
		`{"right": "files", "what": ["gpu"], "why": "draws"}`,
		`{"right": "money", "what": ["@text"], "why": "summarises"}`,
		`{"right": "data", "what": ["diary", "*:models"], "why": "compares"}`,
		`{"right": "login", "what": ["codex"], "why": "its agent works on your login"}`)
	e.Must("deck.project_create", map[string]any{"name": "news", "manifest": m}, nil)
	type row struct {
		answer string
		key    bool
	}
	want := map[string]row{"network api.example.com": {"always", false}, "network *": {"", true}, "files gpu": {"always", false},
		"money @text": {"", true}, "data diary": {"", true}, "data *:models": {"always", false}, "login codex": {"", true}}
	for _, r := range rightsOf(e, "news") {
		w, ok := want[r.Right+" "+r.Subject]
		if !ok {
			continue
		}
		delete(want, r.Right+" "+r.Subject)
		if r.Answer != w.answer || r.Key != w.key {
			t.Fatalf("%s %s: answer %q, takes a passkey %v after enabling, want %q, %v", r.Right, r.Subject, r.Answer, r.Key, w.answer, w.key)
		}
	}
	if len(want) != 0 {
		t.Fatalf("rights not listed: %v", want)
	}
}
