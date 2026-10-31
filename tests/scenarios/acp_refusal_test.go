package scenarios

import (
	"deck2/tests/harness"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	p := e.AcpProject("paint", colorManifest)
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
			got := e.WaitState(e.Run(e.AcpProject("paint", colorManifest), "work"), "done", "error")
			if got.State != tc.state || !strings.Contains(got.Error, tc.want) {
				t.Fatalf("want %s with %q: %+v", tc.state, tc.want, got)
			}
		})
	}
}
