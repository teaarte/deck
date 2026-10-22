package scenarios

import (
	"bytes"
	"compress/gzip"
	"deck2/internal/health"
	deckhome "deck2/internal/home"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/sandbox"
	"deck2/internal/store/db"
)

// gooseInit is the initialize of Goose: no session/resume, session/load instead.
const gooseInit = `{"reply": "initialize", "result": {"protocolVersion": 1, "agentCapabilities": {"loadSession": true, "mcpCapabilities": {"http": true}, "sessionCapabilities": {"list": {}, "close": {}}}, "authMethods": []}}`

const acpInit = `{"reply": "initialize", "result": {"protocolVersion": 1, "agentCapabilities": {"mcpCapabilities": {"http": true}, "sessionCapabilities": {"resume": {}, "list": {}}}, "authMethods": []}}`

const writtenManifest = `{"version": 12,
  "commands": [{"name": "work", "title": "Work", "description": "Writes a file", "class": "internal",
    "result": {"type": "object", "required": ["written"], "properties": {"written": {"type": "integer"}}},
    "executor": {"type": "agent", "prompt": "Write the file."}}]}`

// acpModel is a call of the model of a turn, which carries the tools of the agent.
const acpModel = `{"model": {"model": "m", "messages": [{"role": "user", "content": "go"}], "tools": [{"type": "function", "function": {"name": "shell"}}]}}`

func acpLLM(t *testing.T) *fakeLLM {
	return newLLM(t, func(int, chatRequest) chatReply { return textReply("ok") })
}

