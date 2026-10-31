package harness

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// ChatRequest is what deck's proxy sent to the fake OpenAI-compatible API of the model.
type ChatRequest struct {
	Model    string           `json:"model"`
	Messages []map[string]any `json:"messages"`
	Auth     string           `json:"-"`
}

type ChatReply struct {
	Status int
	Header http.Header
	Body   string
}

// FakeLLM is an OpenAI-compatible chat API: each request gets the reply its script gives for it.
type FakeLLM struct {
	T      *testing.T
	URL    string
	mu     sync.Mutex
	reqs   []ChatRequest
	script func(n int, req ChatRequest) ChatReply
}

func NewLLM(t *testing.T, script func(n int, req ChatRequest) ChatReply) *FakeLLM {
	f := &FakeLLM{T: t, script: script}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		b, _ := io.ReadAll(r.Body)
		var req ChatRequest
		_ = json.Unmarshal(b, &req)
		req.Auth = r.Header.Get("Authorization")
		f.mu.Lock()
		f.reqs = append(f.reqs, req)
		n := len(f.reqs)
		f.mu.Unlock()
		rep := f.script(n, req)
		for k, vs := range rep.Header {
			w.Header()[k] = vs
		}
		w.Header().Set("Content-Type", "application/json")
		if rep.Status == 0 {
			rep.Status = 200
		}
		w.WriteHeader(rep.Status)
		_, _ = io.WriteString(w, rep.Body)
	}))
	t.Cleanup(srv.Close)
	f.URL = srv.URL + "/v1"
	return f
}

func (f *FakeLLM) Requests() []ChatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ChatRequest{}, f.reqs...)
}

// TextReply ends the turn with text.
func TextReply(text string) ChatReply {
	return ChatReply{Body: completion(map[string]any{"role": "assistant", "content": text})}
}

func completion(msg map[string]any) string {
	b, _ := json.Marshal(map[string]any{"id": "chatcmpl-1", "object": "chat.completion", "created": 1, "model": "fake-model",
		"choices": []any{map[string]any{"index": 0, "message": msg, "finish_reason": "stop"}},
		"usage": map[string]any{"prompt_tokens": 100, "completion_tokens": 10, "total_tokens": 110,
			"prompt_tokens_details": map[string]any{"cached_tokens": 40}}})
	return string(b)
}
