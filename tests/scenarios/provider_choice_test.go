package scenarios

import (
	"context"
	"strings"
	"testing"

	"deck2/internal/provider"
)

const notesManifest = `{"version": 5,
  "collections": [{"name": "notes", "description": "Notes", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}}, "required": ["title"]}}],
  "tools": {"http": [{"name": "post", "description": "Posts a note", "method": "POST", "url": "https://example.invalid/post",
    "class": "external", "body": {"text": "{text}"}, "input": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}}]},
  "commands": [{"name": "collect", "title": "Collect", "description": "Collects notes.", "class": "internal", "requires": ["no_cloud"],
    "executor": {"type": "agent", "prompt": "Collect notes."},
    "result": {"type": "object", "properties": {"written": {"type": "integer"}}, "required": ["written"]}}]}`

func TestNoCloudCommandDoesNotTakeACloudProvider(t *testing.T) {
	e, cloud, local := acpPair(t, ok)
	e.addACP(cloud, 1, "cloud", "opencode", acpLLM(t), false)
	p := e.createProject("notes", notesManifest)
	if code, body := e.call("notes.collect", map[string]any{}); code == 200 || !strings.Contains(string(body), "no provider fits") {
		t.Fatalf("with no provider on own hardware the command does not start: %d %s", code, body)
	}
	if code, body := e.call("deck.provider_set", map[string]any{"command": "notes.collect", "provider": "cloud"}); code == 200 ||
		!strings.Contains(string(body), "no_cloud") {
		t.Fatalf("a cloud provider is not picked for a command that stays off the cloud: %d %s", code, body)
	}
	e.addACP(local, 1, "local", "dsh", acpLLM(t), true)
	var list struct {
		Effective string `json:"effective"`
		Providers []struct {
			Name string `json:"name"`
			Fits bool   `json:"fits"`
		} `json:"providers"`
	}
	e.must("deck.providers_list", map[string]any{"command": "notes.collect"}, &list)
	fits := map[string]bool{}
	for _, v := range list.Providers {
		fits[v.Name] = v.Fits
	}
	if list.Effective != "local" || fits["fake"] || fits["cloud"] || !fits["local"] {
		t.Fatalf("only the provider on own hardware fits: %+v", list)
	}
	e.must("deck.provider_set", map[string]any{"command": "notes.collect", "provider": "local"}, nil)
	local.streamText(4, acpTurn(`{"written": 0}`))
	if got := e.waitState(e.run(p, "collect"), "done", "error"); got.State != "done" || local.calls() != 4 {
		t.Fatalf("the command runs on the chosen provider: %+v", got)
	}
	e.addACP(local, 5, "local", "dsh", acpLLM(t), false)
	if code, body := e.call("notes.collect", map[string]any{}); code == 200 || !strings.Contains(string(body), "no_cloud") {
		t.Fatalf("a chosen provider that moved to the cloud is refused at the start: %d %s", code, body)
	}
	if n := cloud.calls(); n != 3 {
		t.Fatalf("nothing but its self-check reached the cloud provider: %d calls", n)
	}
}

func TestProviderThatLeftTheOwnersHardwareStopsTheNextTurn(t *testing.T) {
	e, _, local := acpPair(t, nil)
	e.addACP(local, 1, "acp", "dsh", acpLLM(t), true)
	local.streamText(4, acpTurn(`{"written": 1}`))
	id := e.run(e.acpProject("notes", notesManifest), "collect")
	e.waitState(id, "done")
	e.addACP(local, 5, "acp", "dsh", acpLLM(t), false)
	e.must("deck.process_rework", map[string]any{"process": id, "remark": "one more"}, nil)
	got := e.waitState(id, "error", "done")
	if got.State != "error" || !strings.Contains(got.Error, "no longer fits") || local.calls() != 7 {
		t.Fatalf("a session that must stay off the cloud does not go on once its provider moved there: %+v, %d calls", got, local.calls())
	}
}

func TestProviderIsSwitchedWithoutARestart(t *testing.T) {
	e, api, _ := acpPair(t, func(context.Context, *Call) provider.Result { return provider.Result{Text: "from the main provider"} })
	api.streamText(0, acpTurn(`"from the API"`))
	e.addACP(api, 1, "api", "opencode", acpLLM(t), false)
	p := e.createProject("plain", plainManifest)
	if got := e.waitState(e.run(p, "work"), "done"); string(got.Result) != `"from the main provider"` {
		t.Fatalf("without a setting the main provider runs: %+v", got)
	}
	e.must("deck.provider_set", map[string]any{"command": "plain.work", "provider": "api"}, nil)
	if got := e.waitState(e.run(p, "work"), "done"); string(got.Result) != `"from the API"` {
		t.Fatalf("the setting takes effect at the next start: %+v", got)
	}
	var list struct {
		Chosen string `json:"chosen"`
	}
	e.must("deck.providers_list", map[string]any{"command": "plain.work"}, &list)
	e.must("deck.provider_set", map[string]any{"command": "plain.work", "provider": ""}, nil)
	if got := e.waitState(e.run(p, "work"), "done"); list.Chosen != "api" || string(got.Result) != `"from the main provider"` {
		t.Fatalf("an empty setting goes back to the default: %+v %+v", list, got)
	}
	if code, _ := e.call("deck.provider_set", map[string]any{"command": "plain.nothing", "provider": "api"}); code == 200 {
		t.Fatal("only agent commands take a provider")
	}
	suggests := strings.Replace(plainManifest, `"class": "internal",`, `"class": "internal", "provider": "api",`, 1)
	s := e.createProject("suggests", suggests)
	if got := e.waitState(e.run(s, "work"), "done"); string(got.Result) != `"from the API"` {
		t.Fatalf("without a setting the provider the manifest suggests runs: %+v", got)
	}
}

func TestProviderWithoutAKeySendsNone(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-from-the-environment-of-deck")
	e, local, _ := acpPair(t, nil)
	local.selfCheck()
	local.streamText(4, acpTurn(`"ok"`))
	llm := acpLLM(t)
	e.must("deck.provider_add", map[string]any{"name": "acp", "url": llm.URL, "model": "m", "agent": "opencode", "no_cloud": true}, nil)
	e.waitFor("the self-check", func() bool {
		h, err := e.App.Store.Q.GetHealth(t.Context(), "provider:acp")
		return err == nil && h.Status == "ok"
	})
	e.waitState(e.run(e.acpProject("plain", plainManifest), "work"), "done")
	for _, r := range llm.requests() {
		if r.Auth != "" {
			t.Fatalf("deck's own OPENAI_API_KEY never reaches a provider: %q", r.Auth)
		}
	}
	if code, body := e.call("deck.provider_add", map[string]any{"name": "bare", "url": llm.URL, "model": "m"}); code != 422 ||
		!strings.Contains(string(body), "agent") {
		t.Fatalf("a provider runs in an ACP agent: %d %s", code, body)
	}
}

func TestResultSubmitGetsOneRebuild(t *testing.T) {
	e, local, _ := acpPair(t, nil)
	e.addACP(local, 1, "acp", "opencode", acpLLM(t), true)
	local.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"written": "one"}}}}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"written": "still one"}}}}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"written": 3}}}}`, `{"end": "end_turn"}`))
	got := e.waitState(e.run(e.acpProject("notes", notesManifest), "collect"), "done", "error")
	if got.State != "done" || !got.NotBySchema || string(got.Result) != `{"written":"still one"}` {
		t.Fatalf("one rebuild, then the result stays marked as off the schema and nothing more is taken: %+v", got)
	}
}
