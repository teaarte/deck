package providers

import (
	"encoding/json"
	"slices"
	"testing"

	"deck2/tests/harness"
)

// loginRow is the row of login of a module in deck.rights_list.
func loginRow(e *harness.Env, project string) (answer string, atUse bool) {
	e.T.Helper()
	var rows []struct {
		Right, Answer string
		Key           bool `json:"key"`
	}
	e.Must("deck.rights_list", map[string]any{"project": project}, &rows)
	for _, r := range rows {
		if r.Right == "login" {
			return r.Answer, r.Key
		}
	}
	e.T.Fatalf("no row of login: %+v", rows)
	return "", false
}

// TestALoginIsAskedWhenAnAgentFirstStartsOnIt: enabling a module does not give it the subscription login; its first
// turn on the CLI waits for the owner's answer, once for that work, always for the next.
func TestALoginIsAskedWhenAnAgentFirstStartsOnIt(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	for i := range 3 {
		c.Stream(i, "ok.jsonl")
	}
	e := harness.New(t, harness.WithCLI(c))
	e.Must("deck.project_create", map[string]any{"name": "plain", "manifest": harness.Declared(harness.OnLogin(harness.PlainManifest))}, nil)
	if a, atUse := loginRow(e, "plain"); a != "" || !atUse {
		t.Fatalf("enabling leaves the login to its first use: %q %v", a, atUse)
	}
	first := e.Start("plain", "work", nil)
	e.WaitState(first, "waiting:right")
	a := e.WaitAsk()
	var rows []struct {
		ID      int64
		Change  string
		Actions []map[string]any
	}
	e.Must("deck.asks_list", nil, &rows)
	var verbs []string
	for _, b := range rows[0].Actions {
		verbs = append(verbs, b["verb"].(string))
	}
	if a.Kind != "call" || !slices.Equal(verbs, []string{"allow", "deny_kept", "allow_work", "allow_always", "deny_because"}) || c.Calls() != 0 {
		t.Fatalf("the turn waits for the owner's answer before the CLI starts: %+v %v", a, verbs)
	}
	e.Answer(a.ID, "allow")
	if p := e.WaitState(first, "done", "error"); p.State != "done" {
		t.Fatalf("once allowed, the turn runs: %+v", p)
	}
	second := e.Start("plain", "work", nil)
	e.WaitState(second, "waiting:right")
	e.Answer(e.WaitAsk().ID, "allow_always")
	e.WaitState(second, "done")
	if a, _ := loginRow(e, "plain"); a != "always" {
		t.Fatalf("always is kept for the module: %q", a)
	}
	third := e.Start("plain", "work", nil)
	if p := e.WaitState(third, "done", "error"); p.State != "done" || len(e.Asks()) != 0 {
		t.Fatalf("a login given always asks no more: %+v", p)
	}
}

// TestALoginTheOwnerRefusesStopsTheTurn: a no to the login ends the turn that asked, and is kept: the next start does
// not ask again nor run on that login.
func TestALoginTheOwnerRefusesStopsTheTurn(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	e := harness.New(t, harness.WithCLI(c))
	e.Must("deck.project_create", map[string]any{"name": "plain", "manifest": harness.Declared(harness.OnLogin(harness.PlainManifest))}, nil)
	id := e.Start("plain", "work", nil)
	e.WaitState(id, "waiting:right")
	e.Answer(e.WaitAsk().ID, "deny")
	if p := e.WaitState(id, "error"); c.Calls() != 0 {
		t.Fatalf("a refused login never starts the CLI: %+v", p)
	}
	code, out := e.Call("plain.work", map[string]any{})
	var next struct{ Process int64 }
	_ = json.Unmarshal(out, &next)
	if code == 200 && next.Process != 0 {
		e.WaitState(next.Process, "done", "error")
	}
	if len(e.Asks()) != 0 || c.Calls() != 0 {
		t.Fatalf("a kept no is not asked again, and the CLI does not start: %d %s", code, out)
	}
}
