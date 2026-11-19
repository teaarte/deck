package providers

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	deckhome "deck2/internal/home"
	"deck2/tests/harness"
)

// Rights of a module on the logins of Claude Code: both, or the first only.
const (
	bothLogins = `{"right": "login", "what": ["claude-code", "claude-code-2"], "why": "its agents work on either login"}`
	firstLogin = `{"right": "login", "what": ["claude-code"], "why": "its agents work on the first login"}`
)

// secondLogin adds the login claude-code-2 of the fake CLI and signs it in from deck with code.
func secondLogin(e *harness.Env, code string) {
	e.T.Helper()
	e.Must("deck.connection_set", map[string]any{"name": "claude-code-2", "api": "cli", "agent": "claude-code"}, nil)
	e.Must("deck.agent_login", map[string]any{"provider": "claude-code-2"}, nil)
	e.Must("deck.agent_login_finish", map[string]any{"provider": "claude-code-2", "code": code}, nil)
	e.Until("the sign-in of the second login to end", func() bool { return len(signIns(e)) == 0 })
	e.WaitHealth("provider:claude-code-2", "ok")
}

// lastLogin is the sign-in the last call of the fake CLI ran on, empty for none of deck's.
func lastLogin(c *harness.FakeCLI) string {
	return strings.TrimSpace(c.Files(c.Calls())("login"))
}

