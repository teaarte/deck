package providers

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/health"
	deckhome "deck2/internal/home"
	"deck2/internal/sandbox"
	"deck2/internal/store/db"
	"deck2/tests/harness"
)

// TestACPSelfCheckTellsWhyNoResultCame: a first turn without result_submit is told apart: the API refused what the
// agent sent or its tools, the model made no tool call, or it forgot result_submit, which it is reminded of once.
func TestACPSelfCheckTellsWhyNoResultCame(t *testing.T) {
	t.Parallel()
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(harness.Testdata("acp"), name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	first, second, third := read("selfcheck_1.jsonl"), read("selfcheck_2.jsonl"), read("selfcheck_3.jsonl")
	submit := `{"mcp": {"tool": "result_submit", "arguments": {"result": {"word": "{{word}}", "answer": "{{word}}-answered"}}}}` + "\n"
	forgot := strings.Replace(first, submit, "", 1)
	words := harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, harness.AcpModel,
		`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "The word is written."}}}`, `{"end": "end_turn"}`)
	for _, tc := range []struct {
		name    string
		refuse  string
		streams []string
		status  string
		wants   []string
	}{
		{"format", `'messages.2' : property 'foo' is unsupported`, []string{forgot}, "error",
			[]string{"self-check of goose", "the model API refused what goose sent (400", "property 'foo'", "do not fit"}},
		{"unknown model", "404:The model `m` does not exist or you do not have access to it.", []string{forgot}, "error",
			[]string{"self-check of goose", "does not know the model (404", "check the model id"}},
		{"no tools", `tool calling is not supported for this model`, []string{forgot}, "error",
			[]string{"self-check of goose", "refused a call with tools", "may not take tool calls"}},
		{"a field of tools", `'messages.3' : property 'tool_call_id' is unsupported`, []string{forgot}, "error",
			[]string{"the model API refused what goose sent (400", "tool_call_id", "do not fit"}},
		{"tools of the model", `'tools' is not supported with this model`, []string{forgot}, "error", []string{"refused a call with tools"}},
		{"words", "", []string{words}, "error", []string{"the model does not manage", "made no tool call", "The word is written."}},
		{"forgot twice", "", []string{forgot, strings.Replace(second, submit, "", 1)}, "error",
			[]string{"the model does not manage", "reminded of result_submit", "without calling result_submit"}},
		{"reminded", "", []string{forgot, second, second, third}, "ok", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := harness.NewACP(t)
			for i, s := range tc.streams {
				f.StreamText(i+1, s)
			}
			llm := harness.NewLLM(t, func(int, harness.ChatRequest) harness.ChatReply {
				if tc.refuse != "" {
					status, words := 400, tc.refuse
					if w, ok := strings.CutPrefix(words, "404:"); ok {
						status, words = 404, w
					}
					b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": words, "type": "invalid_request_error"}})
					return harness.ChatReply{Status: status, Body: string(b)}
				}
				return harness.TextReply("ok")
			})
			h := harness.New(t, harness.WithACP(f, nil)).AddACP(f, llm)
			if h.Status != tc.status {
				t.Fatalf("want %s: %+v", tc.status, h)
			}
			for _, w := range tc.wants {
				if !strings.Contains(h.Error, w) {
					t.Fatalf("want %q in: %s", w, h.Error)
				}
			}
			if tc.refuse != "" && strings.Contains(h.Error, "stronger") {
				t.Fatalf("a refusal of the API is no weak model: %s", h.Error)
			}
			if tc.name == "reminded" {
				rpc, _ := json.Marshal(f.Call(2).Rpc)
				if !strings.Contains(string(rpc), "without handing in the result") {
					t.Fatalf("the second turn reminds of result_submit: %s", rpc)
				}
			}
		})
	}
}

// gooseInit is the initialize of Goose: no session/resume, session/load instead.
const gooseInit = `{"reply": "initialize", "result": {"protocolVersion": 1, "agentCapabilities": {"loadSession": true, "mcpCapabilities": {"http": true}, "sessionCapabilities": {"list": {}, "close": {}}}, "authMethods": []}}`

var writtenManifest = harness.Manifest(`{"commands": [{"name": "work", "title": "Work", "description": "Writes a file", "class": "internal",
    "result": {"type": "object", "required": ["written"], "properties": {"written": {"type": "integer"}}},
    "executor": {"type": "agent", "prompt": "Write the file."}}]}`)

