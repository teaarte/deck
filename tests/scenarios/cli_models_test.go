package scenarios

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// args reads the arguments of call n of the fake CLI, and the value of a flag in them.
func (c *fakeCLI) argOf(n int, name string) string {
	c.t.Helper()
	v, _ := flag(c.call(n).args, name)
	return v
}

// TestAModelOfClaudeCodeRunsItsTurnsOnItsModelAndEffort: model_set checks a model of claude-code against what the CLI
// lists and refuses one without auto mode; its self-check runs only on the button, and it and turns run with --model and
// --effort, on the subscription.
func TestAModelOfClaudeCodeRunsItsTurnsOnItsModelAndEffort(t *testing.T) {
	t.Parallel()
	c := newCLI(t)
	c.models("models.jsonl")
	c.checksPass()
	c.stream(0, "ok.jsonl")
	e, _ := cliEnv(t, c)
	var list struct {
		Connections []struct{ Name, API, URL string }
	}
	e.must("deck.models_list", nil, &list)
	if !slices.ContainsFunc(list.Connections, func(c struct{ Name, API, URL string }) bool {
		return c.Name == "claude-code" && c.API == "cli" && c.URL == ""
	}) {
		t.Fatalf("deck keeps the connection of the CLI itself: %+v", list.Connections)
	}
	set := func(model, options string) map[string]any {
		in := map[string]any{"connection": "claude-code", "model": model, "name": "cc-" + strings.ReplaceAll(model, "[", "")}
		if options != "" {
			in["options"] = options
		}
		return in
	}
	e.refused("deck.model_set", set("haiku", ""), 422, "no auto mode on this model")
	e.refused("deck.model_set", set("sonnet", `{"effort": "turbo"}`), 422, `takes effort low, medium, high, xhigh, max in claude-code, not "turbo"`)
	e.refused("deck.model_set", set("sonnet", `{"effort": "low", "temperature": 0}`), 422, "effort alone")
	e.refused("deck.model_set", set("gpt-6-sol", ""), 422, "claude-code lists no model")
	e.refused("deck.model_set", map[string]any{"connection": "claude-code", "model": "sonnet", "name": "cc-x", "agent": "goose"}, 422, "run in claude-code, not goose")
	e.refused("deck.connection_set", map[string]any{"name": "claude-code", "api": "openai", "url": "https://example.com/v1"}, 422, "deck keeps itself")
	e.refused("deck.connection_remove", map[string]any{"name": "claude-code"}, 422, "deck keeps itself")

	var m struct {
		Agent        string
		Subscription bool
		Price        string
	}
	e.must("deck.model_set", map[string]any{"connection": "claude-code", "model": "sonnet", "name": "sonnet-low", "options": `{"effort": "low"}`}, &m)
	if m.Agent != "claude-code" || !m.Subscription {
		t.Fatalf("a model of the CLI runs in it on the subscription: %+v", m)
	}
	onModel := func() int {
		for i := c.calls(); i >= 1; i-- {
			if c.argOf(i, "--model") == "sonnet" {
				return i
			}
		}
		return -1
	}
	unchecked := func(when string) {
		t.Helper()
		var n int
		_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM health WHERE subject = 'provider:sonnet-low'`).Scan(&n)
		if i := onModel(); i >= 0 || n != 0 {
			t.Fatalf("%s, a model of the CLI is not checked: it spends turns of the subscription (call %d, %d rows)", when, i, n)
		}
	}
	unchecked("on model_set")
	e.App.Providers.Checks(t.Context(), time.Now())
	e.waitHealth("provider:claude-code", "ok")
	unchecked("at start")
	e.must("deck.provider_check", map[string]any{"provider": "sonnet-low"}, nil)
	if h := e.waitHealth("provider:sonnet-low", "ok"); h.Version != "2.1.281" {
		t.Fatalf("its self-check on the button runs the CLI on the model: %+v", h)
	}
	if i := onModel(); i < 0 || c.argOf(i, "--effort") != "low" {
		t.Fatalf("the self-check runs on the model and effort: %d", i)
	}
	e.refused("deck.assignment_set", map[string]any{"role": "text", "models": []string{"sonnet-low"}}, 422, "runs agent turns in claude-code")

	p := e.createProject("plain", plainManifest)
	e.useModels("plain.work", "sonnet-low")
	before := c.calls()
	id := e.run(p, "work")
	if got := e.waitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the turn runs on the model: %+v", got)
	}
	n := before + 1
	if c.argOf(n, "--model") != "sonnet" || c.argOf(n, "--effort") != "low" {
		t.Fatalf("the turn runs the CLI with --model and --effort: %v", c.call(n).args)
	}
	var calls []map[string]any
	e.modelCalls("", id, &calls)
	var sub int
	_ = e.App.Store.DB.QueryRow(`SELECT subscription FROM llm_call WHERE process_id = ?`, id).Scan(&sub)
	if len(calls) != 1 || calls[0]["gen_ai.provider.name"] != "sonnet-low" || sub != 1 {
		t.Fatalf("its spend goes by the model, on the subscription: %v %d", calls, sub)
	}
	var views struct {
		Models []struct{ Name, Price string }
	}
	e.must("deck.models_list", nil, &views)
	if i := slices.IndexFunc(views.Models, func(v struct{ Name, Price string }) bool { return v.Name == "sonnet-low" }); i < 0 || views.Models[i].Price != "subscription" {
		t.Fatalf("Settings → Models lists it by the subscription: %+v", views.Models)
	}
	var cmds []struct {
		Name    string
		Choices []string
	}
	e.must("deck.commands_list", map[string]any{"project": "plain"}, &cmds)
	if i := slices.IndexFunc(cmds, func(c struct {
		Name    string
		Choices []string
	}) bool {
		return strings.HasSuffix(c.Name, ".work")
	}); i < 0 || !slices.Contains(cmds[i].Choices, "sonnet-low") {
		t.Fatalf("the picker of the command offers it: %+v", cmds)
	}

	e.refused("deck.model_call", map[string]any{"kind": "text", "model": "sonnet-low", "prompt": "hi"}, 422, "its models run agent turns")
	e.must("deck.connection_set", map[string]any{"name": "router", "api": "openai", "url": "http://127.0.0.1:9/v1"}, nil)
	e.refused("deck.model_set", map[string]any{"connection": "router", "model": "m", "name": "sonnet-low"}, 422, "is a model of claude-code")
	e.must("deck.model_set", map[string]any{"connection": "router", "model": "m", "name": "txt", "kind": "text", "free": true}, nil)
	e.must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"txt"}}, nil)
	e.must("deck.model_set", map[string]any{"connection": "claude-code", "model": "sonnet", "name": "txt"}, nil)
	e.refused("deck.model_call", map[string]any{"kind": "text", "prompt": "hi"}, 422, "no model of @text")
	var named struct{ Name string }
	e.must("deck.model_set", map[string]any{"connection": "claude-code", "model": "opus[1m]"}, &named)
	if named.Name != "claude-code-opus" {
		t.Fatalf("a model of a CLI is named after its connection and model when left out: %q", named.Name)
	}
}

// TestAClaudeCodeModelAtItsLimitHandsTheWorkOnToTheNextOne: opus at its limit hands the work on to sonnet, each
// with its own --model.
func TestAClaudeCodeModelAtItsLimitHandsTheWorkOnToTheNextOne(t *testing.T) {
	t.Parallel()
	c := newCLI(t)
	c.models("models.jsonl")
	c.checksPass()
	raw, _ := os.ReadFile("testdata/claude/ratelimit_rejected.jsonl")
	if err := os.WriteFile(filepath.Join(c.dir, "stream.opus"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	c.stream(0, "ok.jsonl")
	e, _ := cliEnv(t, c)
	e.must("deck.model_set", map[string]any{"connection": "claude-code", "model": "opus", "name": "opus-high", "options": `{"effort": "high"}`}, nil)
	e.must("deck.model_set", map[string]any{"connection": "claude-code", "model": "sonnet", "name": "sonnet-low", "options": `{"effort": "low"}`}, nil)
	p := e.createProject("plain", plainManifest)
	e.useModels("plain.work", "opus-high", "sonnet-low")
	before := c.calls()
	id := e.run(p, "work")
	if got := e.waitState(id, "error", "done", "rate_limited"); got.State != "error" || e.fault(id).ErrorType != "model.limit" {
		t.Fatalf("opus at its limit fails as the model's: %+v %+v", got, e.fault(id))
	}
	next := e.following(id)
	var in struct{ Provider string }
	e.must("deck.process_get", map[string]any{"process": next}, &in)
	if got := e.waitState(next, "done", "error"); got.State != "done" || in.Provider != "sonnet-low" {
		t.Fatalf("sonnet goes on with the work: %+v %+v", got, in)
	}
	if c.argOf(before+1, "--model") != "opus" || c.argOf(before+2, "--model") != "sonnet" || c.argOf(before+2, "--effort") != "low" {
		t.Fatalf("each turn runs on its own model: %v %v", c.call(before+1).args, c.call(before+2).args)
	}
}

// TestAModelOfCodexRunsItsTurnsOnItsModelAndEffort: a model of the codex connection is checked against model/list
// of the app-server, and its turns start with its model and effort.
func TestAModelOfCodexRunsItsTurnsOnItsModelAndEffort(t *testing.T) {
	t.Parallel()
	c := newCodex(t)
	for i, s := range []string{"models.jsonl", "models.jsonl", "models.jsonl", "selfcheck.jsonl", "selfcheck_resume.jsonl", "selfcheck_offline.jsonl"} {
		c.stream(i+1, s)
	}
	c.stream(0, "ok.jsonl")
	e, _ := codexEnv(t, c)
	e.refused("deck.model_set", map[string]any{"connection": "codex", "model": "gpt-6-luna", "name": "luna-min", "options": `{"effort": "minimal"}`}, 422,
		`takes effort low, medium, high, xhigh, max in codex, not "minimal"`)
	e.refused("deck.model_set", map[string]any{"connection": "codex", "model": "sonnet", "name": "codex-sonnet"}, 422, "codex lists no model \"sonnet\"; it lists gpt-6-astra")
	var m struct{ Agent string }
	e.must("deck.model_set", map[string]any{"connection": "codex", "model": "gpt-6-luna", "name": "luna-low", "options": `{"effort": "low"}`}, &m)
	if m.Agent != "codex" {
		t.Fatalf("a model of the codex connection runs in codex: %+v", m)
	}
	e.must("deck.provider_check", map[string]any{"provider": "luna-low"}, nil)
	e.waitHealth("provider:luna-low", "ok")
	p := e.createProject("paint", colorManifest)
	e.useModels("paint.work", "luna-low")
	id := e.run(p, "work")
	if got := e.waitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the turn runs on the model: %+v", got)
	}
	cc := c.call(7)
	start := cc.request("turn/start")
	if string(start["model"]) != `"gpt-6-luna"` || string(start["effort"]) != `"low"` || cc.violation != "" {
		t.Fatalf("turn/start carries the model and its effort: %s %s %s", start["model"], start["effort"], cc.violation)
	}
	if s := c.call(4).request("turn/start"); string(s["model"]) != `"gpt-6-luna"` {
		t.Fatalf("the self-check runs on the model: %s", s["model"])
	}
	if s := cc.request("thread/start"); string(s["model"]) != `"gpt-6-luna"` {
		t.Fatalf("the thread starts on the model: %s", s["model"])
	}
	var calls []map[string]any
	e.modelCalls("", id, &calls)
	if len(calls) == 0 || calls[0]["gen_ai.provider.name"] != "luna-low" {
		t.Fatalf("its spend goes by the model: %v", calls)
	}
}
