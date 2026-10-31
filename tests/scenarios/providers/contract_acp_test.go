package providers

import (
	"bytes"
	"cmp"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deck2/tests/harness"
)

// TestACPModelAPIThatRefusesEchoedReasoningTakesTheCallWithout: an API that refuses the reasoning Goose hands back
// in its history, as Groq does, gets the call again without it, and the later calls of the model without asking.
func TestACPModelAPIThatRefusesEchoedReasoningTakesTheCallWithout(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	refused := 0
	llm := harness.NewLLM(t, func(n int, req harness.ChatRequest) harness.ChatReply {
		for _, m := range req.Messages {
			if _, ok := m["reasoning_content"]; ok {
				refused++
				return harness.ChatReply{Status: 400, Body: `{"error": {"message": "'messages.1' : for 'role:assistant' the following must be satisfied[('messages.1' : property 'reasoning_content' is unsupported)]", "type": "invalid_request_error"}}`}
			}
		}
		return harness.TextReply("ok")
	})
	echoed := `{"model": {"model": "m", "messages": [{"role": "user", "content": "go"}, {"role": "assistant", "content": null, "reasoning_content": "I run it", "tool_calls": [{"id": "c1", "type": "function", "function": {"name": "shell", "arguments": "{}"}}]}, {"role": "tool", "tool_call_id": "c1", "content": "done"}], "tools": [{"type": "function", "function": {"name": "shell"}}]}}`
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, echoed, echoed,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	f.StreamText(5, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-2"}}`, `{"reply": "session/prompt"}`, echoed,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, llm))
	p := e.AcpProject("paint", harness.ColorManifest)
	if got := e.WaitState(e.Run(p, "work"), "done", "error"); got.State != "done" {
		t.Fatalf("the turn goes on: %+v", got)
	}
	for _, m := range f.Call(4).Model {
		if m["status"] != float64(200) {
			t.Fatalf("the agent gets the answer of the call without its reasoning: %+v", m)
		}
	}
	if refused != 1 {
		t.Fatalf("once refused, the calls of the turn go without reasoning: refused %d", refused)
	}
	if got := e.WaitState(e.Run(p, "work"), "done", "error"); got.State != "done" || refused != 1 || f.Call(5).Model[0]["status"] != float64(200) {
		t.Fatalf("the next turn on the model goes without reasoning from its first call: %+v refused %d", got, refused)
	}
}

// TestACPRefusalOfTheModelAPIIsTheEndOfATurnWithoutItsResult: a call of the turn the API refused ends a turn that
// handed in nothing with the words of the API, and does not undo one that handed in its result.
func TestACPRefusalOfTheModelAPIIsTheEndOfATurnWithoutItsResult(t *testing.T) {
	t.Parallel()
	submit := `{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`
	for _, tc := range []struct {
		name  string
		steps []string
		state string
		want  string
	}{
		{"no result", []string{harness.AcpModel}, "error", "refused a call of the turn: 413"},
		{"result first", []string{submit, harness.AcpModel}, "done", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := harness.NewACP(t)
			llm := harness.NewLLM(t, func(n int, _ harness.ChatRequest) harness.ChatReply {
				if n <= 3 {
					return harness.TextReply("ok")
				}
				return harness.ChatReply{Status: 413, Body: `{"error": {"message": "the request is too large for this model"}}`}
			})
			steps := append([]string{harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`}, tc.steps...)
			f.StreamText(4, harness.AcpStream(append(steps, `{"end": "end_turn"}`)...))
			e := harness.New(t, harness.WithACP(f, llm))
			got := e.WaitState(e.Run(e.AcpProject("paint", harness.ColorManifest), "work"), "done", "error")
			if got.State != tc.state || !strings.Contains(got.Error, tc.want) {
				t.Fatalf("want %s with %q: %+v", tc.state, tc.want, got)
			}
		})
	}
}

// TestACPSessionTheAgentLostStartsOver: a resume the agent refuses for a session it no longer lists starts anew.
func TestACPSessionTheAgentLostStartsOver(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Hi."}}}`,
		`{"update": {"sessionUpdate": "agent_thought_chunk", "content": {"type": "text", "text": "Read "}}}`,
		`{"update": {"sessionUpdate": "agent_thought_chunk", "content": {"type": "text", "text": "first."}}}`,
		`{"update": {"sessionUpdate": "tool_call", "toolCallId": "c1", "title": "read", "kind": "read", "status": "pending"}}`,
		`{"update": {"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "in_progress", "rawInput": {"path": "a.txt"}}}`,
		`{"update": {"sessionUpdate": "tool_call_update", "toolCallId": "c1", "status": "completed", "rawInput": {"path": "a.txt"}, "rawOutput": {"output": "text of a"}}}`,
		`{"mcp": {"tool": "ask", "arguments": {"question": "Which color should the report use?"}}}`, `{"end": "end_turn"}`))
	f.StreamText(5, harness.AcpStream(harness.AcpInit, `{"reply": "session/resume", "error": {"code": -32602, "message": "Invalid params: session is not resumable: s-1"}}`,
		`{"reply": "session/list", "result": {"sessions": []}}`))
	f.StreamText(6, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-2"}}`, `{"reply": "session/prompt"}`, `{"end": "end_turn"}`))
	f.StreamText(7, harness.AcpStream(harness.AcpInit, `{"reply": "session/resume", "result": {}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	id := e.Run(e.AcpProject("paint", harness.ColorManifest), "work")
	a := e.WaitAsk()
	e.WaitState(id, "waiting_answer")
	e.Answer(a.ID, "blue")
	// Three starts of the agent in the sandbox, one after another.
	got := e.WaitState(id, "done", "error")
	var resume struct{ SessionID string }
	if f.Calls() == 7 {
		_ = json.Unmarshal(f.Call(7).Request("session/resume"), &resume)
	}
	if got.State != "done" || f.Call(6).Request("session/new") == nil || resume.SessionID != "s-2" {
		t.Fatalf("a session the agent lost starts over and the answer reaches the new one: %+v, %d calls, %+v", got, f.Calls(), resume)
	}
}

// TestACPRecordedTurnOfGoose replays a turn recorded from Goose through the adapter.
func TestACPRecordedTurnOfGoose(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	f.Stream(4, "goose_turn.jsonl")
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	got := e.WaitState(e.Run(e.AcpProject("notes", writtenManifest), "work"), "done", "error")
	c := f.Call(4)
	if got.State != "done" || string(got.Result) != `{"written":1}` || c.Violation != "" {
		t.Fatalf("the recorded turn hands its result in: %+v %s", got, c.Violation)
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
		f := harness.NewACP(t)
		f.StreamText(0, harness.AcpInit)
		cmd := exec.Command(filepath.Join(f.Dir, "goose"))
		cmd.Stdin = strings.NewReader(tc.line + "\n")
		_ = cmd.Run()
		if v := f.Call(1).Violation; !strings.Contains(v, tc.want) {
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
		t.Run(cmp.Or(tc.want, tc.state), func(t *testing.T) {
			t.Parallel()
			f := harness.NewACP(t)
			llm := harness.NewLLM(t, func(n int, _ harness.ChatRequest) harness.ChatReply {
				if n <= 3 || tc.status == 200 {
					return harness.TextReply("ok")
				}
				return harness.ChatReply{Status: tc.status, Header: http.Header{"Retry-After": {"120"}}, Body: `{"error": {"message": "no"}}`}
			})
			// A result handed in wins over a later refusal, so a refused key or a limit comes before any.
			submit := `{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`
			if tc.status != 200 {
				submit = harness.AcpModel
			}
			f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, harness.AcpModel,
				submit, tc.end))
			e := harness.New(t, harness.WithACP(f, llm))
			id := e.Run(e.AcpProject("paint", harness.ColorManifest), "work")
			got := e.WaitState(id, "done", "error", "needs_login", "rate_limited")
			if got.State != tc.state || !strings.Contains(got.Error, tc.want) {
				t.Fatalf("%s %d: want %s with %q, got %+v", tc.end, tc.status, tc.state, tc.want, got)
			}
			if tc.status == 429 {
				pp, _ := e.App.Store.Q.GetProcess(t.Context(), id)
				if left := time.Until(time.UnixMilli(pp.PauseUntilMs.Int64)); left < 100*time.Second || left > 121*time.Second {
					t.Fatalf("the pause lasts until Retry-After: %s", left)
				}
			}
		})
	}
}

// TestAFailedTurnTellsTheEndOfWhatTheAgentWroteToStderr: the error of an agent is at the end of its stderr, so a
// failed turn keeps that end.
func TestAFailedTurnTellsTheEndOfWhatTheAgentWroteToStderr(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	noise, _ := json.Marshal(strings.Repeat("loading a plugin\n", 300) + "Error: the config is broken")
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"stderr": `+string(noise)+`}`, `{"exit": 3}`))
	e := harness.New(t, harness.WithACP(f, harness.NewLLM(t, func(int, harness.ChatRequest) harness.ChatReply { return harness.TextReply("ok") })))
	got := e.WaitState(e.Run(e.AcpProject("paint", harness.ColorManifest), "work"), "done", "error")
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
		t.Run(tc.state, func(t *testing.T) {
			t.Parallel()
			f := harness.NewACP(t)
			llm := harness.NewLLM(t, func(n int, _ harness.ChatRequest) harness.ChatReply {
				if n <= 3 {
					return harness.TextReply("ok")
				}
				return harness.ChatReply{Status: tc.status, Header: http.Header{"Retry-After": {"120"}}, Body: `{"error": {"message": "no"}}`}
			})
			f.StreamText(4, harness.AcpStream(gooseInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, harness.AcpModel,
				`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Ran into this error: Rate limit exceeded."}}}`,
				`{"end": "end_turn"}`))
			e := harness.New(t, harness.WithACP(f, llm))
			if got := e.WaitState(e.Run(e.AcpProject("paint", harness.ColorManifest), "work"), "done", "error", "needs_login", "rate_limited"); got.State != tc.state {
				t.Fatalf("%d: want %s, got %+v", tc.status, tc.state, got)
			}
			if tc.state != "needs_login" {
				return
			}
			if a := e.WaitAsk(); !strings.Contains(a.Text, "key of its connection") || strings.Contains(a.Text, "terminal") {
				t.Fatalf("a refused key of a model is about the key of its connection, not a login in a terminal: %+v", a)
			}
		})
	}
}

// TestACPTurnThatHandedInItsResultIsDoneThoughALaterCallWasRefused: a call of the model refused after the result,
// such as the title of a session Goose asks for on the side, does not undo the turn.
func TestACPTurnThatHandedInItsResultIsDoneThoughALaterCallWasRefused(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	llm := harness.NewLLM(t, func(n int, _ harness.ChatRequest) harness.ChatReply {
		if n <= 3 {
			return harness.TextReply("ok")
		}
		return harness.ChatReply{Status: 429, Header: http.Header{"Retry-After": {"120"}}, Body: `{"error": {"message": "no"}}`}
	})
	f.StreamText(4, harness.AcpStream(gooseInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, harness.AcpModel, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, llm))
	if got := e.WaitState(e.Run(e.AcpProject("paint", harness.ColorManifest), "work"), "done", "error", "needs_login", "rate_limited"); got.State != "done" || string(got.Result) != `{"color":"blue"}` {
		t.Fatalf("the turn that handed in its result is done: %+v", got)
	}
}

// TestACPTurnIsNotUndoneByARefusedCallOnTheSide: a call of the model without tools, such as the title of a session
// Goose asks for, tells nothing of the turn: a turn whose answer is its text is done though that call was refused.
func TestACPTurnIsNotUndoneByARefusedCallOnTheSide(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	llm := harness.NewLLM(t, func(n int, _ harness.ChatRequest) harness.ChatReply {
		if n <= 4 {
			return harness.TextReply("ok")
		}
		return harness.ChatReply{Status: 429, Header: http.Header{"Retry-After": {"120"}}, Body: `{"error": {"message": "no"}}`}
	})
	f.StreamText(4, harness.AcpStream(gooseInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		harness.AcpModel, `{"model": {"model": "m", "messages": [{"role": "user", "content": "a title for this session"}]}}`,
		`{"update": {"sessionUpdate": "agent_message_chunk", "content": {"type": "text", "text": "Checked the page."}}}`, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, llm))
	open := strings.Replace(harness.NoNetworkManifest, `"no_network": true,`, "", 1)
	if got := e.WaitState(e.Run(e.AcpProject("open", open), "work"), "done", "error", "needs_login", "rate_limited"); got.State != "done" || !strings.Contains(string(got.Result), "Checked the page.") {
		t.Fatalf("a turn is done though a call on its side was refused: %+v", got)
	}
}

// TestACPAgentThatDiscoversTheGatewayOpensASession: Goose asks server/discover first; told the method is not there,
// it opens a session with initialize, which the gateway needs (told otherwise, it calls without one).
func TestACPAgentThatDiscoversTheGatewayOpensASession(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	f.StreamText(4, harness.AcpStream(gooseInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"discover": true}`, `{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, harness.AcpLLM(t)))
	got := e.WaitState(e.Run(e.AcpProject("paint", harness.ColorManifest), "work"), "done", "error")
	d := harness.JsonLines[map[string]any](f.Files(4)("discover.jsonl"))
	if got.State != "done" || len(d) != 1 || !strings.Contains(fmt.Sprint(d[0]["body"]), "-32601") {
		t.Fatalf("server/discover is a method the gateway does not have: %+v %v", got, d)
	}
}

// TestACPCountsTokensOfCompressedReplies: a model API that compresses its answers still has its tokens counted.
func TestACPCountsTokensOfCompressedReplies(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body := []byte(harness.TextReply("ok").Body)
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
	f := harness.NewACP(t)
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, harness.AcpModel,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	e := harness.New(t, harness.WithACP(f, &harness.FakeLLM{T: t, URL: srv.URL + "/v1"}))
	id := e.Run(e.AcpProject("paint", harness.ColorManifest), "work")
	e.WaitState(id, "done")
	var calls []map[string]any
	e.ModelCalls("", id, &calls)
	if len(calls) != 1 || calls[0]["gen_ai.usage.output_tokens"] != 10.0 {
		t.Fatalf("the tokens of a compressed reply are counted: %v", calls)
	}
}