// TestACPCommandAgentWorksThroughTheGateway: an ACP agent in the sandbox of deck writes in its folder, cannot read
// what deck hides, calls the model with the key only deck's proxy knows and hands its result in with result_submit.
func TestACPCommandAgentWorksThroughTheGateway(t *testing.T) {
	f := harness.NewACP(t)
	llm := harness.AcpLLM(t)
	t.Setenv("DEEPSEEK_API_KEY", "sk-from-the-environment-of-deck")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	e := harness.New(t, harness.WithACP(f, llm), harness.WithConfig(func(c *app.Config) {
		state := deckhome.Layout(c.Home).State()
		if err := errors.Join(os.MkdirAll(state, 0o700), os.WriteFile(harness.DeckSecretPath(c.Home), []byte("owner-token-value"), 0o600),
			os.WriteFile(filepath.Join(f.Dir, "probe"), []byte(harness.DeckSecretPath(c.Home)), 0o600)); err != nil {
			t.Fatal(err)
		}
	}))

	home := e.App.Cfg.Home
	f.StreamText(4, strings.Join([]string{harness.AcpInit,
		`{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Painting."}}}`,
		`{"shell": "echo painted > canvas.txt"}`,
		`{"shell": "cat ` + harness.DeckSecretPath(home) + `"}`,
		`{"shell": "echo tampered > \"$HOME/../session\""}`,
		harness.AcpModel,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`,
		`{"end": "end_turn"}`}, "\n"))
	id := e.Run(e.AcpProject("paint", harness.ColorManifest), "work")
	got := e.WaitState(id, "done", "error")
	c := f.Call(4)
	if got.State != "done" || string(got.Result) != `{"color":"blue"}` || c.Violation != "" {
		t.Fatalf("the result comes in by the schema with result_submit: %+v %s", got, c.Violation)
	}
	if b, _ := os.ReadFile(filepath.Join(c.Cwd, "canvas.txt")); string(b) != "painted\n" {
		t.Fatalf("the agent writes in its own folder: %q", b)
	}
	session, _ := os.ReadFile(filepath.Join(filepath.Dir(c.Env["HOME"]), "session"))
	if len(c.Shell) != 3 || strings.Contains(c.Shell[1]["out"], "owner-token-value") || string(session) != "s-1" {
		t.Fatalf("the sandbox of deck keeps the owner's token and deck's session file from the agent: %v %q", c.Shell, session)
	}
	reqs := llm.Requests()
	if len(reqs) != 4 || reqs[3].Auth != "Bearer sk-real-model-key" || len(c.Model) != 1 || c.Model[0]["status"] != 200.0 {
		t.Fatalf("the model call goes through deck's proxy, which puts the key in: %v %v", reqs, c.Model)
	}
	for k, v := range c.Env {
		if strings.Contains(v, "sk-real-model-key") || strings.Contains(v, "sk-from-the-environment") || strings.HasPrefix(k, "XDG_") {
			t.Fatalf("the agent sees no key and no settings of the owner: %s=%s", k, v)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(f.Dir, "probe.out")); len(b) == 0 || string(b) == "<nil>" {
		t.Fatalf("even the version of the agent is asked inside the sandbox: %q", b)
	}
	if h := c.Env["HOME"]; !strings.HasPrefix(h, filepath.Join(deckhome.Layout(home).State(), "acp", "sessions")) {
		t.Fatalf("the agent lives in a home of its own session: %s", h)
	}
	var calls []map[string]any
	e.ModelCalls("", id, &calls)
	if len(calls) != 1 || calls[0]["gen_ai.provider.name"] != "acp" || calls[0]["gen_ai.usage.input_tokens"] != 100.0 ||
		calls[0]["gen_ai.usage.cache_read.input_tokens"] != 40.0 || calls[0]["gen_ai.response.model"] != "fake-model" {
		t.Fatalf("deck's proxy journals the tokens of each model call: %v", calls)
	}
	var params struct {
		McpServers []struct{ URL string } `json:"mcpServers"`
	}
	_ = json.Unmarshal(c.Request("session/new"), &params)
	if len(params.McpServers) != 1 || strings.HasPrefix(params.McpServers[0].URL, e.URL) {
		t.Fatalf("the gateway is the one MCP server of the session, reached through the entry of the sandbox: %+v", params)
	}
}

// TestACPQuestionStopsTheTurnBeforeTheAgentTimesOutAndTheAnswerResumes: an ACP agent gives a tool call 60 s, so a
// question waits less, the turn ends and the owner's answer comes back through session/load of the same session.
func TestACPQuestionStopsTheTurnBeforeTheAgentTimesOutAndTheAnswerResumes(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	f.StreamText(4, harness.AcpStream(gooseInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Hi."}}}`,
		`{"update": {"sessionUpdate": "agent_thought_chunk", "content": {"type": "text", "text": "Read "}}}`,
		`{"update": {"sessionUpdate": "agent_thought_chunk", "content": {"type": "text", "text": "first."}}}`,
		`{"update": {"sessionUpdate": "tool_call", "toolCallId": "c1", "title": "read", "kind": "read", "status": "pending"}}`,
		`{"update": {"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "in_progress", "rawInput": {"path": "a.txt"}}}`,
		`{"update": {"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "completed", "rawInput": {"path": "a.txt"}, "rawOutput": {"output": "text of a"}}}`,
		`{"mcp": {"tool": "ask", "arguments": {"question": "Which color should the report use?"}}}`, `{"end": "end_turn"}`))
	f.StreamText(5, harness.AcpStream(gooseInit, `{"reply": "session/load", "result": {}, "replay": [{"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Hi again."}}]}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)), harness.WithConfig(func(c *app.Config) { c.QuestionWait = 10 * time.Minute }))
	id := e.Run(e.AcpProject("paint", harness.ColorManifest), "work")
	a := e.WaitAsk()
	if a.Kind != "question" || !strings.Contains(a.Text, "Which color") {
		t.Fatalf("the question reaches the owner: %+v", a)
	}
	e.WaitState(id, "waiting_answer")
	if m := f.Call(4).Mcp; len(m) != 1 || !strings.Contains(m[0]["text"].(string), "not answered yet") {
		t.Fatalf("the agent hears in time that the owner has not answered and ends its turn: %v", m)
	}
	lines := e.Log(id)
	kinds := make([]string, 0, len(lines))
	for _, l := range lines {
		kinds = append(kinds, fmt.Sprint(l["kind"]))
	}
	if len(kinds) < 5 || !slices.Equal(kinds[:5], []string{"message", "thought", "tool", "tool", "tool_update"}) || lines[0]["text"] != "Hi." ||
		lines[1]["text"] != "Read first." || lines[2]["id"] != "c1" || lines[2]["title"] != "read" || lines[3]["text"] != `{"path":"a.txt"}` ||
		lines[4]["id"] != "c1" || lines[4]["status"] != "completed" || lines[4]["text"] != `{"output":"text of a"}` {
		t.Fatalf("the log keeps the session updates of ACP: a thought whole, the call, its late input and its end: %v", lines)
	}
	e.Answer(a.ID, "blue")
	got := e.WaitState(id, "done", "error")
	c := f.Call(5)
	var resume struct{ SessionID string }
	_ = json.Unmarshal(c.Request("session/load"), &resume)
	if got.State != "done" || string(got.Result) != `{"color":"blue"}` || resume.SessionID != "s-1" || c.Violation != "" {
		t.Fatalf("the answer resumes the session the agent opened: %+v %+v %s", got, resume, c.Violation)
	}
	if !strings.Contains(string(c.Request("session/prompt")), "blue") {
		t.Fatalf("the resume brings the answer: %s", c.Request("session/prompt"))
	}
	for _, l := range e.Log(id) {
		if l["text"] == "Hi again." {
			t.Fatalf("the history a load replays is not logged again: %v", l)
		}
	}
}

// TestACPAgentPermissionIsAllowedOnceWithoutTheOwner: inside the sandbox of deck the agent's own questions about its
// tools are answered with allow_once; "always" stays the owner's button.
func TestACPAgentPermissionIsAllowedOnceWithoutTheOwner(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"permission": {"toolCall": {"toolCallId": "c1", "title": "cat notes", "kind": "execute"}, "options": [{"optionId": "always", "name": "Always", "kind": "allow_always"}, {"optionId": "once", "name": "Once", "kind": "allow_once"}, {"optionId": "no", "name": "No", "kind": "reject_once"}]}}`,
		`{"permission": {"toolCall": {"toolCallId": "c2"}, "options": [{"optionId": "always", "name": "Always", "kind": "allow_always"}, {"optionId": "no", "name": "No", "kind": "reject_once"}]}}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "red"}}}}`, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	id := e.Run(e.AcpProject("paint", harness.ColorManifest), "work")
	got := e.WaitState(id, "done", "error")
	c := f.Call(4)
	if got.State != "done" || len(c.Answers) != 2 || c.Violation != "" {
		t.Fatalf("the agent's questions are answered by the schema: %+v %v %s", got, c.Answers, c.Violation)
	}
	if !strings.Contains(string(c.Answers[0]["result"]), `"optionId":"once"`) || !strings.Contains(string(c.Answers[1]["result"]), `"optionId":"no"`) {
		t.Fatalf("deck allows once and never always: %s / %s", c.Answers[0]["result"], c.Answers[1]["result"])
	}
	var asks []harness.Ask
	e.Must("deck.asks_list", nil, &asks)
	if len(asks) != 0 {
		t.Fatalf("the owner is not asked about what the sandbox bounds: %+v", asks)
	}
}

// TestACPResultWithoutItsFieldIsRefused: arguments of result_submit without result are no result, even where the
// schema takes any object; the agent hands in null again and the run had nothing to do.
func TestACPResultWithoutItsFieldIsRefused(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"null": "None"}}}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": null}}}`, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	loose := strings.Replace(harness.ColorManifest, `{"type": "object", "required": ["color"], "properties": {"color": {"type": "string"}}}`, `{"type": ["object", "null"]}`, 1)
	got := e.WaitState(e.Run(e.AcpProject("paint", loose), "work"), "done", "error")
	c := f.Call(4)
	if got.State != "done" || string(got.Result) != "null" || len(c.Mcp) < 2 || !strings.Contains(fmt.Sprint(c.Mcp[0]), "no result field") {
		t.Fatalf("deck refuses arguments without result and takes the null after: %+v %v", got, c.Mcp)
	}
}