// TestACPCommandAgentWorksThroughTheGateway: an ACP agent in the sandbox of deck writes in its folder, cannot read
// what deck hides, calls the model with the key only deck's proxy knows and hands its result in with result_submit.
func TestACPCommandAgentWorksThroughTheGateway(t *testing.T) {
	f := newACP(t)
	llm := acpLLM(t)
	t.Setenv("DEEPSEEK_API_KEY", "sk-from-the-environment-of-deck")
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	e := acpEnv(t, f, "goose", llm, func(c *app.Config) {
		state := deckhome.Layout(c.Home).State()
		if err := errors.Join(os.MkdirAll(state, 0o700), os.WriteFile(filepath.Join(state, "token"), []byte("owner-token-value"), 0o600),
			os.WriteFile(filepath.Join(f.dir, "probe"), []byte(filepath.Join(state, "token")), 0o600)); err != nil {
			t.Fatal(err)
		}
	})
	home := e.App.Cfg.Home
	f.streamText(4, strings.Join([]string{acpInit,
		`{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Painting."}}}`,
		`{"shell": "echo painted > canvas.txt"}`,
		`{"shell": "cat ` + filepath.Join(deckhome.Layout(home).State(), "token") + `"}`,
		`{"shell": "echo tampered > \"$HOME/../session\""}`,
		acpModel,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`,
		`{"end": "end_turn"}`}, "\n"))
	id := e.run(e.acpProject("paint", colorManifest), "work")
	got := e.waitState(id, "done", "error")
	c := f.call(4)
	if got.State != "done" || string(got.Result) != `{"color":"blue"}` || c.violation != "" {
		t.Fatalf("the result comes in by the schema with result_submit: %+v %s", got, c.violation)
	}
	if b, _ := os.ReadFile(filepath.Join(c.cwd, "canvas.txt")); string(b) != "painted\n" {
		t.Fatalf("the agent writes in its own folder: %q", b)
	}
	session, _ := os.ReadFile(filepath.Join(filepath.Dir(c.env["HOME"]), "session"))
	if len(c.shell) != 3 || strings.Contains(c.shell[1]["out"], "owner-token-value") || string(session) != "s-1" {
		t.Fatalf("the sandbox of deck keeps the owner's token and deck's session file from the agent: %v %q", c.shell, session)
	}
	reqs := llm.requests()
	if len(reqs) != 4 || reqs[3].Auth != "Bearer sk-real-model-key" || len(c.model) != 1 || c.model[0]["status"] != 200.0 {
		t.Fatalf("the model call goes through deck's proxy, which puts the key in: %v %v", reqs, c.model)
	}
	for k, v := range c.env {
		if strings.Contains(v, "sk-real-model-key") || strings.Contains(v, "sk-from-the-environment") || strings.HasPrefix(k, "XDG_") {
			t.Fatalf("the agent sees no key and no settings of the owner: %s=%s", k, v)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(f.dir, "probe.out")); len(b) == 0 || string(b) == "<nil>" {
		t.Fatalf("even the version of the agent is asked inside the sandbox: %q", b)
	}
	if h := c.env["HOME"]; !strings.HasPrefix(h, filepath.Join(deckhome.Layout(home).State(), "acp", "sessions")) {
		t.Fatalf("the agent lives in a home of its own session: %s", h)
	}
	var calls []map[string]any
	e.modelCalls("", id, &calls)
	if len(calls) != 1 || calls[0]["gen_ai.provider.name"] != "acp" || calls[0]["gen_ai.usage.input_tokens"] != 100.0 ||
		calls[0]["gen_ai.usage.cache_read.input_tokens"] != 40.0 || calls[0]["gen_ai.response.model"] != "fake-model" {
		t.Fatalf("deck's proxy journals the tokens of each model call: %v", calls)
	}
	var params struct {
		McpServers []struct{ URL string } `json:"mcpServers"`
	}
	_ = json.Unmarshal(c.request("session/new"), &params)
	if len(params.McpServers) != 1 || strings.HasPrefix(params.McpServers[0].URL, e.URL) {
		t.Fatalf("the gateway is the one MCP server of the session, reached through the entry of the sandbox: %+v", params)
	}
}

func acpStream(lines ...string) string { return strings.Join(lines, "\n") }

// TestACPQuestionStopsTheTurnBeforeTheAgentTimesOutAndTheAnswerResumes: an ACP agent gives a tool call 60 s, so a
// question waits less, the turn ends and the owner's answer comes back through session/load of the same session.
func TestACPQuestionStopsTheTurnBeforeTheAgentTimesOutAndTheAnswerResumes(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	f.streamText(4, acpStream(gooseInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Hi."}}}`,
		`{"update": {"sessionUpdate": "agent_thought_chunk", "content": {"type": "text", "text": "Read "}}}`,
		`{"update": {"sessionUpdate": "agent_thought_chunk", "content": {"type": "text", "text": "first."}}}`,
		`{"update": {"sessionUpdate": "tool_call", "toolCallId": "c1", "title": "read", "kind": "read", "status": "pending"}}`,
		`{"update": {"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "in_progress", "rawInput": {"path": "a.txt"}}}`,
		`{"update": {"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "completed", "rawInput": {"path": "a.txt"}, "rawOutput": {"output": "text of a"}}}`,
		`{"mcp": {"tool": "ask", "arguments": {"question": "Which color should the report use?"}}}`, `{"end": "end_turn"}`))
	f.streamText(5, acpStream(gooseInit, `{"reply": "session/load", "result": {}, "replay": [{"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Hi again."}}]}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "goose", acpLLM(t), func(c *app.Config) { c.QuestionWait = 10 * time.Minute })
	id := e.run(e.acpProject("paint", colorManifest), "work")
	a := e.waitAsk()
	if a.Kind != "question" || !strings.Contains(a.Text, "Which color") {
		t.Fatalf("the question reaches the owner: %+v", a)
	}
	e.waitState(id, "waiting_answer")
	if m := f.call(4).mcp; len(m) != 1 || !strings.Contains(m[0]["text"].(string), "not answered yet") {
		t.Fatalf("the agent hears in time that the owner has not answered and ends its turn: %v", m)
	}
	lines := e.log(id)
	kinds := make([]string, 0, len(lines))
	for _, l := range lines {
		kinds = append(kinds, fmt.Sprint(l["kind"]))
	}
	if len(kinds) < 5 || !slices.Equal(kinds[:5], []string{"message", "thought", "tool", "tool", "tool_update"}) || lines[0]["text"] != "Hi." ||
		lines[1]["text"] != "Read first." || lines[2]["id"] != "c1" || lines[2]["title"] != "read" || lines[3]["text"] != `{"path":"a.txt"}` ||
		lines[4]["id"] != "c1" || lines[4]["status"] != "completed" || lines[4]["text"] != `{"output":"text of a"}` {
		t.Fatalf("the log keeps the session updates of ACP: a thought whole, the call, its late input and its end: %v", lines)
	}
	e.answer(a.ID, "blue")
	got := e.waitState(id, "done", "error")
	c := f.call(5)
	var resume struct{ SessionID string }
	_ = json.Unmarshal(c.request("session/load"), &resume)
	if got.State != "done" || string(got.Result) != `{"color":"blue"}` || resume.SessionID != "s-1" || c.violation != "" {
		t.Fatalf("the answer resumes the session the agent opened: %+v %+v %s", got, resume, c.violation)
	}
	if !strings.Contains(string(c.request("session/prompt")), "blue") {
		t.Fatalf("the resume brings the answer: %s", c.request("session/prompt"))
	}
	for _, l := range e.log(id) {
		if l["text"] == "Hi again." {
			t.Fatalf("the history a load replays is not logged again: %v", l)
		}
	}
}

// TestACPAgentPermissionIsAllowedOnceWithoutTheOwner: inside the sandbox of deck the agent's own questions about its
// tools are answered with allow_once; "always" stays the owner's button.
func TestACPAgentPermissionIsAllowedOnceWithoutTheOwner(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"permission": {"toolCall": {"toolCallId": "c1", "title": "cat notes", "kind": "execute"}, "options": [{"optionId": "always", "name": "Always", "kind": "allow_always"}, {"optionId": "once", "name": "Once", "kind": "allow_once"}, {"optionId": "no", "name": "No", "kind": "reject_once"}]}}`,
		`{"permission": {"toolCall": {"toolCallId": "c2"}, "options": [{"optionId": "always", "name": "Always", "kind": "allow_always"}, {"optionId": "no", "name": "No", "kind": "reject_once"}]}}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "red"}}}}`, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "goose", acpLLM(t))
	id := e.run(e.acpProject("paint", colorManifest), "work")
	got := e.waitState(id, "done", "error")
	c := f.call(4)
	if got.State != "done" || len(c.answers) != 2 || c.violation != "" {
		t.Fatalf("the agent's questions are answered by the schema: %+v %v %s", got, c.answers, c.violation)
	}
	if !strings.Contains(string(c.answers[0]["result"]), `"optionId":"once"`) || !strings.Contains(string(c.answers[1]["result"]), `"optionId":"no"`) {
		t.Fatalf("deck allows once and never always: %s / %s", c.answers[0]["result"], c.answers[1]["result"])
	}
	var asks []askView
	e.must("deck.asks_list", nil, &asks)
	if len(asks) != 0 {
		t.Fatalf("the owner is not asked about what the sandbox bounds: %+v", asks)
	}
}

// TestACPResultWithoutItsFieldIsRefused: arguments of result_submit without result are no result, even where the
// schema takes any object; the agent hands in null again and the run had nothing to do.
func TestACPResultWithoutItsFieldIsRefused(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"null": "None"}}}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": null}}}`, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "goose", acpLLM(t))
	loose := strings.Replace(colorManifest, `{"type": "object", "required": ["color"], "properties": {"color": {"type": "string"}}}`, `{"type": ["object", "null"]}`, 1)
	got := e.waitState(e.run(e.acpProject("paint", loose), "work"), "done", "error")
	c := f.call(4)
	if got.State != "done" || string(got.Result) != "null" || len(c.mcp) < 2 || !strings.Contains(fmt.Sprint(c.mcp[0]), "no result field") {
		t.Fatalf("deck refuses arguments without result and takes the null after: %+v %v", got, c.mcp)
	}
}

