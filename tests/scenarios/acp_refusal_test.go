package scenarios

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestACPModelAPIThatRefusesEchoedReasoningTakesTheCallWithout: an API that refuses the reasoning Goose hands back
// in its history, as Groq does, gets the call again without it, and the rest of the turn without asking.
func TestACPModelAPIThatRefusesEchoedReasoningTakesTheCallWithout(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	refused := 0
	llm := newLLM(t, func(n int, req chatRequest) chatReply {
		for _, m := range req.Messages {
			if _, ok := m["reasoning_content"]; ok {
				refused++
				return chatReply{status: 400, body: `{"error": {"message": "'messages.1' : for 'role:assistant' the following must be satisfied[('messages.1' : property 'reasoning_content' is unsupported)]", "type": "invalid_request_error"}}`}
			}
		}
		return textReply("ok")
	})
	echoed := `{"model": {"model": "m", "messages": [{"role": "user", "content": "go"}, {"role": "assistant", "content": null, "reasoning_content": "I run it", "tool_calls": [{"id": "c1", "type": "function", "function": {"name": "shell", "arguments": "{}"}}]}, {"role": "tool", "tool_call_id": "c1", "content": "done"}], "tools": [{"type": "function", "function": {"name": "shell"}}]}}`
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, echoed, echoed,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	e := acpEnv(t, f, "goose", llm)
	if got := e.waitState(e.run(e.acpProject("paint", colorManifest), "work"), "done", "error"); got.State != "done" {
		t.Fatalf("the turn goes on: %+v", got)
	}
	for _, m := range f.call(4).model {
		if m["status"] != float64(200) {
			t.Fatalf("the agent gets the answer of the call without its reasoning: %+v", m)
		}
	}
	if refused != 1 {
		t.Fatalf("once refused, the calls of the turn go without reasoning: refused %d", refused)
	}
}

// TestACPSelfCheckTellsWhyNoResultCame: a first turn without result_submit is told apart: the API refused what the
// agent sent or its tools, the model made no tool call, or it forgot result_submit, which it is reminded of once.
func TestACPSelfCheckTellsWhyNoResultCame(t *testing.T) {
	t.Parallel()
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("testdata/acp", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	first, second, third := read("selfcheck_1.jsonl"), read("selfcheck_2.jsonl"), read("selfcheck_3.jsonl")
	submit := `{"mcp": {"tool": "result_submit", "arguments": {"result": {"word": "{{word}}", "answer": "{{word}}-answered"}}}}` + "\n"
	forgot := strings.Replace(first, submit, "", 1)
	words := acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, acpModel,
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
		{"words", "", []string{words}, "error", []string{"the model does not manage", "made no tool call", "The word is written."}},
		{"forgot twice", "", []string{forgot, strings.Replace(second, submit, "", 1)}, "error",
			[]string{"the model does not manage", "reminded of result_submit", "without calling result_submit"}},
		{"reminded", "", []string{forgot, second, second, third}, "ok", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			f := newACP(t)
			for i, s := range tc.streams {
				f.streamText(i+1, s)
			}
			llm := newLLM(t, func(int, chatRequest) chatReply {
				if tc.refuse != "" {
					status, words := 400, tc.refuse
					if w, ok := strings.CutPrefix(words, "404:"); ok {
						status, words = 404, w
					}
					b, _ := json.Marshal(map[string]any{"error": map[string]any{"message": words, "type": "invalid_request_error"}})
					return chatReply{status: status, body: string(b)}
				}
				return textReply("ok")
			})
			_, h := acpAdded(t, f, "goose", llm)
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
				rpc, _ := json.Marshal(f.call(2).rpc)
				if !strings.Contains(string(rpc), "without handing in the result") {
					t.Fatalf("the second turn reminds of result_submit: %s", rpc)
				}
			}
		})
	}
}