// TestACPExternalToolStillWaitsForYes: the class rules of the gateway hold for an ACP agent as for any other.
func TestACPExternalToolStillWaitsForYes(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"id": "ext-1"}`) })
	f := harness.NewACP(t)
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "notes__post", "arguments": {"id": "a1", "text": "hi"}}}`, `{"end": "end_turn"}`))
	f.StreamText(5, harness.AcpStream(harness.AcpInit, `{"reply": "session/resume", "result": {}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"posted": true}}}}`, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	e.Secret("", "svc", "k")
	id := e.Run(e.AcpProject("notes", harness.PostManifest(svc.URL, "")), "work")
	a := e.WaitAsk()
	if a.Kind != "call" || len(svc.Requests()) != 0 {
		t.Fatalf("the external call waits for the owner and is not made: %+v %d", a, len(svc.Requests()))
	}
	e.Answer(a.ID, "allow")
	got := e.WaitState(id, "done", "error")
	if got.State != "done" || len(svc.Requests()) != 1 {
		t.Fatalf("after yes deck makes the call and the agent goes on: %+v %d", got, len(svc.Requests()))
	}
}

// TestACPProjectWithoutNetworkHasNoWebThroughTheProxy: the proxy of the sandbox carries the agent only to the hosts
// of its project, and nowhere in a project without network.
func TestACPProjectWithoutNetworkHasNoWebThroughTheProxy(t *testing.T) {
	t.Parallel()
	site := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{}`) })
	curl := `{"shell": "curl -s --noproxy '' --proxy \"$HTTP_PROXY\" ` + site.URL + `/page"}`
	f := harness.NewACP(t)
	for _, n := range []int{4, 5} {
		f.StreamText(n, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
			curl, `{"shell": "curl -s -m 3 -o /dev/null -w '%{http_code}' --noproxy '*' `+site.URL+`/direct"}`,
			`{"shell": "curl -s -m 3 -o /dev/null -w '%{http_code}' --noproxy '' --proxy \"$(echo $HTTP_PROXY | sed 's#//[^@]*@#//#')\" `+site.URL+`/page"}`,
			`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Checked "}}}`,
			`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "the page."}}}`,
			`{"mcp": {"tool": "result_submit", "arguments": {"result": {"written": 0}}}}`, `{"end": "end_turn"}`))
	}
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	open := strings.Replace(harness.NoNetworkManifest, `"no_network": true,`, "", 1)
	if got := e.WaitState(e.Run(e.AcpProject("open", open), "work"), "done"); !strings.Contains(string(got.Result), "Checked the page.") {
		t.Fatalf("what the agent said is the result of a command without a schema: %s", got.Result)
	}
	e.WaitState(e.Run(e.AcpProject("private", harness.NoNetworkManifest), "work"), "done")
	web, off := f.Call(4), f.Call(5)
	if !strings.Contains(web.Shell[0]["out"], "not in the domains") || !strings.Contains(off.Shell[0]["out"], "not reachable") || len(site.Requests()) != 0 {
		t.Fatalf("the proxy carries no agent to deck or local services, and nowhere without network: %q / %q", web.Shell[0]["out"], off.Shell[0]["out"])
	}
	if web.Shell[1]["out"] != "000" || off.Shell[1]["out"] != "000" {
		t.Fatalf("past the proxy the sandbox has no network: %q / %q", web.Shell[1]["out"], off.Shell[1]["out"])
	}
	if web.Shell[2]["out"] != "407" {
		t.Fatalf("the proxy takes only the agent that has its password: %q", web.Shell[2]["out"])
	}
	if web.Args[0] != "acp" || web.Env["GOOSE_MODE"] != "auto" || web.Env["OPENAI_API_KEY"] != "deck" || !strings.Contains(web.Env["EXTENSIONS"], `"developer"`) {
		t.Fatalf("Goose runs on deck's model with its own tools and no questions: %v %v", web.Args, web.Env)
	}
}