// TestACPExternalToolStillWaitsForYes: the class rules of the gateway hold for an ACP agent as for any other.
func TestACPExternalToolStillWaitsForYes(t *testing.T) {
	t.Parallel()
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{"id": "ext-1"}`) })
	f := newACP(t)
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "notes__post", "arguments": {"id": "a1", "text": "hi"}}}`, `{"end": "end_turn"}`))
	f.streamText(5, acpStream(acpInit, `{"reply": "session/resume", "result": {}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"posted": true}}}}`, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "goose", acpLLM(t))
	e.must("deck.secret_set", map[string]any{"name": "svc", "value": "k"}, nil)
	id := e.run(e.acpProject("notes", postManifest(svc.URL, "")), "work")
	a := e.waitAsk()
	if a.Kind != "call" || len(svc.requests()) != 0 {
		t.Fatalf("the external call waits for the owner and is not made: %+v %d", a, len(svc.requests()))
	}
	e.answer(a.ID, "allow")
	got := e.waitState(id, "done", "error")
	if got.State != "done" || len(svc.requests()) != 1 {
		t.Fatalf("after yes deck makes the call and the agent goes on: %+v %d", got, len(svc.requests()))
	}
}

// TestACPProjectWithoutNetworkHasNoWebThroughTheProxy: the proxy of the sandbox carries the agent only to the hosts
// of its project, and nowhere in a project without network.
func TestACPProjectWithoutNetworkHasNoWebThroughTheProxy(t *testing.T) {
	t.Parallel()
	site := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{}`) })
	curl := `{"shell": "curl -s --noproxy '' --proxy \"$HTTP_PROXY\" ` + site.URL + `/page"}`
	f := newACP(t)
	for _, n := range []int{4, 5} {
		f.streamText(n, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
			curl, `{"shell": "curl -s -m 3 -o /dev/null -w '%{http_code}' --noproxy '*' `+site.URL+`/direct"}`,
			`{"shell": "curl -s -m 3 -o /dev/null -w '%{http_code}' --noproxy '' --proxy \"$(echo $HTTP_PROXY | sed 's#//[^@]*@#//#')\" `+site.URL+`/page"}`,
			`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Checked "}}}`,
			`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "the page."}}}`,
			`{"mcp": {"tool": "result_submit", "arguments": {"result": {"written": 0}}}}`, `{"end": "end_turn"}`))
	}
	e := acpEnv(t, f, "goose", acpLLM(t))
	open := strings.Replace(noNetworkManifest, `"no_network": true,`, "", 1)
	if got := e.waitState(e.run(e.acpProject("open", open), "work"), "done"); !strings.Contains(string(got.Result), "Checked the page.") {
		t.Fatalf("what the agent said is the result of a command without a schema: %s", got.Result)
	}
	e.waitState(e.run(e.acpProject("private", noNetworkManifest), "work"), "done")
	web, off := f.call(4), f.call(5)
	if !strings.Contains(web.shell[0]["out"], "not in the domains") || !strings.Contains(off.shell[0]["out"], "not reachable") || len(site.requests()) != 0 {
		t.Fatalf("the proxy carries no agent to deck or local services, and nowhere without network: %q / %q", web.shell[0]["out"], off.shell[0]["out"])
	}
	if web.shell[1]["out"] != "000" || off.shell[1]["out"] != "000" {
		t.Fatalf("past the proxy the sandbox has no network: %q / %q", web.shell[1]["out"], off.shell[1]["out"])
	}
	if web.shell[2]["out"] != "407" {
		t.Fatalf("the proxy takes only the agent that has its password: %q", web.shell[2]["out"])
	}
	if web.args[0] != "acp" || web.env["GOOSE_MODE"] != "auto" || web.env["OPENAI_API_KEY"] != "deck" || !strings.Contains(web.env["EXTENSIONS"], `"developer"`) {
		t.Fatalf("Goose runs on deck's model with its own tools and no questions: %v %v", web.args, web.env)
	}
}

