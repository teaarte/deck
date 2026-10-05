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

// TestAgentsAreFoundWithTheirVersion: deck finds the ACP agents installed with their version, one health row each; an
// opencode of 2.x is named with what deck runs and how to go back, and a model on it fails its check for that reason.
func TestAgentsAreFoundWithTheirVersion(t *testing.T) {
	f := newACP(t)
	if err := os.WriteFile(filepath.Join(f.dir, "version"), []byte("opencode v2.0.20"), 0o644); err != nil {
		t.Fatal(err)
	}
	llm := acpLLM(t)
	e := newEnv(t, nil, func(c *app.Config) {
		c.ACPBinaries = map[string]string{"opencode": filepath.Join(f.dir, "opencode"), "dsh": filepath.Join(f.dir, "no-dsh")}
		c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir, nil}
	})
	e.App.Providers.Checks(t.Context(), time.Now())
	h := e.waitHealth("agent:opencode", "error")
	if h.Version != "opencode 2.0.20" || !strings.Contains(h.Error, "1.x") || !strings.Contains(h.Error, "opencode-ai@1.18.30") {
		t.Fatalf("an opencode of 2.x is red with what deck runs and how to go back: %+v", h)
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
	if found["opencode"] != "opencode 2.0.20|error" || found["dsh"] != "|missing" {
		t.Fatalf("the models list shows each agent with its version, and one that is not installed: %+v", m.Agents)
	}
	e.must("deck.secret_set", map[string]any{"name": "model_key", "value": "sk-real-model-key"}, nil)
	e.must("deck.provider_add", map[string]any{"name": "acp", "url": llm.URL, "model": "m", "key_secret": "model_key", "agent": "opencode"}, nil)
	if h := e.waitHealth("provider:acp", "error"); !strings.Contains(h.Error, "1.x") || f.calls() != 0 {
		t.Fatalf("a model on an opencode deck does not run fails its check for that reason, with no turn: %+v, %d calls", h, f.calls())
	}
}

// TestTheModelsAndConnectionsTabsShowWhatRunsTheModels: Models lists the assignments, the models with an error in a
// line and in full on demand, and the agents; Connections the connections and the other secrets.
func TestTheModelsAndConnectionsTabsShowWhatRunsTheModels(t *testing.T) {
	f := newACP(t)
	if err := os.WriteFile(filepath.Join(f.dir, "version"), []byte("opencode v2.0.20"), 0o644); err != nil {
		t.Fatal(err)
	}
	llm := acpLLM(t)
	e := newEnv(t, nil, func(c *app.Config) {
		c.ACPBinaries = map[string]string{"opencode": filepath.Join(f.dir, "opencode"), "dsh": filepath.Join(f.dir, "no-dsh")}
		c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir, nil}
	})
	e.App.Providers.Checks(t.Context(), time.Now())
	e.waitHealth("agent:opencode", "error")
	e.must("deck.secret_set", map[string]any{"name": "wiki", "value": "w"}, nil)
	e.must("deck.connection_set", map[string]any{"name": "router", "api": "openai", "url": llm.URL, "key": "sk-router"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "router", "model": "x/pic", "kind": "image", "call_usd": 0.04}, nil)
	e.must("deck.model_set", map[string]any{"connection": "router", "model": "m", "name": "acp", "agent": "opencode"}, nil)
	e.waitHealth("provider:acp", "error")
	e.must("deck.assignment_set", map[string]any{"role": "image", "models": []string{"router/x/pic"}}, nil)

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
	if !strings.Contains(whole, "opencode-ai@1.18.30") || !hasVerb(acp.Actions, "check") {
		t.Fatalf("the error of a model is in full on demand, and its check runs again: %+v", acp)
	}
	agents := blockData[[]screenRow](t, e.settingsBlock("Agents", nil)["rows"])
	if !strings.Contains(fmt.Sprint(agents), "opencode 2.0.20") || !strings.Contains(fmt.Sprint(agents), "not installed") {
		t.Fatalf("the agents show their version, and one not installed: %+v", agents)
	}
	conns := blockData[[]screenRow](t, e.settingsBlock("Connections", nil)["rows"])
	if len(conns) != 1 || conns[0].Cells[0] != "router" {
		t.Fatalf("the connections tab lists the connections: %+v", conns)
	}
	secrets := blockData[[]screenRow](t, e.settingsBlock("Other secrets", nil)["rows"])
	if !strings.Contains(fmt.Sprint(secrets), "wiki") {
		t.Fatalf("the connections tab lists the secrets of tools too: %+v", secrets)
	}
}
