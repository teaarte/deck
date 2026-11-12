package modules

import (
	"testing"

	"deck2/tests/harness"
)

// TestEnablingGivesInAdvanceOnlyWhatIsNotAskedAtUse: enabling a module gives always to the network of its programs
// and the files it uses; the login, money and the network of its agents' shells wait for their first use.
func TestEnablingGivesInAdvanceOnlyWhatIsNotAskedAtUse(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	m := harness.Declare(harness.PlainManifest,
		`{"right": "network", "what": ["api.example.com"], "why": "reads the news"}`,
		`{"right": "files", "what": ["gpu"], "why": "draws"}`,
		`{"right": "money", "what": ["@text"], "why": "summarises"}`,
		`{"right": "login", "what": ["codex"], "why": "its agent works on your login"}`)
	e.Must("deck.project_create", map[string]any{"name": "news", "manifest": m}, nil)
	want := map[string]struct {
		answer string
		atUse  bool
	}{"network": {"always", false}, "files": {"always", false}, "money": {"", true}, "login": {"", true}}
	for _, r := range rightsOf(e, "news") {
		w, ok := want[r.Right]
		if !ok {
			continue
		}
		delete(want, r.Right)
		if r.Answer != w.answer || r.AtUse != w.atUse {
			t.Fatalf("%s: answer %q, asked at use %v after enabling, want %q, %v", r.Right, r.Answer, r.AtUse, w.answer, w.atUse)
		}
	}
	if len(want) != 0 {
		t.Fatalf("rights not listed: %v", want)
	}
}