// TestACPSessionTheAgentLostStartsOver: a resume the agent refuses for a session it no longer lists starts anew.
func TestACPSessionTheAgentLostStartsOver(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Hi."}}}`,
		`{"update": {"sessionUpdate": "agent_thought_chunk", "content": {"type": "text", "text": "Read "}}}`,
		`{"update": {"sessionUpdate": "agent_thought_chunk", "content": {"type": "text", "text": "first."}}}`,
		`{"update": {"sessionUpdate": "tool_call", "toolCallId": "c1", "title": "read", "kind": "read", "status": "pending"}}`,
		`{"update": {"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "in_progress", "rawInput": {"path": "a.txt"}}}`,
		`{"update": {"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "completed", "rawInput": {"path": "a.txt"}, "rawOutput": {"output": "text of a"}}}`,
		`{"mcp": {"tool": "ask", "arguments": {"question": "Which color should the report use?"}}}`, `{"end": "end_turn"}`))
	f.streamText(5, acpStream(acpInit, `{"reply": "session/resume", "error": {"code": -32602, "message": "Invalid params: session is not resumable: s-1"}}`,
		`{"reply": "session/list", "result": {"sessions": []}}`))
	f.streamText(6, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-2"}}`, `{"reply": "session/prompt"}`, `{"end": "end_turn"}`))
	f.streamText(7, acpStream(acpInit, `{"reply": "session/resume", "result": {}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "goose", acpLLM(t))
	id := e.run(e.acpProject("paint", colorManifest), "work")
	a := e.waitAsk()
	e.waitState(id, "waiting_answer")
	e.answer(a.ID, "blue")
	got := e.waitState(id, "done", "error")
	var resume struct{ SessionID string }
	if f.calls() == 7 {
		_ = json.Unmarshal(f.call(7).request("session/resume"), &resume)
	}
	if got.State != "done" || f.call(6).request("session/new") == nil || resume.SessionID != "s-2" {
		t.Fatalf("a session the agent lost starts over and the answer reaches the new one: %+v, %d calls, %+v", got, f.calls(), resume)
	}
}

// TestACPStopCancelsTheTurn: a stop of the process sends session/cancel and the agent ends the turn.
func TestACPStopCancelsTheTurn(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, `{"hang": true}`))
	e := acpEnv(t, f, "goose", acpLLM(t))
	id := e.run(e.acpProject("paint", colorManifest), "work")
	e.waitFor("the turn", func() bool { return f.calls() >= 4 && f.call(4).request("session/prompt") != nil })
	e.must("deck.process_stop", map[string]any{"process": id}, nil)
	e.waitState(id, "stopped")
	e.waitFor("the cancel", func() bool { return f.call(4).request("session/cancel") != nil })
}

// TestACPRecordedTurnOfGoose replays a turn recorded from Goose through the adapter.
func TestACPRecordedTurnOfGoose(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	f.stream(4, "goose_turn.jsonl")
	e := acpEnv(t, f, "goose", acpLLM(t))
	got := e.waitState(e.run(e.acpProject("notes", writtenManifest), "work"), "done", "error")
	c := f.call(4)
	if got.State != "done" || string(got.Result) != `{"written":1}` || c.violation != "" {
		t.Fatalf("the recorded turn hands its result in: %+v %s", got, c.violation)
	}
}

// TestACPSelfCheckPastItsCeilingSaysWhy: a self-check that runs out of time tells a key the model API refused
// from a model that never finished a call and from one that answered but is slow.
func TestACPSelfCheckPastItsCeilingSaysWhy(t *testing.T) {
	start := []string{acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`}
	for _, tc := range []struct {
		name          string
		status        int
		steps         []string
		health, wants string
	}{
		{"refused key", 401, []string{acpModel}, "needs_login", "the model API refused the key (401 or 403)"},
		{"silent model", 200, nil, "warning", "not one model call finished within the ceiling"},
		{"slow model", 200, []string{acpModel}, "warning", "the model answered (150 tokens) but did not finish the steps"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newACP(t)
			f.streamText(1, acpStream(append(append(slices.Clone(start), tc.steps...), `{"hang": true}`)...))
			llm := newLLM(t, func(int, chatRequest) chatReply {
				if tc.status != 200 {
					return chatReply{status: tc.status, body: `{"error": {"message": "invalid key"}}`}
				}
				return textReply("ok")
			})
			e, h := acpAdded(t, f, "goose", llm, func(c *app.Config) { c.SelfCheckCeiling = 2 * time.Second })
			if h.Status != tc.health || !strings.Contains(h.Error, tc.wants) || strings.Contains(h.Error, "stronger") {
				t.Fatalf("want %s with %q: %+v", tc.health, tc.wants, h)
			}
			var pl struct{ Connections []struct{ Name, Key string } }
			e.must("deck.models_list", nil, &pl)
			if i := slices.IndexFunc(pl.Connections, func(p struct{ Name, Key string }) bool { return p.Name == "acp" }); i < 0 || pl.Connections[i].Key != "secret model_key" {
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
		b, err := os.ReadFile(filepath.Join("testdata/acp", name))
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
		{[]string{strings.Replace(first, acpModel+"\n", "", 1)}, "token counts", adapter},
		{[]string{strings.Replace(first, `{"mcp": {"tool": "ask", "arguments": {"question": "Which answer should the result carry?"}}}`+"\n", "", 1)}, "question", model},
		{[]string{first, second, strings.Replace(third, `{"shell": "sh net.sh"}`, `{"shell": "echo 200 > net.out"}`, 1)}, "turn without network", adapter},
		{[]string{first, second, strings.Replace(third, `{"shell": "sh net.sh"}`+"\n", "", 1)}, "turn without network", model},
	} {
		f := newACP(t)
		for i, s := range tc.streams {
			f.streamText(i+1, s)
		}
		e, h := acpAdded(t, f, "goose", acpLLM(t))
		if h.Status != "error" || !strings.Contains(h.Error, tc.want) || !strings.HasPrefix(h.Error, tc.verdict) {
			t.Fatalf("the self-check is red with %q, told as %q: %+v", tc.want, tc.verdict, h)
		}
		var pl struct {
			Models []struct{ Agent, Verdict string }
		}
		e.must("deck.models_list", nil, &pl)
		verdict := map[string]string{adapter: "adapter", model: "model"}[tc.verdict]
		if i := slices.IndexFunc(pl.Models, func(p struct{ Agent, Verdict string }) bool { return p.Agent == "goose" }); i < 0 || pl.Models[i].Verdict != verdict {
			t.Fatalf("the models list shows the agent and the verdict %q: %+v", verdict, pl.Models)
		}
		for _, r := range blockData[[]screenRow](t, e.settingsBlock("Models", nil)["rows"]) {
			if r.Cells[0] == "acp" && !hasVerb(r.Actions, "check") {
				t.Fatalf("a provider with a self-check can run it again: %+v", r.Actions)
			}
		}
		f.streamText(0, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
			`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
		id := e.run(e.acpProject("paint", colorManifest), "work")
		if verdict == "model" {
			e.waitState(id, "done")
			continue
		}
		e.waitState(id, "waiting_provider")
		if n := f.calls(); n > len(tc.streams)+1 {
			t.Fatalf("a process on a provider whose adapter is broken waits; one whose model fails runs: %d calls", n)
		}
	}
}

// TestFakeACPRejectsWhatTheSchemaLacks: the fake agent refuses a method or a field the ACP schema does not have.
func TestFakeACPRejectsWhatTheSchemaLacks(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ line, want string }{
		{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"bogus":true}}`, "unknown field bogus"},
		{`{"jsonrpc":"2.0","id":1,"method":"session/fork","params":{}}`, "unknown method"},
		{`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":"/tmp"}}`, "does not match the schema"},
	} {
		f := newACP(t)
		f.streamText(0, acpInit)
		cmd := exec.Command(filepath.Join(f.dir, "goose"))
		cmd.Stdin = strings.NewReader(tc.line + "\n")
		_ = cmd.Run()
		if v := f.call(1).violation; !strings.Contains(v, tc.want) {
			t.Fatalf("%s: the fake refuses with %q, got %q", tc.line, tc.want, v)
		}
	}
}

// TestACPTurnSucceedsOnlyOnEndTurn: another stop reason or a failed prompt is an error; the last answer of the model
// API tells a refused key (a login) and a usage limit (a pause until Retry-After) apart.
func TestACPTurnSucceedsOnlyOnEndTurn(t *testing.T) {
	t.Parallel()
	failed := `{"fail": {"code": -32603, "message": "Internal error: turn failed"}}`
	for _, tc := range []struct {
		end         string
		status      int
		state, want string
	}{
		{`{"end": "cancelled"}`, 200, "error", "interrupted"},
		{`{"end": "max_tokens"}`, 200, "error", "max_tokens"},
		{`{"end": "refusal"}`, 200, "error", "refusal"},
		{failed, 200, "error", "turn failed"},
		{failed, 401, "needs_login", ""},
		{failed, 429, "rate_limited", ""},
	} {
		f := newACP(t)
		llm := newLLM(t, func(n int, _ chatRequest) chatReply {
			if n <= 3 || tc.status == 200 {
				return textReply("ok")
			}
			return chatReply{status: tc.status, header: http.Header{"Retry-After": {"120"}}, body: `{"error": {"message": "no"}}`}
		})
		f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, acpModel,
			`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, tc.end))
		e := acpEnv(t, f, "goose", llm)
		id := e.run(e.acpProject("paint", colorManifest), "work")
		got := e.waitState(id, "done", "error", "needs_login", "rate_limited")
		if got.State != tc.state || !strings.Contains(got.Error, tc.want) {
			t.Fatalf("%s %d: want %s with %q, got %+v", tc.end, tc.status, tc.state, tc.want, got)
		}
		if tc.status == 429 {
			pp, _ := e.App.Store.Q.GetProcess(t.Context(), id)
			if left := time.Until(time.UnixMilli(pp.PauseUntilMs.Int64)); left < 100*time.Second || left > 121*time.Second {
				t.Fatalf("the pause lasts until Retry-After: %s", left)
			}
		}
	}
}

// TestAFailedTurnTellsTheEndOfWhatTheAgentWroteToStderr: the error of an agent is at the end of its stderr, so a
// failed turn keeps that end.
func TestAFailedTurnTellsTheEndOfWhatTheAgentWroteToStderr(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	noise, _ := json.Marshal(strings.Repeat("loading a plugin\n", 300) + "Error: the config is broken")
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"stderr": `+string(noise)+`}`, `{"exit": 3}`))
	e := acpEnv(t, f, "goose", newLLM(t, func(int, chatRequest) chatReply { return textReply("ok") }))
	got := e.waitState(e.run(e.acpProject("paint", colorManifest), "work"), "done", "error")
	if got.State != "error" || !strings.Contains(got.Error, "the config is broken") {
		t.Fatalf("the failed turn tells the end of stderr: %+v", got)
	}
}

// TestACPTurnThatEndsOnARefusalOfTheModelIsTheRefusal: Goose ends its turn as end_turn with the error of the model
// API in its text, so the last answer of the API still tells a usage limit and a refused key.
func TestACPTurnThatEndsOnARefusalOfTheModelIsTheRefusal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		status int
		state  string
	}{{429, "rate_limited"}, {401, "needs_login"}} {
		f := newACP(t)
		llm := newLLM(t, func(n int, _ chatRequest) chatReply {
			if n <= 3 {
				return textReply("ok")
			}
			return chatReply{status: tc.status, header: http.Header{"Retry-After": {"120"}}, body: `{"error": {"message": "no"}}`}
		})
		f.streamText(4, acpStream(gooseInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, acpModel,
			`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Ran into this error: Rate limit exceeded."}}}`,
			`{"end": "end_turn"}`))
		e := acpEnv(t, f, "goose", llm)
		if got := e.waitState(e.run(e.acpProject("paint", colorManifest), "work"), "done", "error", "needs_login", "rate_limited"); got.State != tc.state {
			t.Fatalf("%d: want %s, got %+v", tc.status, tc.state, got)
		}
		if tc.state != "needs_login" {
			continue
		}
		if a := e.waitAsk(); !strings.Contains(a.Text, "key of its connection") || strings.Contains(a.Text, "terminal") {
			t.Fatalf("a refused key of a model is about the key of its connection, not a login in a terminal: %+v", a)
		}
	}
}

