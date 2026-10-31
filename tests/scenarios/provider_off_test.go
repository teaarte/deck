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

func (e *Env) healthSubjects() []string {
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
	main := &Fake{script: func(context.Context, *Call) provider.Result { return provider.Result{Text: "ok"} }}
	e := New(t, WithHome(dir), WithProvider(main, missing))
	e.App.Providers.Checks(context.Background(), time.Now())
	e.Until("codex to be missing, as an agent and as a model", func() bool {
		a, m := e.agentStatus("codex")
		return a == "missing" && m == "missing"
	})
	if slices.Contains(e.healthSubjects(), "provider:codex") {
		t.Fatalf("a missing CLI has no row of health: %v", e.healthSubjects())
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
	c := newCodex(t)
	for i, f := range []string{"selfcheck.jsonl", "selfcheck_resume.jsonl", "selfcheck_offline.jsonl"} {
		c.stream(i+1, f)
		c.stream(i+4, f)
	}
	e := New(t, WithCodex(c))
	e.App.Providers.Checks(context.Background(), time.Now())
	e.Until("the first self-check of codex", func() bool { a, _ := e.agentStatus("codex"); return a == "ok" })
	e.Must("deck.provider_off", map[string]any{"provider": "codex", "off": true}, nil)
	if a, m := e.agentStatus("codex"); a != "off" || m != "off" {
		t.Fatalf("a provider turned off says so: %q %q", a, m)
	}
	if slices.Contains(e.healthSubjects(), "provider:codex") {
		t.Fatalf("a provider turned off keeps no row of health: %v", e.healthSubjects())
	}
	p := e.Project("paint", colorManifest)
	e.UseModels("paint.work", "codex")
	if code, out := e.Call("paint.work", map[string]any{}); code == 200 || !strings.Contains(string(out), "turned off in Settings → Models") {
		t.Fatalf("work assigned to a provider turned off is refused with why: %d %s", code, out)
	}
	_ = p
	e.Refused("deck.provider_off", map[string]any{"provider": "nope", "off": true}, 422, "no built-in provider")
	before := c.calls()
	e.Must("deck.provider_off", map[string]any{"provider": "codex", "off": false}, nil)
	e.Until("turned on again, the provider is checked", func() bool { a, _ := e.agentStatus("codex"); return c.calls() > before && a == "ok" })
}
