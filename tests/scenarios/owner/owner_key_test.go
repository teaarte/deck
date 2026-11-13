package owner

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"deck2/tests/harness"
)

// TestADecisionThatTrustsForLongTakesAPasskeyTouchedWithinFiveMinutes is the table of decisions × the age of the touch
// of the passkey: what trusts for long goes only within five minutes of a touch, the rest at any age.
func TestADecisionThatTrustsForLongTakesAPasskeyTouchedWithinFiveMinutes(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"ok": true}`) })
	e := harness.New(t, harness.WithClock())
	_, tok := e.Token("terminal", "deck.project_create", "notes.*")
	e.Project("notes", harness.PostManifest(svc.URL, ""))
	e.Secret("notes", "svc", "k")
	e.Project("web", harness.Declare(harness.PlainManifest, `{"right": "network", "what": ["api.example.com", "*"], "why": "reads the news"}`))
	var made struct{ ID int64 }
	e.Must("deck.token_create", map[string]any{"name": "vouched", "commands": []string{"deck.projects_list"}}, &made)

	var link struct{ Code string }
	e.Must("deck.pair_link", map[string]any{"name": "phone"}, &link)
	phone := harness.NewPasskey()
	if _, err := phone.Pair(e.URL, e.Https(), link.Code, "phone"); err != nil {
		t.Fatal(err)
	}
	n := 0
	// asked is a new request of a call of the module's tool, which offers always, the owner's last answer forgotten.
	asked := func() int64 {
		e.Must("deck.right_set", map[string]any{"project": "notes", "right": "send", "subject": "post", "answer": "revoke"}, nil)
		n++
		code, out := e.As(tok).Call("notes.post", map[string]any{"id": fmt.Sprint(n), "text": "hi"})
		var w struct{ Waiting int64 }
		_ = json.Unmarshal(out, &w)
		if code != 200 || w.Waiting == 0 {
			t.Fatalf("a call of the tool asks the owner: %d %s", code, out)
		}
		return w.Waiting
	}
	// draft is a new draft, which a token makes, not the owner.
	draft := func() map[string]any {
		n++
		name := fmt.Sprintf("draft%d", n)
		if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": name, "manifest": harness.PlainManifest}); code != 200 {
			t.Fatalf("a token makes a draft: %d %s", code, out)
		}
		var m struct{ Revision int64 }
		e.Must("deck.manifest_get", map[string]any{"project": name}, &m)
		return map[string]any{"project": name, "revision": m.Revision}
	}
	same := func(in map[string]any) func() map[string]any { return func() map[string]any { return in } }
	rows := []struct {
		what    string
		command string
		input   func() map[string]any
		keyed   bool
	}{
		{"always to the whole internet", "deck.right_set", same(map[string]any{"project": "web", "right": "network", "subject": "*", "answer": "always"}), true},
		{"always to a named host", "deck.right_set", same(map[string]any{"project": "web", "right": "network", "subject": "api.example.com", "answer": "always"}), false},
		{"no to the whole internet", "deck.right_set", same(map[string]any{"project": "web", "right": "network", "subject": "*", "answer": "no"}), false},
		{"a revoke", "deck.right_set", same(map[string]any{"project": "web", "right": "network", "subject": "*", "answer": "revoke"}), false},
		{"once to a call that acts in the owner's name", "deck.ask_answer", func() map[string]any { return map[string]any{"ask": asked(), "answer": "allow"} }, false},
		{"no to it", "deck.ask_answer", func() map[string]any { return map[string]any{"ask": asked(), "answer": "deny"} }, false},
		{"always to it", "deck.ask_answer", func() map[string]any { return map[string]any{"ask": asked(), "answer": "allow_always"} }, true},
		{"enabling a draft", "deck.project_enable", draft, true},
		{"a token", "deck.token_create", same(map[string]any{"name": "more", "commands": []string{"deck.projects_list"}}), true},
		{"a link that pairs a device", "deck.pair_link", same(map[string]any{"name": "tablet"}), true},
		{"trusting the data of a module", "deck.project_trust", same(map[string]any{"project": "notes"}), true},
		{"trusting a token", "deck.token_trust", same(map[string]any{"token": made.ID, "trusted": true}), true},
		{"taking the trust back", "deck.token_trust", same(map[string]any{"token": made.ID, "trusted": false}), false},
		{"the master's pages after text from outside always", "deck.setting_set", same(map[string]any{"name": "master_web", "value": "always"}), true},
		{"and back to asking", "deck.setting_set", same(map[string]any{"name": "master_web", "value": "ask"}), false},
	}
	for _, row := range rows {
		for _, age := range []time.Duration{0, 4*time.Minute + 59*time.Second, 5*time.Minute + time.Second, time.Hour} {
			cookie, err := phone.Login(e.URL, e.Https())
			if err != nil {
				t.Fatal(err)
			}
			in := row.input()
			e.Clock.Add(age)
			code, out := e.Signed(cookie).Call(row.command, in)
			want := http.StatusOK
			if row.keyed && age > 5*time.Minute {
				want = http.StatusPreconditionRequired
			}
			if code != want || want != 200 && !strings.Contains(string(out), "passkey") {
				t.Fatalf("%s, the passkey touched %v before: %d %s, want %d", row.what, age, code, out, want)
			}
			if want != 200 {
				// The same session goes once the owner touches the passkey again: a new session of the same browser.
				again, err := phone.Login(e.URL, e.Https())
				if err != nil {
					t.Fatal(err)
				}
				if code, out := e.Signed(again).Call(row.command, in); code != 200 {
					t.Fatalf("%s, after a new touch: %d %s", row.what, code, out)
				}
			}
		}
	}
}
