package scenarios

import (
	"os"
	"path/filepath"
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