// TestACPStopCancelsTheTurn: a stop of the process sends session/cancel and the agent ends the turn.
func TestACPStopCancelsTheTurn(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, `{"hang": true}`))
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	id := e.Run(e.AcpProject("paint", harness.ColorManifest), "work")
	e.Until("the turn", func() bool { return f.Calls() >= 4 && f.Call(4).Request("session/prompt") != nil })
	e.Must("deck.process_stop", map[string]any{"process": id}, nil)
	e.WaitState(id, "stopped")
	e.Until("the cancel", func() bool { return f.Call(4).Request("session/cancel") != nil })
}

// TestACPSelfCheckPastItsCeilingSaysWhy: a self-check that runs out of time tells a key the model API refused
// from a model that never finished a call and from one that answered but is slow.
func TestACPSelfCheckPastItsCeilingSaysWhy(t *testing.T) {
	t.Parallel()
	start := []string{harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`}
	for _, tc := range []struct {
		name          string
		status        int
		steps         []string
		health, wants string
	}{
		{"refused key", 401, []string{harness.AcpModel}, "needs_login", "the model API refused the key (401 or 403)"},
		{"silent model", 200, nil, "warning", "not one model call finished within the ceiling"},
		{"slow model", 200, []string{harness.AcpModel}, "warning", "the model answered (150 tokens) but did not finish the steps"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := harness.NewACP(t)
			f.StreamText(1, harness.AcpStream(append(append(slices.Clone(start), tc.steps...), `{"hang": true}`)...))
			llm := harness.NewLLM(t, func(int, harness.ChatRequest) harness.ChatReply {
				if tc.status != 200 {
					return harness.ChatReply{Status: tc.status, Body: `{"error": {"message": "invalid key"}}`}
				}
				return harness.TextReply("ok")
			})
			e := harness.New(t, harness.WithACP(f, nil), harness.WithConfig(func(c *app.Config) { c.SelfCheckCeiling = 4 * time.Second }))
			h := e.AddACP(f, llm)
			if h.Status != tc.health || !strings.Contains(h.Error, tc.wants) || strings.Contains(h.Error, "stronger") {
				t.Fatalf("want %s with %q: %+v", tc.health, tc.wants, h)
			}
			var pl struct{ Connections []struct{ Name, Key string } }
			e.Must("deck.models_list", nil, &pl)
			if i := slices.IndexFunc(pl.Connections, func(p struct{ Name, Key string }) bool { return p.Name == "acp" }); i < 0 || pl.Connections[i].Key != "secret" {
				t.Fatalf("the models list tells how the key of a connection is passed: %+v", pl.Connections)
			}
		})
	}
}

// TestACPSelfCheckIsRedWhenTheSandboxOrTheProxyFails: the probes deck reads itself turn the self-check red, told
// apart as a broken adapter (leak, open network, no tokens) or a model that did not do the steps.
func TestACPSelfCheckIsRedWhenTheSandboxOrTheProxyFails(t *testing.T) {
	t.Parallel()
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join(harness.Testdata("acp"), name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	first, second, third := read("selfcheck_1.jsonl"), read("selfcheck_2.jsonl"), read("selfcheck_3.jsonl")
	adapter, model := "self-check of goose", "the model does not manage"
	for _, tc := range []struct {
		streams       []string
		want, verdict string
	}{
		{[]string{strings.Replace(first, `{"shell": "sh probe.sh"}`, `{"shell": "echo 0 > probe.code"}`, 1)}, "read a file deck hides", adapter},
		{[]string{strings.Replace(first, `{"shell": "sh probe.sh"}`+"\n", "", 1)}, "did not run", model},
		{[]string{strings.Replace(first, harness.AcpModel+"\n", "", 1)}, "token counts", adapter},
		{[]string{strings.Replace(first, `{"mcp": {"tool": "ask", "arguments": {"question": "Which answer should the result carry?"}}}`+"\n", "", 1)}, "question", model},
		{[]string{first, second, strings.Replace(third, `{"shell": "sh net.sh"}`, `{"shell": "echo 200 > net.out"}`, 1)}, "turn without network", adapter},
		{[]string{first, second, strings.Replace(third, `{"shell": "sh net.sh"}`+"\n", "", 1)}, "turn without network", model},
	} {
		t.Run(tc.verdict+": "+tc.want, func(t *testing.T) {
			t.Parallel()
			f := harness.NewACP(t)
			for i, s := range tc.streams {
				f.StreamText(i+1, s)
			}
			e := harness.New(t, harness.WithACP(f, nil))
			h := e.AddACP(f, harness.AcpLLM(t))
			if h.Status != "error" || !strings.Contains(h.Error, tc.want) || !strings.HasPrefix(h.Error, tc.verdict) {
				t.Fatalf("the self-check is red with %q, told as %q: %+v", tc.want, tc.verdict, h)
			}
			var pl struct {
				Models []struct{ Agent, Verdict string }
			}
			e.Must("deck.models_list", nil, &pl)
			verdict := map[string]string{adapter: "adapter", model: "model"}[tc.verdict]
			if i := slices.IndexFunc(pl.Models, func(p struct{ Agent, Verdict string }) bool { return p.Agent == "goose" }); i < 0 || pl.Models[i].Verdict != verdict {
				t.Fatalf("the models list shows the agent and the verdict %q: %+v", verdict, pl.Models)
			}
			for _, r := range e.AllModels(nil) {
				if r.Title == "acp" && !harness.HasVerb(r.Actions, "check") {
					t.Fatalf("a provider with a self-check can run it again: %+v", r.Actions)
				}
			}
			f.StreamText(0, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
				`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
			id := e.Run(e.AcpProject("paint", harness.ColorManifest), "work")
			if verdict == "model" {
				e.WaitState(id, "done")
				return
			}
			e.WaitState(id, "waiting_provider")
			if n := f.Calls(); n > len(tc.streams)+1 {
				t.Fatalf("a process on a provider whose adapter is broken waits; one whose model fails runs: %d calls", n)
			}
		})
	}
}

