package harness

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

// ModelCalls reads the journal of model calls, newest first, of a project and a process when they are given.
func (e *Env) ModelCalls(project string, process int64, out any) {
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

// useModels assigns the models an agent command, project.command, runs on, as the owner does on the Models tab; none
// goes back to the default.
func (e *Env) UseModels(command string, models ...string) {
	e.T.Helper()
	project, cmd, _ := strings.Cut(command, ".")
	e.Must("deck.assignment_set", map[string]any{"role": "agent", "project": project, "command": cmd, "models": append([]string{}, models...)}, nil)
}

// AgentModel adds a model an ACP agent runs on a connection of the same name, as the owner does in Settings, from
// name, url, model, agent, key_secret, no_cloud and check_minutes; it gives the outcome of the first call that failed.
func (e *Env) AgentModel(in map[string]any) (int, []byte) {
	e.T.Helper()
	conn := map[string]any{"name": in["name"], "api": "openai", "url": in["url"]}
	if v, ok := in["key_secret"]; ok {
		conn["key_secret"] = v
	}
	if v, ok := in["no_cloud"]; ok {
		conn["local"] = v
	}
	if code, out := e.Call("deck.connection_set", conn); code != 200 {
		return code, out
	}
	model := map[string]any{"connection": in["name"], "name": in["name"], "model": in["model"], "agent": in["agent"]}
	if v, ok := in["check_minutes"]; ok {
		model["check_minutes"] = v
	}
	return e.Call("deck.model_set", model)
}
