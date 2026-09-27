package scenarios

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
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

const acpInit = `{"reply": "initialize", "result": {"protocolVersion": 1, "agentCapabilities": {"mcpCapabilities": {"http": true}, "sessionCapabilities": {"resume": {}, "list": {}}}, "authMethods": []}}`

const writtenManifest = `{"version": 5,
  "commands": [{"name": "work", "title": "Work", "description": "Writes a file", "class": "internal",
    "result": {"type": "object", "required": ["written"], "properties": {"written": {"type": "integer"}}},
    "executor": {"type": "agent", "prompt": "Write the file."}}]}`

const acpModel = `{"model": {"model": "m", "messages": [{"role": "user", "content": "go"}]}}`

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
	e := acpEnv(t, f, "dsh", llm)
	home := e.App.Cfg.Home
	if err := os.WriteFile(filepath.Join(home, "token"), []byte("owner-token-value"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.dir, "probe"), []byte(filepath.Join(home, "token")), 0o600); err != nil {
		t.Fatal(err)
	}
	e.App.Checks(t.Context(), time.Now().Add(time.Hour))
	e.waitFor("the version check", func() bool { _, err := os.Stat(filepath.Join(f.dir, "probe.out")); return err == nil })
	f.streamText(4, strings.Join([]string{acpInit,
		`{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Painting."}}}`,
		`{"shell": "echo painted > canvas.txt"}`,
		`{"shell": "cat ` + filepath.Join(home, "token") + `"}`,
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
	if h := c.env["HOME"]; !strings.HasPrefix(h, filepath.Join(home, "acp", "sessions")) {
		t.Fatalf("the agent lives in a home of its own session: %s", h)
	}
	var calls []map[string]any
	e.must("deck.llm_calls_list", map[string]any{"process": id}, &calls)
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
// question waits less, the turn ends and the owner's answer comes back through session/resume of the same session.
func TestACPQuestionStopsTheTurnBeforeTheAgentTimesOutAndTheAnswerResumes(t *testing.T) {
	f := newACP(t)
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"update": {"sessionUpdate": "agent_thought_chunk", "content": {"type": "text", "text": "Read "}}}`,
		`{"update": {"sessionUpdate": "agent_thought_chunk", "content": {"type": "text", "text": "first."}}}`,
		`{"update": {"sessionUpdate": "tool_call", "toolCallId": "c1", "title": "read", "kind": "read", "status": "pending"}}`,
		`{"update": {"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "in_progress", "rawInput": {"path": "a.txt"}}}`,
		`{"update": {"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "completed", "rawInput": {"path": "a.txt"}, "rawOutput": {"output": "text of a"}}}`,
		`{"mcp": {"tool": "ask", "arguments": {"question": "Which color should the report use?"}}}`, `{"end": "end_turn"}`))
	f.streamText(5, acpStream(acpInit, `{"reply": "session/resume", "result": {}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "opencode", acpLLM(t), func(c *app.Config) { c.QuestionWait = 10 * time.Minute })
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
	if !slices.Equal(kinds[:4], []string{"thought", "tool", "tool", "tool_update"}) || lines[0]["text"] != "Read first." ||
		lines[1]["id"] != "c1" || lines[1]["title"] != "read" || lines[2]["text"] != `{"path":"a.txt"}` ||
		lines[3]["id"] != "c1" || lines[3]["status"] != "completed" || lines[3]["text"] != `{"output":"text of a"}` {
		t.Fatalf("the log keeps the session updates of ACP: a thought whole, the call, its late input and its end: %v", lines)
	}
	if p := f.call(4).port; p != "bound true" {
		t.Fatalf("the sandbox lets OpenCode serve itself on its own port: %q", p)
	}
	e.answer(a.ID, "blue")
	got := e.waitState(id, "done", "error")
	c := f.call(5)
	var resume struct{ SessionID string }
	_ = json.Unmarshal(c.request("session/resume"), &resume)
	if got.State != "done" || string(got.Result) != `{"color":"blue"}` || resume.SessionID != "s-1" || c.violation != "" {
		t.Fatalf("the answer resumes the session the agent opened: %+v %+v %s", got, resume, c.violation)
	}
	if !strings.Contains(string(c.request("session/prompt")), "blue") {
		t.Fatalf("the resume brings the answer: %s", c.request("session/prompt"))
	}
}

// TestACPAgentPermissionIsAllowedOnceWithoutTheOwner: inside the sandbox of deck the agent's own questions about its
// tools are answered with allow_once; "always" stays the owner's button.
func TestACPAgentPermissionIsAllowedOnceWithoutTheOwner(t *testing.T) {
	f := newACP(t)
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"permission": {"toolCall": {"toolCallId": "c1", "title": "cat notes", "kind": "execute"}, "options": [{"optionId": "always", "name": "Always", "kind": "allow_always"}, {"optionId": "once", "name": "Once", "kind": "allow_once"}, {"optionId": "no", "name": "No", "kind": "reject_once"}]}}`,
		`{"permission": {"toolCall": {"toolCallId": "c2"}, "options": [{"optionId": "always", "name": "Always", "kind": "allow_always"}, {"optionId": "no", "name": "No", "kind": "reject_once"}]}}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "red"}}}}`, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "opencode", acpLLM(t))
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

// TestACPExternalToolStillWaitsForYes: the class rules of the gateway hold for an ACP agent as for any other.
func TestACPExternalToolStillWaitsForYes(t *testing.T) {
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{"id": "ext-1"}`) })
	f := newACP(t)
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "notes__post", "arguments": {"id": "a1", "text": "hi"}}}`, `{"end": "end_turn"}`))
	f.streamText(5, acpStream(acpInit, `{"reply": "session/resume", "result": {}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"posted": true}}}}`, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "dsh", acpLLM(t))
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

// TestACPProjectWithoutNetworkHasNoWebThroughTheProxy: the proxy of the sandbox carries the agent only to public
// addresses, and nowhere in a project without network, where the agent's own web tools are off too.
func TestACPProjectWithoutNetworkHasNoWebThroughTheProxy(t *testing.T) {
	site := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { jsonReply(w, `{}`) })
	curl := `{"shell": "curl -s --noproxy '' --proxy \"$HTTP_PROXY\" ` + site.URL + `/page"}`
	f := newACP(t)
	for _, n := range []int{4, 5} {
		f.streamText(n, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
			curl, `{"shell": "curl -s -m 3 -o /dev/null -w '%{http_code}' --noproxy '*' `+site.URL+`/direct"}`,
			`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Checked "}}}`,
			`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "the page."}}}`,
			`{"mcp": {"tool": "result_submit", "arguments": {"result": {"written": 0}}}}`, `{"end": "end_turn"}`))
	}
	e := acpEnv(t, f, "opencode", acpLLM(t))
	open := strings.Replace(noNetworkManifest, `"no_network": true,`, "", 1)
	if got := e.waitState(e.run(e.acpProject("open", open), "work"), "done"); !strings.Contains(string(got.Result), "Checked the page.") {
		t.Fatalf("what the agent said is the result of a command without a schema: %s", got.Result)
	}
	e.waitState(e.run(e.acpProject("private", noNetworkManifest), "work"), "done")
	web, off := f.call(4), f.call(5)
	if !strings.Contains(web.shell[0]["out"], "not a public address") || !strings.Contains(off.shell[0]["out"], "not reachable") || len(site.requests()) != 0 {
		t.Fatalf("the proxy carries no agent to deck or local services, and nowhere without network: %q / %q", web.shell[0]["out"], off.shell[0]["out"])
	}
	if web.shell[1]["out"] != "000" || off.shell[1]["out"] != "000" {
		t.Fatalf("past the proxy the sandbox has no network: %q / %q", web.shell[1]["out"], off.shell[1]["out"])
	}
	if !strings.Contains(off.env["OPENCODE_CONFIG_CONTENT"], `"webfetch":"deny"`) || strings.Contains(web.env["OPENCODE_CONFIG_CONTENT"], `"webfetch":"deny"`) {
		t.Fatalf("without network the agent's own web tools are off: %s", off.env["OPENCODE_CONFIG_CONTENT"])
	}
}

// TestACPSessionTheAgentLostStartsOver: a resume the agent refuses for a session it no longer lists starts anew.
func TestACPSessionTheAgentLostStartsOver(t *testing.T) {
	f := newACP(t)
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
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
	e := acpEnv(t, f, "dsh", acpLLM(t))
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
	f := newACP(t)
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, `{"hang": true}`))
	e := acpEnv(t, f, "dsh", acpLLM(t))
	id := e.run(e.acpProject("paint", colorManifest), "work")
	e.waitFor("the turn", func() bool { return f.calls() >= 4 && f.call(4).request("session/prompt") != nil })
	e.must("deck.process_stop", map[string]any{"process": id}, nil)
	e.waitState(id, "stopped")
	e.waitFor("the cancel", func() bool { return f.call(4).request("session/cancel") != nil })
}

