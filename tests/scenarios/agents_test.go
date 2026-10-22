package scenarios

import (
	"deck2/internal/health"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/sandbox"
)

// TestAgentsAreFoundWithTheirVersion: deck finds the ACP agents installed with their version, one health row each.
func TestAgentsAreFoundWithTheirVersion(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	if err := os.WriteFile(filepath.Join(f.dir, "version"), []byte(" 1.53.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, nil, func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.dir, "goose")}
		c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir, nil}
	})
	e.App.Providers.Checks(t.Context(), time.Now())
	if h := e.waitHealth("agent:goose", "ok"); h.Version != "goose 1.53.0" {
		t.Fatalf("an agent is found with its version: %+v", h)
	}
	found := func(e *Env) string {
		var m modelsOut
		e.must("deck.models_list", nil, &m)
		for _, a := range m.Agents {
			if a.Name == "goose" {
				return a.Version + "|" + a.Status
			}
		}
		return ""
	}
	if got := found(e); got != "goose 1.53.0|ok" {
		t.Fatalf("the models list shows the agent with its version: %s", got)
	}
	gone := newEnv(t, nil, func(c *app.Config) { c.ACPBinaries = map[string]string{"goose": filepath.Join(f.dir, "no-goose")} })
	gone.App.Providers.Checks(t.Context(), time.Now())
	if _, err := health.Get(t.Context(), gone.App.Store.Q, health.Of{}, "agent:goose"); err == nil {
		t.Fatal("an agent that is not installed has no health row")
	}
	if got := found(gone); got != "|missing" {
		t.Fatalf("the models list shows an agent that is not installed: %s", got)
	}
	if agents := blockData[[]screenRow](t, gone.settingsBlock("Agents", nil)["rows"]); !strings.Contains(fmt.Sprint(agents), "not installed") {
		t.Fatalf("the agents tab shows an agent that is not installed: %+v", agents)
	}
}

// TestTheModelsAndConnectionsTabsShowWhatRunsTheModels: Models lists the assignments, the models with an error in a
// line and in full on demand, and the agents; Connections the connections and the other secrets.
func TestTheModelsAndConnectionsTabsShowWhatRunsTheModels(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	if err := os.WriteFile(filepath.Join(f.dir, "versionfails"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	llm := acpLLM(t)
	e := newEnv(t, nil, func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.dir, "goose")}
		c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir, nil}
	})
	e.App.Providers.Checks(t.Context(), time.Now())
	e.waitHealth("agent:goose", "error")
	e.must("deck.secret_set", map[string]any{"name": "wiki", "value": "w"}, nil)
	e.must("deck.connection_set", map[string]any{"name": "router", "api": "openai", "url": llm.URL, "key": "sk-router"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "router", "model": "x/pic", "kind": "image", "call_usd": 0.04}, nil)
	e.must("deck.model_set", map[string]any{"connection": "router", "model": "m", "name": "acp", "agent": "goose"}, nil)
	e.waitHealth("provider:acp", "error")
	e.must("deck.assignment_set", map[string]any{"role": "image", "models": []string{"router/x/pic"}}, nil)
	var kept struct {
		Kind    string
		CallUSD float64 `json:"call_usd"`
	}
	e.must("deck.model_set", map[string]any{"connection": "router", "model": "x/pic"}, &kept)
	if kept.Kind != "image" || kept.CallUSD != 0.04 {
		t.Fatalf("a model changed without its kind and price keeps them: %+v", kept)
	}

	rows := blockData[[]screenRow](t, e.settingsBlock("Everywhere else", nil)["rows"])
	i := slices.IndexFunc(rows, func(r screenRow) bool { return r.Cells[0] == "image" })
	if i < 0 || fmt.Sprint(rows[i].Cells...) != "imagerouter/x/picrouter/x/pic$0.04 a call" {
		t.Fatalf("the assignments show which models do which work and which one does it now: %+v", rows)
	}
	var acp *screenRow
	models := blockData[[]screenRow](t, e.settingsBlock("Models", nil)["rows"])
	for i, r := range models {
		if r.Cells[0] == "acp" {
			acp = &models[i]
		}
	}
	if acp == nil || models[0].Cells[0] != "fake" || len(acp.Details) != len(acp.Cells) {
		t.Fatalf("the models are the built-in providers and the models of connections, with details: %+v", models)
	}
	whole := ""
	for _, d := range acp.Details {
		if s, ok := d.(string); ok {
			whole = s
		}
	}
	if !strings.Contains(whole, "--version") || !hasVerb(acp.Actions, "check") {
		t.Fatalf("the error of a model is in full on demand, and its check runs again: %+v", acp)
	}
	agents := blockData[[]screenRow](t, e.settingsBlock("Agents", nil)["rows"])
	if !strings.Contains(fmt.Sprint(agents), "goose --version") {
		t.Fatalf("the agents show why one gives no version: %+v", agents)
	}
	conns := blockData[[]screenRow](t, e.settingsBlock("Connections", nil)["rows"])
	if len(conns) != 4 || conns[2].Cells[0] != "router" || conns[3].Cells[0] != "whisper" || fmt.Sprint(verb(t, conns[3].Actions, "edit")["params"].(map[string]any)["local"]) != "true" {
		t.Fatalf("the connections tab lists the connections, the whisper of deck too, and edits each as it is: %+v", conns)
	}
	if conns[0].Cells[0] != "claude-code" || conns[1].Cells[0] != "codex" || len(conns[0].Actions)+len(conns[1].Actions) != 0 {
		t.Fatalf("the connections of the CLIs are deck's own, with no edit or remove: %+v", conns[:2])
	}
	secrets := blockData[[]screenRow](t, e.settingsBlock("Secrets", nil)["rows"])
	if !strings.Contains(fmt.Sprint(secrets), "wiki") {
		t.Fatalf("the connections tab lists the secrets of tools too: %+v", secrets)
	}
}