// TestACPTurnThatHandedInItsResultIsDoneThoughALaterCallWasRefused: a call of the model refused after the result,
// such as the title of a session Goose asks for on the side, does not undo the turn.
func TestACPTurnThatHandedInItsResultIsDoneThoughALaterCallWasRefused(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	llm := newLLM(t, func(n int, _ chatRequest) chatReply {
		if n <= 3 {
			return textReply("ok")
		}
		return chatReply{status: 429, header: http.Header{"Retry-After": {"120"}}, body: `{"error": {"message": "no"}}`}
	})
	f.streamText(4, acpStream(gooseInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, acpModel, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "goose", llm)
	if got := e.waitState(e.run(e.acpProject("paint", colorManifest), "work"), "done", "error", "needs_login", "rate_limited"); got.State != "done" || string(got.Result) != `{"color":"blue"}` {
		t.Fatalf("the turn that handed in its result is done: %+v", got)
	}
}

// TestACPTurnIsNotUndoneByARefusedCallOnTheSide: a call of the model without tools, such as the title of a session
// Goose asks for, tells nothing of the turn: a turn whose answer is its text is done though that call was refused.
func TestACPTurnIsNotUndoneByARefusedCallOnTheSide(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	llm := newLLM(t, func(n int, _ chatRequest) chatReply {
		if n <= 4 {
			return textReply("ok")
		}
		return chatReply{status: 429, header: http.Header{"Retry-After": {"120"}}, body: `{"error": {"message": "no"}}`}
	})
	f.streamText(4, acpStream(gooseInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		acpModel, `{"model": {"model": "m", "messages": [{"role": "user", "content": "a title for this session"}]}}`,
		`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Checked the page."}}}`, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "goose", llm)
	open := strings.Replace(noNetworkManifest, `"no_network": true,`, "", 1)
	if got := e.waitState(e.run(e.acpProject("open", open), "work"), "done", "error", "needs_login", "rate_limited"); got.State != "done" || !strings.Contains(string(got.Result), "Checked the page.") {
		t.Fatalf("a turn is done though a call on its side was refused: %+v", got)
	}
}

// TestACPAgentThatDiscoversTheGatewayOpensASession: Goose asks server/discover first; told the method is not there,
// it opens a session with initialize, which the gateway needs (told otherwise, it calls without one).
func TestACPAgentThatDiscoversTheGatewayOpensASession(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	f.streamText(4, acpStream(gooseInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"discover": true}`, `{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "goose", acpLLM(t))
	got := e.waitState(e.run(e.acpProject("paint", colorManifest), "work"), "done", "error")
	d := jsonLines[map[string]any](f.files(4)("discover.jsonl"))
	if got.State != "done" || len(d) != 1 || !strings.Contains(fmt.Sprint(d[0]["body"]), "-32601") {
		t.Fatalf("server/discover is a method the gateway does not have: %+v %v", got, d)
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
	f := newACP(t)
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"shell": "cat \"$HOME/.agents/skills/reader/SKILL.md\" > seen.txt; rm -rf \"$HOME/.agents\"; ln -s `+victims+` \"$HOME/.agents\""}`,
		`{"mcp": {"tool": "ask", "arguments": {"question": "Which color should the report use?"}}}`, `{"end": "end_turn"}`))
	f.streamText(5, acpStream(acpInit, `{"reply": "session/resume", "result": {}}`, `{"reply": "session/prompt"}`,
		`{"shell": "cat \"$HOME/.agents/skills/reader/SKILL.md\" > seen.txt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "goose", acpLLM(t))
	skilled := strings.Replace(colorManifest, `"prompt": "Ask the owner which color the report should use."}`,
		`"prompt": "Ask the owner which color the report should use.", "skills": ["reader"]}`, 1)
	p := e.acpProject("paint", skilled)
	e.must("deck.document_save", map[string]any{"project": "paint", "name": "skills/reader/SKILL.md", "kind": "skill",
		"text": "---\nname: reader\ndescription: Reads.\n---\nRead.\n"}, nil)
	id := e.run(p, "work")
	a := e.waitAsk()
	e.waitState(id, "waiting_answer")
	e.answer(a.ID, "blue")
	if got := e.waitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("want done, got %+v", got)
	}
	if entries, _ := os.ReadDir(victims); len(entries) != 1 {
		t.Fatalf("deck wrote the skills through the agent's symlink: %v", entries)
	}
	if b, _ := os.ReadFile(filepath.Join(f.call(5).cwd, "seen.txt")); !strings.Contains(string(b), "name: reader") {
		t.Fatalf("the skill is where ACP agents look for it on each turn: %q", b)
	}
}

// TestACPStopDuringSetupEndsTheAgent: a stop while the agent has not even answered initialize ends the turn.
func TestACPStopDuringSetupEndsTheAgent(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	f.streamText(4, `{"hang": true}`)
	e := acpEnv(t, f, "goose", acpLLM(t))
	id := e.run(e.acpProject("paint", colorManifest), "work")
	e.waitFor("the agent", func() bool { return f.calls() >= 4 && f.call(4).request("initialize") != nil })
	e.must("deck.process_stop", map[string]any{"process": id}, nil)
	e.waitState(id, "stopped")
}

// TestACPProviderNameIsUniqueAcrossWorkspaces: health and self-checks of providers go by name, so a name another
// workspace uses is refused.
func TestACPProviderNameIsUniqueAcrossWorkspaces(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	ctx := t.Context()
	if _, err := e.App.Store.DB.ExecContext(ctx, `INSERT INTO workspace (id, name) VALUES (2, 'other')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.App.Store.DB.ExecContext(ctx, `INSERT INTO connection (id, workspace_id, name, url) VALUES (9, 2, 'x', 'http://x/v1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.App.Store.DB.ExecContext(ctx, `INSERT INTO model (workspace_id, connection_id, name, model, agent) VALUES (2, 9, 'shared', 'm', 'goose')`); err != nil {
		t.Fatal(err)
	}
	code, out := e.agentModel(map[string]any{"name": "shared", "url": "http://localhost:1/v1", "model": "m", "agent": "goose"})
	if code != 422 || !strings.Contains(string(out), "another workspace") {
		t.Fatalf("a provider name of another workspace is refused: %d %s", code, out)
	}
}

// TestACPProviderChangedDuringItsSelfCheckIsCheckedAgain: the first check ran on the old definition, so a change
// while it runs starts another one before the provider counts as green.
func TestACPProviderChangedDuringItsSelfCheckIsCheckedAgain(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	f.selfCheck()
	for i := 4; i <= 6; i++ {
		f.stream(i, "selfcheck_"+itoa(int64(i-3))+".jsonl")
	}
	slow := strings.Replace(acpInit, `{"reply": "initialize"`, `{"shell": "sleep 1"}`+"\n"+`{"reply": "initialize"`, 1)
	b, _ := os.ReadFile("testdata/acp/selfcheck_1.jsonl")
	f.streamText(1, strings.Replace(string(b), acpInit, slow, 1))
	llm := acpLLM(t)
	e := newEnv(t, nil, func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.dir, "goose")}
		c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir, nil}
	})
	e.mustAgentModel(map[string]any{"name": "acp", "url": llm.URL, "model": "m", "agent": "goose"})
	e.waitFor("the first check", func() bool { return f.calls() >= 1 })
	e.mustAgentModel(map[string]any{"name": "acp", "url": llm.URL, "model": "m2", "agent": "goose"})
	e.waitWithin("the second check", 40*time.Second, func() bool {
		h, _ := health.Get(t.Context(), e.App.Store.Q, health.Of{}, "provider:acp")
		return f.calls() >= 6 && h.Status == "ok"
	})
	if m := f.call(f.calls()).env["GOOSE_MODEL"]; m != "m2" {
		t.Fatalf("the second check runs on the changed provider: %s", m)
	}
}

