package scenarios

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/provider/codex"
)

type agentsV struct {
	Agents []struct{ Name, Status string }
	Models []struct {
		Name, Status string
		Builtin      bool
	}
}

func (e *Env) agentStatus(name string) (agent, model string) {
	var v agentsV
	e.must("deck.models_list", nil, &v)
	for _, a := range v.Agents {
		if a.Name == name {
			agent = a.Status
		}
	}
	for _, m := range v.Models {
		if m.Name == name {
			model = m.Status
		}
	}
	return agent, model
}

func (e *Env) healthSubjects() []string {
	var hs []struct{ Subject string }
	e.must("deck.health_list", nil, &hs)
	var out []string
	for _, h := range hs {
		out = append(out, h.Subject)
	}
	return out
}

// TestACLIThatIsNotInstalledIsOffByItself: a built-in CLI deck cannot find is not checked, has no red row and takes
// no work; the owner can say so of none.
func TestACLIThatIsNotInstalledIsOffByItself(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	missing := &codex.Adapter{Binary: filepath.Join(dir, "codex"), Home: home.Layout(dir), Dir: home.Layout(dir).Codex(), Linger: time.Second}
	main := &Fake{script: func(context.Context, *Call) provider.Result { return provider.Result{Text: "ok"} }}
	e := newEnvBuiltin(t, dir, []provider.Provider{main, missing})
	e.App.Providers.Checks(context.Background(), time.Now())
	e.waitFor("codex to be missing, as an agent and as a model", func() bool {
		a, m := e.agentStatus("codex")
		return a == "missing" && m == "missing"
	})
	if slices.Contains(e.healthSubjects(), "provider:codex") {
		t.Fatalf("a missing CLI has no row of health: %v", e.healthSubjects())
	}
	var topics struct{ Models []string }
	e.must("deck.chat_topics", nil, &topics)
	if slices.Contains(topics.Models, "codex") {
		t.Fatalf("a missing CLI is not offered: %v", topics.Models)
	}
}

// TestTheOwnerTurnsAProviderOffAndOnAgain: a provider turned off is not checked, offered or given work, an assignment
// to it says why; turned on, it is checked again.
func TestTheOwnerTurnsAProviderOffAndOnAgain(t *testing.T) {
	t.Parallel()
	c := newCodex(t)
	for i, f := range []string{"selfcheck.jsonl", "selfcheck_resume.jsonl", "selfcheck_offline.jsonl"} {
		c.stream(i+1, f)
		c.stream(i+4, f)
	}
	e, _ := codexEnv(t, c)
	e.App.Providers.Checks(context.Background(), time.Now())
	e.waitFor("the first self-check of codex", func() bool { a, _ := e.agentStatus("codex"); return a == "ok" })
	e.must("deck.provider_off", map[string]any{"provider": "codex", "off": true}, nil)
	if a, m := e.agentStatus("codex"); a != "off" || m != "off" {
		t.Fatalf("a provider turned off says so: %q %q", a, m)
	}
	if slices.Contains(e.healthSubjects(), "provider:codex") {
		t.Fatalf("a provider turned off keeps no row of health: %v", e.healthSubjects())
	}
	p := e.createProject("paint", colorManifest)
	e.useModels("paint.work", "codex")
	if code, out := e.call("paint.work", map[string]any{}); code == 200 || !strings.Contains(string(out), "turned off in Settings → Models") {
		t.Fatalf("work assigned to a provider turned off is refused with why: %d %s", code, out)
	}
	_ = p
	e.refused("deck.provider_off", map[string]any{"provider": "nope", "off": true}, 422, "no built-in provider")
	before := c.calls()
	e.must("deck.provider_off", map[string]any{"provider": "codex", "off": false}, nil)
	e.waitFor("turned on again, the provider is checked", func() bool { a, _ := e.agentStatus("codex"); return c.calls() > before && a == "ok" })
}
