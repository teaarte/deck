package scenarios

import (
	"context"
	"deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/provider/codex"
	"deck2/tests/harness"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func agentStatus(e *harness.Env, name string) (agent, model string) {
	var v harness.AgentsV
	e.Must("deck.models_list", nil, &v)
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

func healthSubjects(e *harness.Env) []string {
	var out []string
	for _, h := range e.Health() {
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
	main := &harness.Fake{Script: func(context.Context, *harness.Call) provider.Result { return provider.Result{Text: "ok"} }}
	e := harness.New(t, harness.WithHome(dir), harness.WithProvider(main, missing))
	e.App.Providers.Checks(context.Background(), time.Now())
	e.Until("codex to be missing, as an agent and as a model", func() bool {
		a, m := agentStatus(e, "codex")
		return a == "missing" && m == "missing"
	})
	if slices.Contains(healthSubjects(e), "provider:codex") {
		t.Fatalf("a missing CLI has no row of health: %v", healthSubjects(e))
	}
	var topics struct{ Models []string }
	e.Must("deck.chat_topics", nil, &topics)
	if slices.Contains(topics.Models, "codex") {
		t.Fatalf("a missing CLI is not offered: %v", topics.Models)
	}
}

// TestTheOwnerTurnsAProviderOffAndOnAgain: a provider turned off is not checked, offered or given work, an assignment
// to it says why; turned on, it is checked again.
func TestTheOwnerTurnsAProviderOffAndOnAgain(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	for i, f := range []string{"selfcheck.jsonl", "selfcheck_resume.jsonl", "selfcheck_offline.jsonl"} {
		c.Stream(i+1, f)
		c.Stream(i+4, f)
	}
	e := harness.New(t, harness.WithCodex(c))
	e.App.Providers.Checks(context.Background(), time.Now())
	e.Until("the first self-check of codex", func() bool { a, _ := agentStatus(e, "codex"); return a == "ok" })
	e.Must("deck.provider_off", map[string]any{"provider": "codex", "off": true}, nil)
	if a, m := agentStatus(e, "codex"); a != "off" || m != "off" {
		t.Fatalf("a provider turned off says so: %q %q", a, m)
	}
	if slices.Contains(healthSubjects(e), "provider:codex") {
		t.Fatalf("a provider turned off keeps no row of health: %v", healthSubjects(e))
	}
	p := e.Project("paint", colorManifest)
	e.UseModels("paint.work", "codex")
	if code, out := e.Call("paint.work", map[string]any{}); code == 200 || !strings.Contains(string(out), "turned off in Settings → Models") {
		t.Fatalf("work assigned to a provider turned off is refused with why: %d %s", code, out)
	}
	_ = p
	e.Refused("deck.provider_off", map[string]any{"provider": "nope", "off": true}, 422, "no built-in provider")
	before := c.Calls()
	e.Must("deck.provider_off", map[string]any{"provider": "codex", "off": false}, nil)
	e.Until("turned on again, the provider is checked", func() bool { a, _ := agentStatus(e, "codex"); return c.Calls() > before && a == "ok" })
}