// TestACPDeckWritesNothingThroughWhatTheAgentLeft: skills go into a folder the agent writes to, so deck replaces
// what it finds there rather than follow a symlink the agent planted in an earlier turn.
func TestACPDeckWritesNothingThroughWhatTheAgentLeft(t *testing.T) {
	t.Parallel()
	victims := t.TempDir()
	victim := filepath.Join(victims, "owner.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := harness.NewACP(t)
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"shell": "cat \"$HOME/.agents/skills/reader/SKILL.md\" > seen.txt; rm -rf \"$HOME/.agents\"; ln -s `+victims+` \"$HOME/.agents\""}`,
		`{"mcp": {"tool": "ask", "arguments": {"question": "Which color should the report use?"}}}`, `{"end": "end_turn"}`))
	f.StreamText(5, harness.AcpStream(harness.AcpInit, `{"reply": "session/resume", "result": {}}`, `{"reply": "session/prompt"}`,
		`{"shell": "cat \"$HOME/.agents/skills/reader/SKILL.md\" > seen.txt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	skilled := strings.Replace(harness.ColorManifest, `"prompt": "Ask the owner which color the report should use."}`,
		`"prompt": "Ask the owner which color the report should use.", "skills": ["reader"]}`, 1)
	p := e.AcpProject("paint", skilled)
	e.Must("deck.document_save", map[string]any{"project": "paint", "name": "skills/reader/SKILL.md", "kind": "skill",
		"text": "---\nname: reader\ndescription: Reads.\n---\nRead.\n"}, nil)
	id := e.Run(p, "work")
	a := e.WaitAsk()
	e.WaitState(id, "waiting_answer")
	e.Answer(a.ID, "blue")
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	if entries, _ := os.ReadDir(victims); len(entries) != 1 {
		t.Fatalf("deck wrote the skills through the agent's symlink: %v", entries)
	}
	if b, _ := os.ReadFile(filepath.Join(f.Call(5).Cwd, "seen.txt")); !strings.Contains(string(b), "name: reader") {
		t.Fatalf("the skill is where ACP agents look for it on each turn: %q", b)
	}
}

