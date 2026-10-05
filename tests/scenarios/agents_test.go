package scenarios

import (
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
	f := newACP(t)
	if err := os.WriteFile(filepath.Join(f.dir, "version"), []byte(" 1.53.0"), 0o644); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, nil, func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.dir, "goose"), "dsh": filepath.Join(f.dir, "no-dsh")}
		c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir, nil}
	})
	e.App.Providers.Checks(t.Context(), time.Now())
	if h := e.waitHealth("agent:goose", "ok"); h.Version != "goose 1.53.0" {
		t.Fatalf("an agent is found with its version: %+v", h)
	}
	if _, err := e.App.Store.Q.GetHealth(t.Context(), "agent:dsh"); err == nil {
		t.Fatal("an agent that is not installed has no health row")
	}
	var m modelsOut
	e.must("deck.models_list", nil, &m)
	found := map[string]string{}
	for _, a := range m.Agents {
		found[a.Name] = a.Version + "|" + a.Status
	}
	if found["goose"] != "goose 1.53.0|ok" || found["dsh"] != "|missing" {
		t.Fatalf("the models list shows each agent with its version, and one that is not installed: %+v", m.Agents)
	}
}

// TestTheModelsAndConnectionsTabsShowWhatRunsTheModels: Models lists the assignments, the models with an error in a
// line and in full on demand, and the agents; Connections the connections and the other secrets.
func TestTheModelsAndConnectionsTabsShowWhatRunsTheModels(t *testing.T) {
	f := newACP(t)
	if err := os.WriteFile(filepath.Join(f.dir, "versionfails"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	llm := acpLLM(t)
	e := newEnv(t, nil, func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.dir, "goose"), "dsh": filepath.Join(f.dir, "no-dsh")}
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

	rows := blockData[[]screenRow](t, e.settingsBlock("Assignments", nil)["rows"])
	i := slices.IndexFunc(rows, func(r screenRow) bool { return r.Cells[0] == "image" })
	if i < 0 || fmt.Sprint(rows[i].Cells...) != "imageeverywhererouter/x/picrouter/x/pic" {
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
	if !strings.Contains(fmt.Sprint(agents), "goose --version") || !strings.Contains(fmt.Sprint(agents), "not installed") {
		t.Fatalf("the agents show why one gives no version, and one not installed: %+v", agents)
	}
	conns := blockData[[]screenRow](t, e.settingsBlock("Connections", nil)["rows"])
	if len(conns) != 2 || conns[0].Cells[0] != "router" || conns[1].Cells[0] != "whisper" || fmt.Sprint(verb(t, conns[1].Actions, "edit")["params"].(map[string]any)["local"]) != "true" {
		t.Fatalf("the connections tab lists the connections, the whisper of deck too, and edits each as it is: %+v", conns)
	}
	secrets := blockData[[]screenRow](t, e.settingsBlock("Other secrets", nil)["rows"])
	if !strings.Contains(fmt.Sprint(secrets), "wiki") {
		t.Fatalf("the connections tab lists the secrets of tools too: %+v", secrets)
	}
}

// TestAnAgentWhoseVersionFailsSaysSo: an agent whose --version fails is red with that error and no version.
func TestAnAgentWhoseVersionFailsSaysSo(t *testing.T) {
	f := newACP(t)
	if err := os.WriteFile(filepath.Join(f.dir, "versionfails"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	e := newEnv(t, nil, func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.dir, "goose"), "dsh": filepath.Join(f.dir, "dsh")}
		c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir, nil}
	})
	e.App.Providers.Checks(t.Context(), time.Now())
	for _, agent := range []string{"goose", "dsh"} {
		if h := e.waitHealth("agent:"+agent, "error"); h.Version != "" || !strings.Contains(h.Error, "--version") {
			t.Fatalf("%s: a failing --version is the error it is: %+v", agent, h)
		}
	}
}
