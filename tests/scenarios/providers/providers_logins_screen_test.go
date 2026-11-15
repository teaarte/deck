package providers

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"deck2/tests/harness"
)

// TestTheOwnerAddsASecondLoginInSettings: the card of a CLI in Settings → Models opens the dialog of another login,
// whose form sends a login; the new login shows it needs a sign-in, and its cards and Waiting for you sign in to it.
func TestTheOwnerAddsASecondLoginInSettings(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	nologin, err := os.ReadFile(harness.Testdata("claude/nologin.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	// A CLI with no sign-in of deck's answers that it needs one.
	if err := os.WriteFile(filepath.Join(c.Dir, "stream.login."), nologin, 0o644); err != nil {
		t.Fatal(err)
	}
	e := harness.New(t, harness.WithCLI(c))
	open := e.Button("", "settings", nil, "", map[string]any{"dialog": "login", "agent": "claude-code"})
	params, _ := open["params"].(map[string]any)
	if params["name"] != "claude-code-2" {
		t.Fatalf("the card of the CLI opens the dialog of another login, named after it: %v", open)
	}
	var form struct {
		Values map[string]any `json:"values"`
		Fixed  map[string]any `json:"fixed"`
	}
	at, _ := json.Marshal(map[string]any{"name": params["name"], "agent": params["agent"]})
	for _, d := range e.Screen("", "settings", map[string]any{"dialog": "login", "tab": "Models", "params": string(at)}) {
		var v struct {
			Commands []struct{ Name string } `json:"commands"`
		}
		if json.Unmarshal(d["value"], &v) == nil && len(v.Commands) == 1 && v.Commands[0].Name == "deck.connection_set" {
			if err := json.Unmarshal(d["value"], &form); err == nil && form.Fixed["api"] == "cli" {
				break
			}
		}
	}
	if form.Fixed["api"] != "cli" || form.Fixed["url"] != "" || form.Fixed["key"] != "" || form.Fixed["node"] != "" || form.Fixed["local"] != false ||
		form.Values["agent"] != "claude-code" || form.Values["name"] != "claude-code-2" {
		t.Fatalf("the form of a login sends api cli and shows only its name and CLI: %+v", form)
	}
	in := map[string]any{}
	for _, m := range []map[string]any{form.Values, form.Fixed} {
		for k, v := range m {
			in[k] = v
		}
	}
	e.Must("deck.connection_set", in, nil)
	e.WaitHealth("provider:claude-code-2", "needs_login")
	e.Refused("deck.connection_set", in, 422, "already there")

	e.Project("plain", harness.Declare(harness.PlainManifest, bothLogins))
	e.UseModels("plain.work", "claude-code-2")
	var list struct {
		Waits  []struct{ Kind, Agent string }
		Models []struct{ Name, Login string }
	}
	e.Must("deck.models_list", map[string]any{}, &list)
	for _, m := range list.Models {
		if m.Name == "claude-code-2" && m.Login != "claude-code-2" || m.Name == "claude-code" && m.Login != "claude-code" {
			t.Fatalf("a model on a login names the login its card signs in to: %+v", m)
		}
	}
	waits := false
	for _, w := range list.Waits {
		waits = waits || w.Kind == "sign_in" && w.Agent == "claude-code-2"
	}
	if !waits {
		t.Fatalf("Waiting for you asks to sign in to the login its work goes to: %+v", list.Waits)
	}
	var l harness.SignInRow
	code, out := e.Press(e.Button("", "settings", nil, "deck.agent_login", map[string]any{"provider": "claude-code-2"}))
	if err := json.Unmarshal(out, &l); code != 200 || err != nil || !l.Paste || l.URL == "" {
		t.Fatalf("Sign in on the login starts the sign-in of the login: %d %s", code, out)
	}
	if code, out := e.Press(e.Button("", "settings", nil, "deck.connection_delete", map[string]any{"name": "claude-code-2"})); code != 200 {
		t.Fatalf("Remove on the card of the login removes it: %d %s", code, out)
	}
	e.Must("deck.models_list", map[string]any{}, &list)
	for _, m := range list.Models {
		if m.Name == "claude-code-2" {
			t.Fatalf("the login is gone: %+v", list.Models)
		}
	}
}