// TestACPStopDuringSetupEndsTheAgent: a stop while the agent has not even answered initialize ends the turn.
func TestACPStopDuringSetupEndsTheAgent(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	f.StreamText(4, `{"hang": true}`)
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	id := e.Run(e.AcpProject("paint", harness.ColorManifest), "work")
	e.Until("the agent", func() bool { return f.Calls() >= 4 && f.Call(4).Request("initialize") != nil })
	e.Must("deck.process_stop", map[string]any{"process": id}, nil)
	e.WaitState(id, "stopped")
}

// TestACPProviderNameIsUniqueAcrossWorkspaces: health and self-checks of providers go by name, so a name another
// workspace uses is refused.
func TestACPProviderNameIsUniqueAcrossWorkspaces(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	// Another workspace with a model in an agent, which no command of this one makes.
	e.SQL(`INSERT INTO workspace (id, name) VALUES (2, 'other');
		INSERT INTO connection (id, workspace_id, name, url) VALUES (9, 2, 'x', 'http://x/v1');
		INSERT INTO model (workspace_id, connection_id, name, model, agent) VALUES (2, 9, 'shared', 'm', 'goose')`)
	code, out := e.AgentModel(map[string]any{"name": "shared", "url": "http://localhost:1/v1", "model": "m", "agent": "goose"})
	if code != 422 || !strings.Contains(string(out), "another workspace") {
		t.Fatalf("a provider name of another workspace is refused: %d %s", code, out)
	}
}

// TestACPProviderChangedDuringItsSelfCheckIsCheckedAgain: the first check ran on the old definition, so a change
// while it runs starts another one before the provider counts as green.
func TestACPProviderChangedDuringItsSelfCheckIsCheckedAgain(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	f.SelfCheck()
	for i := 4; i <= 6; i++ {
		f.Stream(i, "selfcheck_"+harness.Itoa(int64(i-3))+".jsonl")
	}
	slow := strings.Replace(harness.AcpInit, `{"reply": "initialize"`, `{"shell": "sleep 1"}`+"\n"+`{"reply": "initialize"`, 1)
	b, _ := os.ReadFile(harness.Testdata("acp/selfcheck_1.jsonl"))
	f.StreamText(1, strings.Replace(string(b), harness.AcpInit, slow, 1))
	llm := harness.AcpLLM(t)
	e := harness.New(t, harness.WithConfig(func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.Dir, "goose")}
		c.Sandbox = harness.FakeDirSandbox{Sandbox: sandbox.New(), Dir: f.Dir, More: nil}
	}))

	e.MustAgentModel(map[string]any{"name": "acp", "url": llm.URL, "model": "m", "agent": "goose"})
	e.Until("the first check", func() bool { return f.Calls() >= 1 })
	e.MustAgentModel(map[string]any{"name": "acp", "url": llm.URL, "model": "m2", "agent": "goose"})
	e.Until("the second check", func() bool {
		h, _ := health.Get(t.Context(), e.App.Store.Q, health.Of{}, "provider:acp")
		return f.Calls() >= 6 && h.Status == "ok"
	})
	if m := f.Call(f.Calls()).Env["GOOSE_MODEL"]; m != "m2" {
		t.Fatalf("the second check runs on the changed provider: %s", m)
	}
}