// TestACPCountsTokensOfCompressedReplies: a model API that compresses its answers still has its tokens counted.
func TestACPCountsTokensOfCompressedReplies(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := []byte(textReply("ok").body)
		w.Header().Set("Content-Type", "application/json")
		if strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			var b bytes.Buffer
			zw := gzip.NewWriter(&b)
			_, _ = zw.Write(body)
			_ = zw.Close()
			w.Header().Set("Content-Encoding", "gzip")
			body = b.Bytes()
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	f := newACP(t)
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, acpModel,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "goose", &fakeLLM{t: t, URL: srv.URL + "/v1"})
	id := e.run(e.acpProject("paint", colorManifest), "work")
	e.waitState(id, "done")
	var calls []map[string]any
	e.modelCalls("", id, &calls)
	if len(calls) != 1 || calls[0]["gen_ai.usage.output_tokens"] != 10.0 {
		t.Fatalf("the tokens of a compressed reply are counted: %v", calls)
	}
}

// TestACPProviderIsNotUsedBeforeItsFirstSelfCheck: a command started right after the provider is added waits for the
// self-check instead of running on an agent nobody checked.
func TestACPProviderIsNotUsedBeforeItsFirstSelfCheck(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	f.selfCheck()
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	if err := os.WriteFile(filepath.Join(f.dir, "slowversion"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	llm := acpLLM(t)
	e := newEnv(t, nil, func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.dir, "goose")}
		c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir, nil}
	})
	p := e.createProject("paint", colorManifest)
	e.must("deck.secret_set", map[string]any{"name": "k", "value": "v"}, nil)
	go func() {
		_, _ = e.agentModel(map[string]any{"name": "acp", "url": llm.URL, "model": "m", "agent": "goose"})
	}()
	e.waitFor("the provider", func() bool {
		_, err := e.App.Store.Q.GetModel(t.Context(), db.GetModelParams{WorkspaceID: 1, Name: "acp"})
		return err == nil
	})
	e.useModels("paint.work", "acp")
	id := e.run(p, "work")
	e.waitFor("the self-check", func() bool {
		h, err := health.Get(t.Context(), e.App.Store.Q, health.Of{}, "provider:acp")
		return err == nil && h.Status == "ok"
	})
	got := e.waitState(id, "done", "error")
	if got.State != "done" || !strings.Contains(string(f.call(1).request("session/prompt")), "self-check of deck") {
		t.Fatalf("the first agent run is the self-check, the command waits for it: %+v", got)
	}
}

