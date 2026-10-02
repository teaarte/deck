package scenarios

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// chatRequest is what deck's proxy sent to the fake OpenAI-compatible API of the model.
type chatRequest struct {
	Model    string           `json:"model"`
	Messages []map[string]any `json:"messages"`
	Auth     string           `json:"-"`
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

func (f *fakeLLM) requests() []chatRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]chatRequest{}, f.reqs...)
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