// TestACPProviderIsNotUsedBeforeItsFirstSelfCheck: a command started right after the provider is added waits for the
// self-check instead of running on an agent nobody checked.
func TestACPProviderIsNotUsedBeforeItsFirstSelfCheck(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	f.SelfCheck()
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	if err := os.WriteFile(filepath.Join(f.Dir, "slowversion"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	llm := harness.AcpLLM(t)
	e := harness.New(t, harness.WithConfig(func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.Dir, "goose")}
		c.Sandbox = harness.FakeDirSandbox{Sandbox: sandbox.New(), Dir: f.Dir, More: nil}
	}))

	p := e.Project("paint", harness.ColorManifest)
	e.Secret("paint", "k", "v")
	go func() {
		_, _ = e.AgentModel(map[string]any{"name": "acp", "url": llm.URL, "model": "m", "agent": "goose"})
	}()
	e.Until("the provider", func() bool {
		_, err := e.App.Store.Q.GetModel(t.Context(), db.GetModelParams{WorkspaceID: 1, Name: "acp"})
		return err == nil
	})
	e.UseModels("paint.work", "acp")
	id := e.Run(p, "work")
	e.Until("the self-check", func() bool {
		h, err := health.Get(t.Context(), e.App.Store.Q, health.Of{}, "provider:acp")
		return err == nil && h.Status == "ok"
	})
	got := e.WaitState(id, "done", "error")
	if got.State != "done" || !strings.Contains(string(f.Call(1).Request("session/prompt")), "self-check of deck") {
		t.Fatalf("the first agent run is the self-check, the command waits for it: %+v", got)
	}
}

// TestAProviderTakesItsOwnSelfCheckCeiling: a slow model gets more time for its self-check than deck's default.
func TestAProviderTakesItsOwnSelfCheckCeiling(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	f.StreamText(1, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, harness.AcpModel, `{"hang": true}`))
	llm := harness.NewLLM(t, func(int, harness.ChatRequest) harness.ChatReply { return harness.TextReply("ok") })
	e := harness.New(t, harness.WithConfig(func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.Dir, "goose")}
		c.Sandbox = harness.FakeDirSandbox{Sandbox: sandbox.New(), Dir: f.Dir, More: nil}
		c.ACPCallLimit = 2 * time.Second
		c.SelfCheckCeiling = time.Second
	}))

	e.MustAgentModel(map[string]any{"name": "acp", "url": llm.URL, "model": "m", "agent": "goose", "check_minutes": 1})
	// Past deck's ceiling of a second the check must still run.
	time.Sleep(2500 * time.Millisecond)
	h, err := health.Get(t.Context(), e.App.Store.Q, health.Of{}, "provider:acp")
	if err != nil || h.Status != "checking" {
		t.Fatalf("the self-check of a provider with a minute still runs past deck's second: %+v %v", h, err)
	}
}

// TestACPAgentDoesNotReadTheOwnersHome: the sandbox keeps the owner's home closed from an ACP agent, as from scripts.
func TestACPAgentDoesNotReadTheOwnersHome(t *testing.T) {
	owner := harness.OwnerHome(t)
	_ = os.WriteFile(filepath.Join(owner, ".zshrc"), []byte("private"), 0o600)
	f := harness.NewACP(t)
	t.Setenv("HOME", owner)
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"shell": "cat `+filepath.Join(owner, ".zshrc")+`"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"written": 0}}}}`, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	id := e.Run(e.AcpProject("paint", writtenManifest), "work")
	e.WaitState(id, "done")
	if strings.Contains(f.Call(4).Shell[0]["out"], "private") {
		t.Fatalf("the agent does not read the owner's home: %v", f.Call(4).Shell)
	}
}

// TestACPAgentReachesALocalServiceItsProjectNames: a service of the owner's machine that the project names by IP and
// port in domains.read is reached by the shell of an ACP agent through deck's proxy, as by a script.
func TestACPAgentReachesALocalServiceItsProjectNames(t *testing.T) {
	t.Parallel()
	local := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { _, _ = w.Write([]byte("hello")) })
	addr := strings.TrimPrefix(local.URL, "http://")
	f := harness.NewACP(t)
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"shell": "curl -s -m 5 -o /dev/null -w '%{http_code}' http://`+addr+`/"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"written": 0}}}}`, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	m := strings.Replace(writtenManifest, harness.Manifest(`{`), harness.Manifest(fmt.Sprintf(`{"domains": {"read": [%q]},`, addr)), 1)
	e.WaitState(e.Run(e.AcpProject("paint", m), "work"), "done")
	if got := f.Call(4).Shell[0]["out"]; got != "200" || len(local.Requests()) != 1 {
		t.Fatalf("the shell reaches the service its project names: %q, %d requests", got, len(local.Requests()))
	}
}

