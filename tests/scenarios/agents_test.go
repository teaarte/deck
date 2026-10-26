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

// TestTheModelsAndConnectionsTabsShowWhatRunsTheModels: Models lists who does what, the models with an error in a
// line and in full on demand, and its dialog the connections; Advanced the agents and the other secrets.
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

	rows := blockData[[]screenRow](t, e.settingsBlock("Who does what", nil)["rows"])
	i := slices.IndexFunc(rows, func(r screenRow) bool { return r.Cells[1] == "image" })
	if i < 0 || rows[i].Cells[0] != "deck" || rows[i].Cells[2] != "router/x/pic" || rows[i].Cells[3] != "$0.04 a call" || !hasVerb(rows[i].Actions, "remove") {
		t.Fatalf("the assignments show which models do which work and which one does it now: %+v", rows)
	}
	if pick := verb(t, rows[i].Actions, "model"); fmt.Sprint(pick["options"]) != "[router/x/pic]" || fmt.Sprint(pick["groups"]) != "[router]" {
		t.Fatalf("the models a row picks from stand under their provider: %+v", pick)
	}
	var acp *listItem
	models := e.allModels(nil)
	for i, r := range models {
		if r.Title == "acp" {
			acp = &models[i]
		}
	}
	if acp == nil || models[0].Title != "fake" || acp.Group != "router" {
		t.Fatalf("the models are the built-in providers and the models of connections, by provider: %+v", models)
	}
	whole := acp.Details
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

// TestAModelInNoAgentRunsNoCommand: a text model without an agent is not assigned to a command, and a command
// assigned to it anyway refuses with the agents to set it in.
func TestAModelInNoAgentRunsNoCommand(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "router", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "router", "model": "x/free", "name": "free", "kind": "text"}, nil)
	e.createProject("plain", testManifest([]string{agentCommand("work", "")}, nil))
	if code, out := e.call("deck.assignment_set", map[string]any{"role": "agent", "project": "plain", "command": "work", "models": []string{"free"}}); code != 422 || !strings.Contains(string(out), "goose") {
		t.Fatalf("a model in no agent is not assigned: %d %s", code, out)
	}
	if _, err := e.App.Store.DB.Exec(`INSERT INTO assignment (workspace_id, project_id, command, role, models) SELECT 1, id, 'work', 'agent', '["free"]' FROM project WHERE name = 'plain'`); err != nil {
		t.Fatal(err)
	}
	if code, out := e.call("plain.work", map[string]any{}); code == 200 || !strings.Contains(string(out), "goose") {
		t.Fatalf("a command on it refuses with the reason: %d %s", code, out)
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
