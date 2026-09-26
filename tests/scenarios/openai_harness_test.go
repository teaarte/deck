package scenarios

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// chatRequest is what deck sent to the fake OpenAI-compatible API.
type chatRequest struct {
	Model    string           `json:"model"`
	Messages []map[string]any `json:"messages"`
	Tools    []struct {
		Function struct {
			Name string `json:"name"`
		} `json:"function"`
	} `json:"tools"`
	Auth string `json:"-"`
}

func (r chatRequest) tools() []string {
	var out []string
	for _, t := range r.Tools {
		out = append(out, t.Function.Name)
	}
	return out
}

// last is the text of the last message of a role.
func (r chatRequest) last(role string) string {
	for i := len(r.Messages) - 1; i >= 0; i-- {
		if r.Messages[i]["role"] == role {
			s, _ := r.Messages[i]["content"].(string)
			return s
		}
	}
	return ""
}

type chatReply struct {
	status int
	header http.Header
	body   string
}

// fakeLLM is an OpenAI-compatible chat API: each request gets the reply its script gives for it.
type fakeLLM struct {
	t      *testing.T
	URL    string
	mu     sync.Mutex
	reqs   []chatRequest
	script func(n int, req chatRequest) chatReply
}

func newLLM(t *testing.T, script func(n int, req chatRequest) chatReply) *fakeLLM {
	f := &fakeLLM{t: t, script: script}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var req chatRequest
		_ = json.Unmarshal(b, &req)
		req.Auth = r.Header.Get("Authorization")
		f.mu.Lock()
		f.reqs = append(f.reqs, req)
		n := len(f.reqs)
		f.mu.Unlock()
		rep := f.script(n, req)
		if bad := silentAssistant(req); bad >= 0 {
			rep = chatReply{status: 400, body: `{"error":{"message":"invalid message content type: <nil>","type":"invalid_request_error"}}`}
		}
		for k, vs := range rep.header {
			w.Header()[k] = vs
		}
		w.Header().Set("Content-Type", "application/json")
		if rep.status == 0 {
			rep.status = 200
		}
		w.WriteHeader(rep.status)
		_, _ = io.WriteString(w, rep.body)
	}))
	t.Cleanup(srv.Close)
	f.URL = srv.URL + "/v1"
	return f
}

// silentAssistant is the index of an assistant message with neither content nor tool calls, which Ollama
// refuses, or -1.
func silentAssistant(req chatRequest) int {
	for i, m := range req.Messages {
		if m["role"] == "assistant" && m["content"] == nil && m["tool_calls"] == nil {
			return i
		}
	}
	return -1
}

func (f *fakeLLM) requests() []chatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]chatRequest{}, f.reqs...)
}

type fnCall struct {
	name string
	args any
}

// toolReply asks deck to make tool calls.
func toolReply(calls ...fnCall) chatReply {
	var tcs []map[string]any
	for i, c := range calls {
		a, _ := json.Marshal(c.args)
		tcs = append(tcs, map[string]any{"id": fmt.Sprintf("call_%d", i), "type": "function",
			"function": map[string]any{"name": c.name, "arguments": string(a)}})
	}
	return chatReply{body: completion(map[string]any{"role": "assistant", "content": "", "tool_calls": tcs})}
}

// textReply ends the turn with text.
func textReply(text string) chatReply {
	return chatReply{body: completion(map[string]any{"role": "assistant", "content": text})}
}

func completion(msg map[string]any) string {
	b, _ := json.Marshal(map[string]any{"id": "chatcmpl-1", "object": "chat.completion", "created": 1, "model": "fake-model",
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}},
		"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 10, "total_tokens": 110,
			"prompt_tokens_details": map[string]any{"cached_tokens": 40}}})
	return string(b)
}

// recorded replays a recorded reply of testdata/openai, which must say where and when it was recorded.
func recorded(t *testing.T, fixture string) chatReply {
	t.Helper()
	var meta map[string]struct {
		Server, Model, Date, Kind string
		Status                    int
		RetryAfter                string `json:"retry_after"`
	}
	b, _ := os.ReadFile("testdata/openai/fixtures.json")
	if err := json.Unmarshal(b, &meta); err != nil {
		t.Fatal(err)
	}
	m, ok := meta[fixture]
	if !ok || m.Server == "" || m.Date == "" || (m.Kind != "real" && m.Kind != "synthetic") {
		t.Fatalf("fixture %s needs its server, date and kind in fixtures.json", fixture)
	}
	body, err := os.ReadFile(filepath.Join("testdata/openai", fixture))
	if err != nil {
		t.Fatal(err)
	}
	rep := chatReply{status: m.Status, body: string(body), header: http.Header{}}
	if m.RetryAfter != "" {
		rep.header.Set("Retry-After", m.RetryAfter)
	}
	return rep
}

// addLLM adds the fake API as an OpenAI-compatible provider of the workspace, with its key as a secret.
func (e *Env) addLLM(name string, f *fakeLLM, noCloud bool) {
	e.T.Helper()
	e.must("deck.secret_set", map[string]any{"name": name + "-key", "value": "sk-" + name}, nil)
	e.must("deck.provider_add", map[string]any{"name": name, "url": f.URL, "model": "asked-model", "key_secret": name + "-key",
		"no_cloud": noCloud}, nil)
}