// TestAProviderTakesItsOwnSelfCheckCeiling: a slow model gets more time for its self-check than deck's default.
func TestAProviderTakesItsOwnSelfCheckCeiling(t *testing.T) {
	f := newACP(t)
	f.streamText(1, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, acpModel, `{"hang": true}`))
	llm := newLLM(t, func(int, chatRequest) chatReply { return textReply("ok") })
	e := newEnv(t, nil, func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.dir, "goose")}
		c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir, nil}
		c.ACPCallLimit = 2 * time.Second
		c.SelfCheckCeiling = time.Second
	})
	e.mustAgentModel(map[string]any{"name": "acp", "url": llm.URL, "model": "m", "agent": "goose", "check_minutes": 1})
	time.Sleep(2500 * time.Millisecond)
	h, err := health.Get(t.Context(), e.App.Store.Q, health.Of{}, "provider:acp")
	if err != nil || h.Status != "checking" {
		t.Fatalf("the self-check of a provider with a minute still runs past deck's second: %+v %v", h, err)
	}
}

// TestACPAgentDoesNotReadTheOwnersHome: the sandbox keeps the owner's home closed from an ACP agent, as from scripts.
func TestACPAgentDoesNotReadTheOwnersHome(t *testing.T) {
	owner := ownerHome(t)
	_ = os.WriteFile(filepath.Join(owner, ".zshrc"), []byte("private"), 0o600)
	f := newACP(t)
	t.Setenv("HOME", owner)
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"shell": "cat `+filepath.Join(owner, ".zshrc")+`"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"written": 0}}}}`, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "goose", acpLLM(t))
	id := e.run(e.acpProject("paint", writtenManifest), "work")
	e.waitState(id, "done")
	if strings.Contains(f.call(4).shell[0]["out"], "private") {
		t.Fatalf("the agent does not read the owner's home: %v", f.call(4).shell)
	}
}

// TestACPAgentReachesALocalServiceItsProjectNames: a service of the owner's machine that the project names by IP and
// port in domains.read is reached by the shell of an ACP agent through deck's proxy, as by a script.
func TestACPAgentReachesALocalServiceItsProjectNames(t *testing.T) {
	t.Parallel()
	local := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { _, _ = w.Write([]byte("hello")) })
	addr := strings.TrimPrefix(local.URL, "http://")
	f := newACP(t)
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"shell": "curl -s -m 5 -o /dev/null -w '%{http_code}' http://`+addr+`/"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"written": 0}}}}`, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "goose", acpLLM(t))
	m := strings.Replace(writtenManifest, `{"version": 12,`, fmt.Sprintf(`{"version": 12, "domains": {"read": [%q]},`, addr), 1)
	e.waitState(e.run(e.acpProject("paint", m), "work"), "done")
	if got := f.call(4).shell[0]["out"]; got != "200" || len(local.requests()) != 1 {
		t.Fatalf("the shell reaches the service its project names: %q, %d requests", got, len(local.requests()))
	}
}

