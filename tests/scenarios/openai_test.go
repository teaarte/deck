package scenarios

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"deck2/internal/provider"
)

// notesManifest has an agent command that must not leave the owner's hardware and an external HTTP tool.
const notesManifest = `{"version": 4,
  "collections": [{"name": "notes", "description": "Notes", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}}, "required": ["title"]}}],
  "tools": {"http": [{"name": "post", "description": "Posts a note", "method": "POST", "url": "https://example.invalid/post",
    "class": "external", "body": {"text": "{text}"}, "input": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}}]},
  "commands": [{"name": "collect", "title": "Collect", "description": "Collects notes.", "class": "internal", "requires": ["no_cloud"],
    "executor": {"type": "agent", "prompt": "Collect notes."},
    "result": {"type": "object", "properties": {"written": {"type": "integer"}}, "required": ["written"]}}]}`

func TestAgentOnAnOpenAICompatibleProviderWorksThroughTheGateway(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-from-the-environment-of-deck")
	llm := newLLM(t, func(n int, req chatRequest) chatReply {
		switch n {
		case 1:
			return toolReply(fnCall{"records_upsert", map[string]any{"collection": "notes", "records": `{"title": "a"}`}},
				fnCall{"notes__post", map[string]any{"text": "hi"}})
		case 2:
			return toolReply(fnCall{"result_submit", map[string]any{"result": map[string]any{"written": "one"}}})
		case 3:
			return toolReply(fnCall{"result_submit", map[string]any{"result": map[string]any{"written": 1}}})
		case 4:
			return textReply("Done; the post waits for the owner.")
		case 5:
			return toolReply(fnCall{"result_submit", map[string]any{"result": map[string]any{"written": 2}}})
		}
		return textReply("done")
	})
	e := newEnv(t, nil)
	e.addLLM("local", llm, true)
	p := e.createProject("notes", notesManifest)
	id := e.run(p, "collect")
	if got := e.waitState(id, "waiting_answer", "done", "error"); got.State != "waiting_answer" {
		t.Fatalf("an external call waits for the owner, so the process parks: %+v", got)
	}
	reqs := llm.requests()
	if len(reqs) != 4 {
		t.Fatalf("want four model calls in the first turn, got %d", len(reqs))
	}
	tools := reqs[0].tools()
	for _, want := range []string{"records_upsert", "notes__post", "result_submit", "AskUserQuestion"} {
		if !slices.Contains(tools, want) {
			t.Fatalf("the model gets the gateway tools, %s missing from %v", want, tools)
		}
	}
	if slices.Contains(tools, "approve") {
		t.Fatalf("the permission tool is deck's own, not the model's: %v", tools)
	}
	if reqs[0].Auth != "Bearer sk-local" {
		t.Fatalf("the key comes from the secret, not from deck's environment: %q", reqs[0].Auth)
	}
	if got := toolResults(reqs[1]); !strings.Contains(got, `"written"`) || !strings.Contains(got, "waits for the owner's confirmation") {
		t.Fatalf("records_upsert runs and the external post waits for the owner: %s", got)
	}
	if got := reqs[2].last("tool"); !strings.Contains(got, "does not match its schema") {
		t.Fatalf("a result off the schema gets one request to rebuild: %s", got)
	}
	a := e.waitAsk()
	e.answer(a.ID, "deny:not now")
	got := e.waitState(id, "done", "error")
	if got.State != "done" || got.NotBySchema || string(got.Result) != `{"written":2}` {
		t.Fatalf("after the answer the same session goes on and hands in its result: %+v", got)
	}
	reqs = llm.requests()
	if len(reqs[4].Messages) <= len(reqs[3].Messages) || !strings.Contains(reqs[4].last("user"), "not now") {
		t.Fatalf("the resume continues the stored session with the owner's answer: %v", reqs[4].Messages)
	}
	var calls []map[string]any
	e.must("deck.llm_calls_list", map[string]any{"process": id}, &calls)
	if len(calls) != 6 || calls[0]["gen_ai.provider.name"] != "local" || calls[0]["gen_ai.usage.input_tokens"] != 100.0 ||
		calls[0]["gen_ai.usage.cache_read.input_tokens"] != 40.0 || calls[0]["gen_ai.response.model"] != "fake-model" {
		t.Fatalf("every model call is journaled with its tokens: %v", calls)
	}
}

func toolResults(req chatRequest) string {
	var b strings.Builder
	for _, m := range req.Messages {
		if m["role"] == "tool" {
			fmt.Fprintln(&b, m["content"])
		}
	}
	return b.String()
}

func TestResultSubmitGetsOneRebuild(t *testing.T) {
	cases := []struct {
		name    string
		replies []chatReply
		want    string
		off     bool
	}{
		{"twice off the schema", []chatReply{
			toolReply(fnCall{"result_submit", map[string]any{"result": map[string]any{"written": "one"}}}),
			toolReply(fnCall{"result_submit", map[string]any{"result": map[string]any{"written": "still one"}}}),
			textReply("done")}, `{"written":"still one"}`, true},
		{"no result handed in", []chatReply{textReply("I wrote one note."),
			toolReply(fnCall{"result_submit", map[string]any{"written": 1}}), textReply("done")}, `{"written":1}`, false},
		{"no result after the request", []chatReply{textReply("I wrote one note."), textReply("Still just text.")}, `"Still just text."`, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			llm := newLLM(t, func(n int, req chatRequest) chatReply { return c.replies[min(n, len(c.replies))-1] })
			e := newEnv(t, nil)
			e.addLLM("local", llm, true)
			p := e.createProject("notes", notesManifest)
			got := e.waitState(e.run(p, "collect"), "done", "error")
			if got.State != "done" || got.NotBySchema != c.off || string(got.Result) != c.want {
				t.Fatalf("want %s (off the schema %v), got %+v", c.want, c.off, got)
			}
			if n := len(llm.requests()); n > len(c.replies) {
				t.Fatalf("deck asked to rebuild more than once: %d model calls", n)
			}
		})
	}
}