// TestAnAgentWhoseVersionFailsSaysSo: an agent whose --version fails is red with that error and no version.
func TestAnAgentWhoseVersionFailsSaysSo(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	if err := os.WriteFile(filepath.Join(f.dir, "versionfails"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, nil, func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.dir, "goose")}
		c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir, nil}
	})
	e.App.Providers.Checks(t.Context(), time.Now())
	if h := e.waitHealth("agent:goose", "error"); h.Version != "" || !strings.Contains(h.Error, "--version") {
		t.Fatalf("a failing --version is the error it is: %+v", h)
	}
}

// TestAModelOfOpenCodeSaysToSetItAgain: deck runs OpenCode no more; a base from before keeps such a model, red with
// what to do, and a command assigned to it refuses with that reason.
func TestAModelOfOpenCodeSaysToSetItAgain(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := newEnvWith(t, home, &Fake{})
	e.olderBase(14)
	for _, q := range []string{
		`INSERT INTO connection (workspace_id, name, api, url) VALUES (1, 'router', 'openai', 'https://openrouter.ai/api/v1')`,
		`INSERT INTO model (workspace_id, connection_id, name, model, kind, agent) SELECT 1, id, 'free', 'x/free', 'text', 'opencode' FROM connection WHERE name = 'router'`,
		`INSERT INTO health (subject, status, error) VALUES ('provider:free', 'error', 'install it with npm i -g opencode-ai@1.18.30'), ('agent:opencode', 'error', 'opencode 2.0.20 is not supported'),
		   ('provider:removed-long-ago', 'error', 'the model did not finish'), ('provider:codex', 'ok', '')`,
	} {
		if _, err := e.App.Store.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = e.App.Close()
	e = newEnvWith(t, home, &Fake{})
	if _, err := health.Get(t.Context(), e.App.Store.Q, health.Of{}, "agent:opencode"); err == nil {
		t.Fatal("the health row of OpenCode is gone with it")
	}
	if _, err := health.Get(t.Context(), e.App.Store.Q, health.Of{}, "provider:removed-long-ago"); err == nil {
		t.Fatal("the health of a model removed before goes")
	}
	if _, err := health.Get(t.Context(), e.App.Store.Q, health.Of{}, "provider:codex"); err != nil {
		t.Fatal("the health of a built-in provider stays")
	}
	if h, err := health.Get(t.Context(), e.App.Store.Q, health.Of{}, "provider:free"); err != nil || h.Status != "error" || !strings.Contains(h.Error, "goose") {
		t.Fatalf("a model of OpenCode is red with what to do: %+v %v", h, err)
	}
	e.createProject("plain", testManifest([]string{agentCommand("work", "")}, nil))
	if code, out := e.call("deck.assignment_set", map[string]any{"role": "agent", "project": "plain", "command": "work", "models": []string{"free"}}); code != 422 || !strings.Contains(string(out), "goose") {
		t.Fatalf("a model of OpenCode is not assigned anew: %d %s", code, out)
	}
	if _, err := e.App.Store.DB.Exec(`INSERT INTO assignment (workspace_id, project_id, command, role, models) SELECT 1, id, 'work', 'agent', '["free"]' FROM project WHERE name = 'plain'`); err != nil {
		t.Fatal(err)
	}
	if code, out := e.call("plain.work", map[string]any{}); code == 200 || !strings.Contains(string(out), "goose") {
		t.Fatalf("a command on it refuses with the reason: %d %s", code, out)
	}
}

// TestAModelOfDshSaysToSetItAgain: deck runs dsh no more; its agent row goes, a model on it is red with what to do,
// and an open call of a model frozen before keeps the owner's yes.
func TestAModelOfDshSaysToSetItAgain(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := newEnvWith(t, home, &Fake{})
	e.createProject("plain", plainManifest)
	e.olderBase(37)
	for _, q := range []string{
		`INSERT INTO connection (workspace_id, name, api, url) VALUES (1, 'router', 'openai', 'https://openrouter.ai/api/v1')`,
		`INSERT INTO model (workspace_id, connection_id, name, model, kind, agent) SELECT 1, id, 'free', 'x/free', 'text', 'dsh' FROM connection WHERE name = 'router'`,
		`INSERT INTO health (subject, status, error) VALUES ('provider:free', 'ok', ''), ('agent:dsh', 'ok', '')`,
		`INSERT INTO ask (workspace_id, project_id, kind, text, payload, state) SELECT 1, id, 'call', 'A picture?',
		   '{"call":{"command":"deck.model_call","input":{"kind":"image"}},"tool":"deck.model_request"}', 'open' FROM project WHERE name = 'plain'`,
	} {
		if _, err := e.App.Store.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	_ = e.App.Close()
	e = newEnvWith(t, home, &Fake{})
	if _, err := health.Get(t.Context(), e.App.Store.Q, health.Of{}, "agent:dsh"); err == nil {
		t.Fatal("the health row of dsh is gone with it")
	}
	if h, err := health.Get(t.Context(), e.App.Store.Q, health.Of{}, "provider:free"); err != nil || h.Status != "error" || !strings.Contains(h.Error, "agent goose") {
		t.Fatalf("a model of dsh is red with what to do: %+v %v", h, err)
	}
	var tool string
	if err := e.App.Store.DB.QueryRow(`SELECT json_extract(payload, '$.tool') FROM ask WHERE kind = 'call'`).Scan(&tool); err != nil || tool != "deck.model_call" {
		t.Fatalf("an open call of a model keeps the fingerprint deck checks on the yes: %q %v", tool, err)
	}
}

// TestAModelThatGetsAnAgentLosesTheHealthOfItsCalls: the health of a model in an agent is its self-check's; a red row
// of its calls from before does not keep it out of its assignment.
func TestAModelThatGetsAnAgentLosesTheHealthOfItsCalls(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	llm := acpLLM(t)
	e := newEnv(t, nil, func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.dir, "goose")}
		c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir, nil}
	})
	e.must("deck.connection_set", map[string]any{"name": "router", "api": "openai", "url": llm.URL, "key": "sk-router"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "router", "model": "m", "name": "qwen", "kind": "text"}, nil)
	if _, err := e.App.Store.DB.Exec(`INSERT INTO health (subject, workspace_id, status, error) VALUES ('model:qwen', 1, 'error', 'did not answer')`); err != nil {
		t.Fatal(err)
	}
	e.must("deck.model_set", map[string]any{"connection": "router", "model": "m", "name": "qwen", "agent": "goose"}, nil)
	var n int
	_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM health WHERE subject = 'model:qwen'`).Scan(&n)
	if n != 0 {
		t.Fatal("the red row of the calls of the model goes once it runs in an agent")
	}
}