// TestAnACPAgentCallsOnlyItsModelThroughDeck: deck's key goes with the calls of the model of a turn, not with any
// other request to the API of the connection, as for a script.
func TestAnACPAgentCallsOnlyItsModelThroughDeck(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	llm := acpLLM(t)
	f.streamText(4, acpStream(gooseInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"get": "files"}`, acpModel, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "goose", llm)
	e.waitState(e.run(e.acpProject("paint", colorManifest), "work"), "done", "error")
	got := jsonLines[map[string]any](f.files(4)("get.jsonl"))
	if len(got) != 1 || got[0]["status"] != 403.0 {
		t.Fatalf("a request other than a call of the model is refused: %v", got)
	}
}

// TestACPCommandWithAFolderRunsOnALocalModel: a folder command that requires no_cloud is red in health while no model
// fits it; on a local ACP model it works in the owner's folder, its skills in the session home, the owner's kept.
func TestACPCommandWithAFolderRunsOnALocalModel(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	e := newEnv(t, ok, func(c *app.Config) {
		c.ACPBinaries = map[string]string{"goose": filepath.Join(f.dir, "goose")}
		c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir, nil}
		c.ACPCallLimit = 2 * time.Second
	})
	cmd := strings.Replace(folderCommand("files", "true"), `"executor":`, `"requires": ["no_cloud"], "executor":`, 1)
	cmd = strings.Replace(cmd, `"prompt": "Do files."`, `"prompt": "Do files.", "skills": ["reader"]`, 1)
	p := e.createProject("music", testManifest([]string{cmd}, nil))
	e.must("deck.document_save", map[string]any{"project": "music", "name": "skills/reader/SKILL.md", "kind": "skill",
		"text": "---\nname: reader\ndescription: Reads.\n---\nRead.\n"}, nil)
	folder := repoFolder(t)
	own := filepath.Join(folder, ".agents", "skills", "own", "SKILL.md")
	if err := errors.Join(os.MkdirAll(filepath.Dir(own), 0o700), os.WriteFile(own, []byte("the owner's"), 0o600)); err != nil {
		t.Fatal(err)
	}
	e.must("deck.folder_add", map[string]any{"project": "music", "path": folder}, nil)
	unfit := func() *struct{ Subject, Status, Error string } {
		var rows []struct{ Subject, Status, Error string }
		e.must("deck.health_list", map[string]any{"project": "music"}, &rows)
		for _, r := range rows {
			if r.Subject == "command:files" {
				return &r
			}
		}
		return nil
	}
	if r := unfit(); r == nil || r.Status != "error" || !strings.Contains(r.Error, "no provider fits music.files") {
		t.Fatalf("a command no provider fits is a red row of health: %+v", r)
	}
	e.addACP(f, 1, "local", "goose", acpLLM(t), true)
	if r := unfit(); r != nil {
		t.Fatalf("the row goes once a model fits: %+v", r)
	}
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"shell": "cat \"$HOME/.agents/skills/reader/SKILL.md\" > seen.txt; ls .agents/skills > listed.txt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"n": 1}}}}`, `{"end": "end_turn"}`))
	id := e.run(p, "files")
	if got := e.waitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the command runs on the local model: %+v", got)
	}
	if c := f.call(4); c.cwd != folder {
		t.Fatalf("the agent works in the owner's folder: %s", c.cwd)
	}
	if b, _ := os.ReadFile(filepath.Join(folder, "seen.txt")); !strings.Contains(string(b), "name: reader") {
		t.Fatalf("the skill is in the home of the session, where the agent looks for it: %q", b)
	}
	listed, _ := os.ReadFile(filepath.Join(folder, "listed.txt"))
	if b, _ := os.ReadFile(own); string(b) != "the owner's" || string(listed) != "own\n" {
		t.Fatalf("deck writes no skill into the owner's folder and keeps the owner's: %q %q", b, listed)
	}
}