// TestASecondLoginOfACLIIsAConnectionOfItsOwn: another login of Claude Code is a connection with its own sign-in, homes
// and model; its turns run on its sign-in, the first's on the first's; the login of the first does not open it.
func TestASecondLoginOfACLIIsAConnectionOfItsOwn(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.ChecksPass()
	c.Stream(0, "ok.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	h := deckhome.Layout(e.App.Cfg.Home)
	e.Refused("deck.connection_set", map[string]any{"name": "claude-code", "api": "cli", "agent": "claude-code"}, 422, "built-in provider")
	e.Refused("deck.connection_set", map[string]any{"name": "cc2", "api": "cli", "agent": "claude-code", "url": "https://example.com"}, 422, "takes no url")
	e.Refused("deck.connection_set", map[string]any{"name": "cc2", "api": "cli"}, 422, "names its agent")
	secondLogin(e, "good-code-2")

	if b, err := os.ReadFile(filepath.Join(h.Login("claude.claude-code-2"), ".credentials.json")); err != nil || strings.TrimSpace(string(b)) != "good-code-2" {
		t.Fatalf("the second login keeps its sign-in in a folder of its own: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(h.Login("claude"), ".credentials.json")); err == nil {
		t.Fatal("signing in the second login leaves the first login's file alone")
	}
	var list struct {
		Connections []struct{ Name, API string }
		Models      []struct{ Name, Connection, Agent string }
	}
	e.Must("deck.models_list", map[string]any{}, &list)
	conn, model := false, false
	for _, k := range list.Connections {
		conn = conn || k.Name == "claude-code-2" && k.API == "cli"
	}
	for _, m := range list.Models {
		model = model || m.Name == "claude-code-2" && m.Connection == "claude-code-2" && m.Agent == "claude-code"
	}
	if !conn || !model {
		t.Fatalf("the login is a connection of api cli and a model of its name in the CLI: %+v", list)
	}

	e.Must("deck.project_create", map[string]any{"name": "plain", "manifest": harness.Declared(harness.Declare(harness.PlainManifest, bothLogins))}, nil)
	e.UseModels("plain.work", "claude-code")
	first := e.Start("plain", "work", nil)
	e.WaitState(first, "waiting:right")
	e.Answer(e.WaitAsk().ID, "allow_always")
	if p := e.WaitState(first, "done", "error"); p.State != "done" || lastLogin(c) == "good-code-2" {
		t.Fatalf("a turn of the first login runs on the first login's sign-in: %+v %q", p, lastLogin(c))
	}
	e.UseModels("plain.work", "claude-code-2")
	second := e.Start("plain", "work", nil)
	if p := e.WaitState(second, "waiting:right", "done", "error"); p.State != "waiting:right" {
		t.Fatalf("the login of the first given always does not open the second: %+v", p)
	}
	e.Answer(e.WaitAsk().ID, "allow")
	if p := e.WaitState(second, "done", "error"); p.State != "done" || lastLogin(c) != "good-code-2" {
		t.Fatalf("a turn assigned to the second login runs on its sign-in: %+v %q", p, lastLogin(c))
	}
	if env := c.Call(c.Calls()).Env["CLAUDE_CONFIG_DIR"]; !strings.Contains(env, "claude.claude-code-2") {
		t.Fatalf("the second login runs in a home of its own: %q", env)
	}

	e.Project("one", harness.Declare(harness.PlainManifest, firstLogin))
	e.UseModels("one.work", "claude-code-2")
	e.Refused("one.work", map[string]any{}, 403, "subscription login claude-code-2")
}

// TestALimitOfTheFirstLoginHandsTheWorkToTheSecond: the first login at the limit of its subscription pauses itself
// only; the work goes to the second login, whose own quota and pause are its own row.
func TestALimitOfTheFirstLoginHandsTheWorkToTheSecond(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.ChecksPass()
	raw, _ := os.ReadFile(harness.Testdata("claude/ratelimit_rejected.jsonl"))
	reset := time.Now().Add(5 * time.Hour).Unix()
	c.StreamText(0, strings.ReplaceAll(string(raw), "1790379000", fmt.Sprint(reset)))
	if err := os.WriteFile(filepath.Join(c.Dir, "stream.login.good-code-2"), []byte(withQuota(t, "allowed", 0.3, 0.2)), 0o644); err != nil {
		t.Fatal(err)
	}
	e := harness.New(t, harness.WithCLI(c))
	secondLogin(e, "good-code-2")
	e.Project("plain", harness.Declare(harness.PlainManifest, bothLogins))
	e.Project("one", harness.Declare(harness.PlainManifest, firstLogin))
	e.UseModels("plain.work", "claude-code", "claude-code-2")
	e.UseModels("one.work", "claude-code", "claude-code-2")

	id := e.Start("plain", "work", nil)
	if got := e.WaitState(id, "error", "done", "waiting:limit"); got.State != "error" || e.Fault(id).ErrorType != "model.limit" {
		t.Fatalf("the first login at its limit fails as the model's: %+v %+v", got, e.Fault(id))
	}
	next := e.Following(id)
	if got := e.WaitState(next, "done", "error"); got.State != "done" || got.Provider != "claude-code-2" || lastLogin(c) != "good-code-2" {
		t.Fatalf("the work goes on on the second login: %+v %q", got, lastLogin(c))
	}
	if h := e.HealthRowOf("limit:claude-code"); h == nil || h["status"] != "paused" {
		t.Fatalf("the limit pauses the first login: %v", h)
	}
	if h := e.HealthRowOf("limit:claude-code-2"); h == nil || h["status"] != "ok" || !strings.Contains(fmt.Sprint(h["error"]), "30 percent") {
		t.Fatalf("the second login has its own row of its quota, not paused: %v", h)
	}
	calls := c.Calls()
	again := e.Start("plain", "work", nil)
	if got := e.WaitState(again, "done", "error"); got.State != "done" || got.Provider != "claude-code-2" || c.Calls() != calls+1 {
		t.Fatalf("a new turn goes straight to the second login while the first is paused: %+v", got)
	}
	held := e.Start("one", "work", nil)
	if p := e.WaitState(held, "waiting:limit", "waiting:provider", "done", "error"); p.Provider != "claude-code" || p.State == "done" {
		t.Fatalf("a module with the right on the first login only waits for it, not the second: %+v", p)
	}
	if c.Calls() != calls+1 {
		t.Fatalf("the CLI did not start for that module: %d", c.Calls()-calls)
	}
}

// TestRemovingASecondLoginLeavesTheFirst: removing the second login takes its model, its assignments and its sign-in
// folder; the first login, its file and its turns stay as they were.
func TestRemovingASecondLoginLeavesTheFirst(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.ChecksPass()
	c.Stream(0, "ok.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	h := deckhome.Layout(e.App.Cfg.Home)
	if err := os.MkdirAll(h.Login("claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	first := filepath.Join(h.Login("claude"), ".credentials.json")
	if err := os.WriteFile(first, []byte("the owner's login\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	secondLogin(e, "good-code-2")
	e.Project("plain", harness.Declare(harness.PlainManifest, bothLogins))
	e.UseModels("plain.work", "claude-code-2", "claude-code")
	e.Refused("deck.model_delete", map[string]any{"name": "claude-code-2"}, 422, "connection_delete")
	e.Refused("deck.model_set", map[string]any{"connection": "claude-code", "model": "opus", "name": "claude-code-2"}, 422, "stays on its own connection")
	e.Refused("deck.connection_delete", map[string]any{"name": "claude-code"}, 422, "deck keeps itself")
	e.Must("deck.connection_delete", map[string]any{"name": "claude-code-2"}, nil)

	if _, err := os.Stat(filepath.Join(e.App.Cfg.Home, "agents", "claude.claude-code-2")); !os.IsNotExist(err) {
		t.Fatalf("removing the login removes its sign-in and homes: %v", err)
	}
	if b, err := os.ReadFile(first); err != nil || string(b) != "the owner's login\n" {
		t.Fatalf("the first login's file stays: %q %v", b, err)
	}
	var list struct {
		Connections []struct{ Name string }
		Models      []struct{ Name string }
	}
	e.Must("deck.models_list", map[string]any{}, &list)
	for _, k := range list.Connections {
		if k.Name == "claude-code-2" {
			t.Fatalf("the connection is gone: %+v", list.Connections)
		}
	}
	for _, m := range list.Models {
		if m.Name == "claude-code-2" {
			t.Fatalf("the login's model is gone: %+v", list.Models)
		}
	}
	id := e.Start("plain", "work", nil)
	if p := e.WaitState(id, "done", "error"); p.State != "done" || p.Provider != "claude-code" {
		t.Fatalf("the work goes to the first login, as before: %+v", p)
	}
	// A login of the same name added again is another account: what the module was given on the old one is asked again.
	secondLogin(e, "good-code-3")
	e.UseModels("plain.work", "claude-code-2")
	again := e.Start("plain", "work", nil)
	if p := e.WaitState(again, "waiting:right", "done", "error"); p.State != "waiting:right" {
		t.Fatalf("the right on a removed login does not pass to a new one of its name: %+v", p)
	}
}

// TestTheOwnersLoginRunsAsBeforeAfterTheMigrationOfLogins: a base from before logins were connections migrates its
// built-in CLIs into logins of their own name; the owner's sign-in stays where it was and its turns run on it.
func TestTheOwnersLoginRunsAsBeforeAfterTheMigrationOfLogins(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.ChecksPass()
	c.Stream(0, "ok.jsonl")
	c.Models("models.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	h := deckhome.Layout(e.App.Cfg.Home)
	if err := os.MkdirAll(h.Login("claude"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.Login("claude"), ".credentials.json"), []byte("owner\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.Must("deck.model_set", map[string]any{"connection": "claude-code", "model": "opus", "name": "opus"}, nil)
	// The base of a deck before logins were connections, to be migrated.
	e.SQL(`ALTER TABLE connection DROP COLUMN agent; ALTER TABLE chat_topic DROP COLUMN again_provider; ALTER TABLE chat_topic DROP COLUMN again_note; ALTER TABLE chat_topic DROP COLUMN again_follows`)
	// The migration of the base, which deck shows nowhere.
	e.SQL(`DELETE FROM goose_db_version WHERE version_id >= 89`)
	e = e.Restart()
	var agent string
	// The CLI of a connection, which deck shows nowhere.
	if err := e.SQLRow(`SELECT agent FROM connection WHERE workspace_id = 1 AND name = 'claude-code'`).Scan(&agent); err != nil || agent != "claude-code" {
		t.Fatalf("the built-in CLI is a login of its own name: %q %v", agent, err)
	}
	e.Project("plain", harness.OnLogin(harness.PlainManifest))
	for _, model := range []string{"claude-code", "opus"} {
		e.UseModels("plain.work", model)
		id := e.Start("plain", "work", nil)
		if p := e.WaitState(id, "done", "error"); p.State != "done" {
			t.Fatalf("%s runs after the migration: %+v", model, p)
		}
		env := c.Call(c.Calls()).Env["CLAUDE_CONFIG_DIR"]
		if env != "" && (lastLogin(c) != "owner" || strings.Contains(env, "claude.")) {
			t.Fatalf("%s runs on the owner's sign-in in its old place: %q %q", model, env, lastLogin(c))
		}
	}
	if _, err := os.Stat(filepath.Join(e.App.Cfg.Home, "agents", "claude.claude-code")); !os.IsNotExist(err) {
		t.Fatalf("no folder of a login is made for the built-in one: %v", err)
	}
}