// TestAnACPAgentCallsOnlyItsModelThroughDeck: deck's key goes with the calls of the model of a turn, not with any
// other request to the API of the connection, as for a script.
func TestAnACPAgentCallsOnlyItsModelThroughDeck(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	llm := harness.AcpLLM(t)
	f.StreamText(4, harness.AcpStream(gooseInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"get": "files"}`, harness.AcpModel, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, llm))
	e.WaitState(e.Run(e.AcpProject("paint", harness.ColorManifest), "work"), "done", "error")
	got := harness.JsonLines[map[string]any](f.Files(4)("get.jsonl"))
	if len(got) != 1 || got[0]["status"] != 403.0 {
		t.Fatalf("a request other than a call of the model is refused: %v", got)
	}
}

// TestACPCommandWithAFolderRunsOnALocalModel: a folder command that requires no_cloud is red in health while no model
// fits it; on a local ACP model it works in the owner's folder, its skills in the session home, the owner's kept.
func TestACPCommandWithAFolderRunsOnALocalModel(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	e := harness.New(t, harness.WithAgent(harness.Ok), harness.WithConfig(func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.Dir, "goose")}
		c.Sandbox = harness.FakeDirSandbox{Sandbox: sandbox.New(), Dir: f.Dir, More: nil}
		c.ACPCallLimit = 2 * time.Second
	}))

	cmd := strings.Replace(harness.FolderCommand("files", "true"), `"executor":`, `"requires": ["no_cloud"], "executor":`, 1)
	cmd = strings.Replace(cmd, `"prompt": "Do files."`, `"prompt": "Do files.", "skills": ["reader"]`, 1)
	p := e.Project("music", harness.TestManifest([]string{cmd}, nil))
	e.Must("deck.document_save", map[string]any{"project": "music", "name": "skills/reader/SKILL.md", "kind": "skill",
		"text": "---\nname: reader\ndescription: Reads.\n---\nRead.\n"}, nil)
	folder := harness.RepoFolder(t)
	own := filepath.Join(folder, ".agents", "skills", "own", "SKILL.md")
	if err := errors.Join(os.MkdirAll(filepath.Dir(own), 0o700), os.WriteFile(own, []byte("the owner's"), 0o600)); err != nil {
		t.Fatal(err)
	}
	e.Must("deck.folder_add", map[string]any{"project": "music", "path": folder}, nil)
	unfit := func() *harness.HealthRow {
		for _, r := range e.Health("music") {
			if r.Subject == "command:music.files" {
				return &r
			}
		}
		return nil
	}
	if r := unfit(); r == nil || r.Status != "error" || !strings.Contains(r.Error, "no provider fits music.files") {
		t.Fatalf("a command no provider fits is a red row of health: %+v", r)
	}
	e.SetACP(f, 1, "local", "goose", harness.AcpLLM(t), true)
	if r := unfit(); r != nil {
		t.Fatalf("the row goes once a model fits: %+v", r)
	}
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"shell": "cat \"$HOME/.agents/skills/reader/SKILL.md\" > seen.txt; ls .agents/skills > listed.txt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"n": 1}}}}`, `{"end": "end_turn"}`))
	id := e.Run(p, "files")
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the command runs on the local model: %+v", got)
	}
	if c := f.Call(4); c.Cwd != folder {
		t.Fatalf("the agent works in the owner's folder: %s", c.Cwd)
	}
	if b, _ := os.ReadFile(filepath.Join(folder, "seen.txt")); !strings.Contains(string(b), "name: reader") {
		t.Fatalf("the skill is in the home of the session, where the agent looks for it: %q", b)
	}
	listed, _ := os.ReadFile(filepath.Join(folder, "listed.txt"))
	if b, _ := os.ReadFile(own); string(b) != "the owner's" || string(listed) != "own\n" {
		t.Fatalf("deck writes no skill into the owner's folder and keeps the owner's: %q %q", b, listed)
	}
}

// TestACPTurnPastTheLimitOfItsModelGoesOnWithoutAPause: a call of an ACP turn past the tokens a minute of its model
// reaches no model; the turn is the model's limit, and the model does not pause, since it met no limit of its own.
func TestACPTurnPastTheLimitOfItsModelGoesOnWithoutAPause(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	llm := harness.AcpLLM(t)
	f.StreamText(4, harness.AcpStream(gooseInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		harness.AcpModel, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, llm))
	before := len(llm.Requests())
	e.Must("deck.model_set", map[string]any{"connection": "acp", "name": "acp", "model": "m", "tpm": 10}, nil)
	got := e.WaitState(e.Run(e.AcpProject("paint", harness.ColorManifest), "work"), "done", "error", "rate_limited")
	if fl := e.Fault(got.ID); got.State != "error" || fl.ErrorType != "model.limit" || !strings.Contains(fl.Error, "tokens a minute acp takes") {
		t.Fatalf("a call past the limit of the model is the model's limit: %+v %+v", got, fl)
	}
	if n := len(llm.Requests()); n != before {
		t.Fatalf("the call past the limit reaches no model: %d calls after %d", n, before)
	}
	if h := e.HealthRowOf("provider:acp"); h == nil || h["status"] == "paused" {
		t.Fatalf("the model does not pause on a limit it did not meet: %v", h)
	}
}