// TestACPRecordedTurnsOfBothAgents replays turns recorded from dsh and OpenCode through the same adapter.
func TestACPRecordedTurnsOfBothAgents(t *testing.T) {
	for _, tc := range []struct{ agent, fixture string }{{"dsh", "dsh_turn.jsonl"}, {"opencode", "opencode_turn.jsonl"}} {
		t.Run(tc.agent, func(t *testing.T) {
			f := newACP(t)
			f.stream(4, tc.fixture)
			e := acpEnv(t, f, tc.agent, acpLLM(t))
			got := e.waitState(e.run(e.acpProject("notes", writtenManifest), "work"), "done", "error")
			c := f.call(4)
			if got.State != "done" || string(got.Result) != `{"written":1}` || c.violation != "" {
				t.Fatalf("the recorded turn hands its result in: %+v %s", got, c.violation)
			}
		})
	}
}

// TestACPSelfCheckIsRedWhenTheSandboxOrTheProxyFails: the probes deck reads itself turn the self-check red, told
// apart as a broken adapter (leak, open network, no tokens) or a model that did not do the steps.
func TestACPSelfCheckIsRedWhenTheSandboxOrTheProxyFails(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("testdata/acp", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	first, second, third := read("selfcheck_1.jsonl"), read("selfcheck_2.jsonl"), read("selfcheck_3.jsonl")
	adapter, model := "self-check of dsh", "the model does not manage"
	for _, tc := range []struct {
		streams       []string
		want, verdict string
	}{
		{[]string{strings.Replace(first, `{"shell": "sh probe.sh"}`, `{"shell": "echo 0 > probe.code"}`, 1)}, "read a file deck hides", adapter},
		{[]string{strings.Replace(first, `{"shell": "sh probe.sh"}`+"\n", "", 1)}, "did not run", model},
		{[]string{strings.Replace(first, acpModel+"\n", "", 1)}, "token counts", adapter},
		{[]string{strings.Replace(first, `{"mcp": {"tool": "ask", "arguments": {"question": "Which answer should the result carry?"}}}`+"\n", "", 1)}, "question", model},
		{[]string{first, second, strings.Replace(third, `{"shell": "sh net.sh"}`, `{"shell": "echo 200 > net.out"}`, 1)}, "no network", adapter},
		{[]string{first, second, strings.Replace(third, `{"shell": "sh net.sh"}`+"\n", "", 1)}, "no network", model},
	} {
		f := newACP(t)
		for i, s := range tc.streams {
			f.streamText(i+1, s)
		}
		e, h := acpAdded(t, f, "dsh", acpLLM(t))
		if h.Status != "error" || !strings.Contains(h.Error, tc.want) || !strings.HasPrefix(h.Error, tc.verdict) {
			t.Fatalf("the self-check is red with %q, told as %q: %+v", tc.want, tc.verdict, h)
		}
		var pl struct {
			Providers []struct{ Agent, Verdict string }
		}
		e.must("deck.providers_list", nil, &pl)
		verdict := map[string]string{adapter: "adapter", model: "model"}[tc.verdict]
		if i := slices.IndexFunc(pl.Providers, func(p struct{ Agent, Verdict string }) bool { return p.Agent == "dsh" }); i < 0 || pl.Providers[i].Verdict != verdict {
			t.Fatalf("the providers list shows the agent and the verdict %q: %+v", verdict, pl.Providers)
		}
		f.streamText(0, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
			`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
		id := e.run(e.acpProject("paint", colorManifest), "work")
		e.waitState(id, "waiting_provider")
		if n := f.calls(); n > len(tc.streams)+1 {
			t.Fatalf("a process on a red provider waits: %d calls", n)
		}
	}
}

// TestFakeACPRejectsWhatTheSchemaLacks: the fake agent refuses a method or a field the ACP schema does not have.
func TestFakeACPRejectsWhatTheSchemaLacks(t *testing.T) {
	for _, tc := range []struct{ line, want string }{
		{`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":1,"bogus":true}}`, "unknown field bogus"},
		{`{"jsonrpc":"2.0","id":1,"method":"session/fork","params":{}}`, "unknown method"},
		{`{"jsonrpc":"2.0","id":1,"method":"session/new","params":{"cwd":"/tmp"}}`, "does not match the schema"},
	} {
		f := newACP(t)
		f.streamText(0, acpInit)
		cmd := exec.Command(filepath.Join(f.dir, "dsh"))
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
		e := acpEnv(t, f, "dsh", llm)
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

// TestACPDeckWritesNothingThroughWhatTheAgentLeft: skills and the profile files go into folders the agent writes
// to, so deck replaces what it finds there rather than follow a symlink the agent planted in an earlier turn.
func TestACPDeckWritesNothingThroughWhatTheAgentLeft(t *testing.T) {
	victims := t.TempDir()
	victim := filepath.Join(victims, "owner.txt")
	if err := os.WriteFile(victim, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := newACP(t)
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"shell": "cat .agents/skills/reader/SKILL.md > seen.txt; rm -rf .agents; ln -s `+victims+` .agents; ln -sf `+victim+` \"$HOME/deck.patch.yml\""}`,
		`{"mcp": {"tool": "ask", "arguments": {"question": "Which color should the report use?"}}}`, `{"end": "end_turn"}`))
	f.streamText(5, acpStream(acpInit, `{"reply": "session/resume", "result": {}}`, `{"reply": "session/prompt"}`,
		`{"shell": "cat .agents/skills/reader/SKILL.md > seen.txt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "dsh", acpLLM(t))
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
	if b, _ := os.ReadFile(victim); string(b) != "keep" {
		t.Fatalf("deck wrote the profile file through the agent's symlink: %q", b)
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
	f := newACP(t)
	f.streamText(4, `{"hang": true}`)
	e := acpEnv(t, f, "dsh", acpLLM(t))
	id := e.run(e.acpProject("paint", colorManifest), "work")
	e.waitFor("the agent", func() bool { return f.calls() >= 4 && f.call(4).request("initialize") != nil })
	e.must("deck.process_stop", map[string]any{"process": id}, nil)
	e.waitState(id, "stopped")
}

// TestACPProviderNameIsUniqueAcrossWorkspaces: health and self-checks of providers go by name, so a name another
// workspace uses is refused.
func TestACPProviderNameIsUniqueAcrossWorkspaces(t *testing.T) {
	e := newEnv(t, nil)
	ctx := t.Context()
	if _, err := e.App.Store.DB.ExecContext(ctx, `INSERT INTO workspace (id, name) VALUES (2, 'other')`); err != nil {
		t.Fatal(err)
	}
	if _, err := e.App.Store.DB.ExecContext(ctx, `INSERT INTO setting (workspace_id, key, value) VALUES (2, 'provider:shared', '{"url":"http://x/v1","model":"m","agent":"dsh"}')`); err != nil {
		t.Fatal(err)
	}
	code, out := e.call("deck.provider_add", map[string]any{"name": "shared", "url": "http://localhost:1/v1", "model": "m"})
	if code != 422 || !strings.Contains(string(out), "another workspace") {
		t.Fatalf("a provider name of another workspace is refused: %d %s", code, out)
	}
}

// TestACPProviderChangedDuringItsSelfCheckIsCheckedAgain: the first check ran on the old definition, so a change
// while it runs starts another one before the provider counts as green.
func TestACPProviderChangedDuringItsSelfCheckIsCheckedAgain(t *testing.T) {
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
		c.ACPBinaries = map[string]string{"dsh": filepath.Join(f.dir, "dsh")}
		c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir}
	})
	e.must("deck.provider_add", map[string]any{"name": "acp", "url": llm.URL, "model": "m", "agent": "dsh"}, nil)
	e.waitFor("the first check", func() bool { return f.calls() >= 1 })
	e.must("deck.provider_add", map[string]any{"name": "acp", "url": llm.URL, "model": "m2", "agent": "dsh"}, nil)
	e.waitFor("the second check", func() bool {
		h, _ := e.App.Store.Q.GetHealth(t.Context(), "provider:acp")
		return f.calls() >= 6 && h.Status == "ok"
	})
	if b, _ := os.ReadFile(filepath.Join(f.dir, "patch")); !strings.Contains(string(b), `"model":"m2"`) {
		t.Fatalf("the second check runs on the changed provider: %s", b)
	}
}

// TestACPCountsTokensOfCompressedReplies: a model API that compresses its answers still has its tokens counted.
func TestACPCountsTokensOfCompressedReplies(t *testing.T) {
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
	e := acpEnv(t, f, "dsh", &fakeLLM{t: t, URL: srv.URL + "/v1"})
	id := e.run(e.acpProject("paint", colorManifest), "work")
	e.waitState(id, "done")
	var calls []map[string]any
	e.must("deck.llm_calls_list", map[string]any{"process": id}, &calls)
	if len(calls) != 1 || calls[0]["gen_ai.usage.output_tokens"] != 10.0 {
		t.Fatalf("the tokens of a compressed reply are counted: %v", calls)
	}
}

// TestACPProviderIsNotUsedBeforeItsFirstSelfCheck: a command started right after the provider is added waits for the
// self-check instead of running on an agent nobody checked.
func TestACPProviderIsNotUsedBeforeItsFirstSelfCheck(t *testing.T) {
	f := newACP(t)
	f.selfCheck()
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	if err := os.WriteFile(filepath.Join(f.dir, "slowversion"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	llm := acpLLM(t)
	e := newEnv(t, nil, func(c *app.Config) {
		c.ACPBinaries = map[string]string{"dsh": filepath.Join(f.dir, "dsh")}
		c.Sandbox = fakeDirSandbox{sandbox.New(), f.dir}
	})
	p := e.createProject("paint", colorManifest)
	e.must("deck.secret_set", map[string]any{"name": "k", "value": "v"}, nil)
	go func() {
		_, _ = e.call("deck.provider_add", map[string]any{"name": "acp", "url": llm.URL, "model": "m", "agent": "dsh"})
	}()
	e.waitFor("the provider", func() bool {
		_, err := e.App.Store.Q.GetSetting(t.Context(), db.GetSettingParams{WorkspaceID: 1, Key: "provider:acp"})
		return err == nil
	})
	e.must("deck.provider_set", map[string]any{"command": "paint.work", "provider": "acp"}, nil)
	id := e.run(p, "work")
	e.waitFor("the self-check", func() bool {
		h, err := e.App.Store.Q.GetHealth(t.Context(), "provider:acp")
		return err == nil && h.Status == "ok"
	})
	got := e.waitState(id, "done", "error")
	if got.State != "done" || !strings.Contains(string(f.call(1).request("session/prompt")), "self-check of deck") {
		t.Fatalf("the first agent run is the self-check, the command waits for it: %+v", got)
	}
}