func TestAgentRunsOnRecordedOllamaReplies(t *testing.T) {
	llm := newLLM(t, func(n int, req chatRequest) chatReply {
		if n == 1 {
			return recorded(t, "ollama_tool_calls.json")
		}
		return recorded(t, "ollama_final.json")
	})
	e := newEnv(t, nil)
	e.addLLM("ollama", llm, true)
	p := e.createProject("notes", notesManifest)
	id := e.run(p, "collect")
	got := e.waitState(id, "done", "error")
	if got.State != "done" || string(got.Result) != `{"written":1}` {
		t.Fatalf("the recorded tool calls write the record and hand in the result: %+v", got)
	}
	var recs []map[string]any
	e.must("deck.records_list", map[string]any{"project": "notes", "collection": "notes"}, &recs)
	if len(recs) != 1 {
		t.Fatalf("want the recorded record, got %v", recs)
	}
	var calls []map[string]any
	e.must("deck.llm_calls_list", map[string]any{"process": id}, &calls)
	if len(calls) != 2 || calls[1]["gen_ai.usage.input_tokens"] != 301.0 || calls[1]["gen_ai.usage.cache_read.input_tokens"] != 300.0 ||
		calls[1]["gen_ai.usage.output_tokens"] != 388.0 || calls[1]["gen_ai.response.model"] != "qwen3:latest" {
		t.Fatalf("tokens come from each reply's usage: %v", calls)
	}
}

func TestProviderRefusalsOfAnOpenAICompatibleAPI(t *testing.T) {
	for _, c := range []struct{ fixture, state string }{{"ratelimit.json", "rate_limited"}, {"unauthorized.json", "needs_login"}} {
		t.Run(c.fixture, func(t *testing.T) {
			llm := newLLM(t, func(int, chatRequest) chatReply { return recorded(t, c.fixture) })
			e := newClockEnv(t, nil)
			e.addLLM("cloud", llm, false)
			p := e.createProject("plain", plainManifest)
			e.must("deck.provider_set", map[string]any{"command": "plain.work", "provider": "cloud"}, nil)
			id := e.run(p, "work")
			if got := e.waitState(id, c.state, "error", "done"); got.State != c.state {
				t.Fatalf("want %s, got %+v", c.state, got)
			}
			if c.state == "rate_limited" {
				pp, _ := e.App.Store.Q.GetProcess(context.Background(), id)
				if want := e.Clock.Now().Add(7200 * 1e9).UnixMilli(); pp.PauseUntilMs.Int64 != want {
					t.Fatalf("Retry-After sets the next try by the deck clock: %d, want %d", pp.PauseUntilMs.Int64, want)
				}
			}
		})
	}
}

func TestNoCloudCommandDoesNotTakeACloudProvider(t *testing.T) {
	local := newLLM(t, func(n int, _ chatRequest) chatReply {
		if n%2 == 0 {
			return textReply("done")
		}
		return toolReply(fnCall{"result_submit", map[string]any{"result": map[string]any{"written": 0}}})
	})
	cloud := newLLM(t, func(int, chatRequest) chatReply { return textReply("cloud") })
	e := newEnv(t, ok)
	e.addLLM("cloud", cloud, false)
	p := e.createProject("notes", notesManifest)
	if code, body := e.call("notes.collect", map[string]any{}); code == 200 || !strings.Contains(string(body), "no provider fits") {
		t.Fatalf("with no provider on own hardware the command does not start: %d %s", code, body)
	}
	if code, body := e.call("deck.provider_set", map[string]any{"command": "notes.collect", "provider": "cloud"}); code == 200 ||
		!strings.Contains(string(body), "no_cloud") {
		t.Fatalf("a cloud provider is not picked for a command that stays off the cloud: %d %s", code, body)
	}
	e.addLLM("local", local, true)
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
	if got := e.waitState(e.run(p, "collect"), "done", "error"); got.State != "done" || len(local.requests()) != 2 {
		t.Fatalf("the command runs on the chosen provider: %+v", got)
	}
	e.addLLM("local", local, false)
	if code, body := e.call("notes.collect", map[string]any{}); code == 200 || !strings.Contains(string(body), "no_cloud") {
		t.Fatalf("a chosen provider that moved to the cloud is refused at the start: %d %s", code, body)
	}
	if len(cloud.requests()) != 0 {
		t.Fatal("nothing reached the cloud provider")
	}
}

func TestProviderIsSwitchedWithoutARestart(t *testing.T) {
	llm := newLLM(t, func(int, chatRequest) chatReply { return textReply("from the API") })
	e := newEnv(t, func(context.Context, *Call) provider.Result { return provider.Result{Text: "from the main provider"} })
	e.addLLM("api", llm, false)
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
	llm := newLLM(t, func(int, chatRequest) chatReply { return textReply("ok") })
	e := newEnv(t, nil)
	e.must("deck.provider_add", map[string]any{"name": "local", "url": llm.URL, "model": "m", "no_cloud": true}, nil)
	p := e.createProject("plain", plainManifest)
	e.must("deck.provider_set", map[string]any{"command": "plain.work", "provider": "local"}, nil)
	e.waitState(e.run(p, "work"), "done")
	if auth := llm.requests()[0].Auth; auth != "" {
		t.Fatalf("deck's own OPENAI_API_KEY never reaches a provider: %q", auth)
	}
}
