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

// modelCalls reads the journal of model calls, newest first, of a project and a process when they are given.
func (e *Env) modelCalls(project string, process int64, out any) {
	e.T.Helper()
	rows, err := e.App.Store.DB.Query(`SELECT c.id, COALESCE(c.process_id, 0), c.command, c.gen_ai_provider_name, c.gen_ai_request_model,
		c.gen_ai_response_model, c.gen_ai_usage_input_tokens, c.gen_ai_usage_output_tokens, c.gen_ai_usage_cache_read_input_tokens,
		c.gen_ai_usage_cache_creation_input_tokens, c.duration_ms, c.cost_usd, c.created_at
		FROM llm_call c LEFT JOIN project p ON p.id = c.project_id
		WHERE (? = '' OR p.name = ?) AND (? = 0 OR c.process_id = ?) ORDER BY c.id DESC`, project, project, process, process)
	if err != nil {
		e.T.Fatal(err)
	}
	defer rows.Close()
	var calls []map[string]any
	for rows.Next() {
		var id, proc, in, outT, read, created, ms int64
		var command, prov, req, resp, at string
		var cost *float64
		if err := rows.Scan(&id, &proc, &command, &prov, &req, &resp, &in, &outT, &read, &created, &ms, &cost, &at); err != nil {
			e.T.Fatal(err)
		}
		c := map[string]any{"id": id, "process": proc, "command": command, "gen_ai.provider.name": prov, "gen_ai.request.model": req,
			"gen_ai.response.model": resp, "gen_ai.usage.input_tokens": in, "gen_ai.usage.output_tokens": outT,
			"gen_ai.usage.cache_read.input_tokens": read, "gen_ai.usage.cache_creation.input_tokens": created,
			"duration_ms": ms, "created_at": at}
		if cost != nil {
			c["cost_usd"] = *cost
		}
		calls = append(calls, c)
	}
	raw, _ := json.Marshal(calls)
	if err := json.Unmarshal(raw, out); err != nil {
		e.T.Fatal(err)
	}
}
