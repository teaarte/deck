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

// Assigned are the models of a role of a project as the owner reads them.
func (e *Env) Assigned(project, role string) []string {
	e.T.Helper()
	var m ModelsOut
	e.Must("deck.models_list", map[string]any{"project": project}, &m)
	for _, a := range m.Assignments {
		if a.Project == project && a.Command == "" && a.Role == role {
			return a.Models
		}
	}
	return nil
}

// MustAgentModel is agentModel that must succeed.
func (e *Env) MustAgentModel(in map[string]any) {
	e.T.Helper()
	if code, out := e.AgentModel(in); code != 200 {
		e.T.Fatalf("add model %v: %d %s", in["name"], code, out)
	}
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

func ChatText(content string) string {
	b, _ := json.Marshal(map[string]any{"id": "c-1", "model": "fact-model", "choices": []any{map[string]any{"message": map[string]any{"role": "assistant", "content": content}}},
		"usage": map[string]any{"prompt_tokens": 200, "completion_tokens": 20}})
	return string(b)
}

type ModelsOut struct {
	Agents []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Status  string `json:"status"`
		Error   string `json:"error"`
	} `json:"agents"`
	Connections []ModelsOutConnection `json:"connections"`
	Models      []struct {
		Builtin    bool     `json:"builtin"`
		Name       string   `json:"name"`
		Connection string   `json:"connection"`
		Model      string   `json:"model"`
		Kind       string   `json:"kind"`
		Agent      string   `json:"agent"`
		InputUSD   *float64 `json:"input_usd"`
		OutputUSD  *float64 `json:"output_usd"`
		CallUSD    *float64 `json:"call_usd"`
		Free       bool     `json:"free"`
	} `json:"models"`
	Assignments []AssignmentOut `json:"assignments"`
}

type AssignmentOut struct {
	Project string   `json:"project"`
	Command string   `json:"command"`
	Role    string   `json:"role"`
	Models  []string `json:"models"`
	Using   string   `json:"using"`
	Why     string   `json:"why"`
}

type ModelsOutConnection struct {
	Name   string `json:"name"`
	API    string `json:"api"`
	URL    string `json:"url"`
	Key    string `json:"key"`
	Local  bool   `json:"local"`
	Models int    `json:"models"`
}

// FakeModels is an API of models that answers each path with what its script gives and keeps what it got.
type FakeModels struct {
	URL  string
	mu   sync.Mutex
	got  []ModelCall
	path map[string]func(n int, body map[string]any) (int, string, string)
}

type ModelCall struct {
	Path, Auth, Model string
	// Header is the X-Deck-Model the API got, which deck keeps to itself.
	Header string
	Body   map[string]any
}

func NewModels(t *testing.T, path map[string]func(n int, body map[string]any) (int, string, string)) *FakeModels {
	f := &FakeModels{path: path}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		model, _ := body["model"].(string)
		f.mu.Lock()
		f.got = append(f.got, ModelCall{Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Model: model, Header: r.Header.Get("X-Deck-Model"), Body: body})
		n := len(f.got)
		f.mu.Unlock()
		answer := f.path[r.URL.Path]
		if answer == nil {
			http.NotFound(w, r)
			return
		}
		status, ct, text := answer(n, body)
		w.Header().Set("Content-Type", ct)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, text)
	}))
	t.Cleanup(srv.Close)
	f.URL = srv.URL
	return f
}

func (f *FakeModels) Calls() []ModelCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ModelCall{}, f.got...)
}

// PictureAnswer is how OpenRouter answers chat completions with an image: the picture in base64, the usage with its cost.
func PictureAnswer(cost float64) string {
	b, _ := json.Marshal(map[string]any{"id": "gen-1", "model": "google/gemini-3.1-flash-lite-image", "choices": []any{map[string]any{
		"message": map[string]any{"role": "assistant", "content": "", "images": []any{map[string]any{"type": "image_url",
			"image_url": map[string]any{"url": "data:image/png;base64,iVBORw0KGgo="}}}}}},
		"usage": map[string]any{"prompt_tokens": 58, "completion_tokens": 1290, "total_tokens": 1348, "cost": cost}})
	return string(b)
}

type SpendRow struct {
	Provider string   `json:"gen_ai.provider.name"`
	Model    string   `json:"gen_ai.request.model"`
	Process  int64    `json:"process"`
	Command  string   `json:"command"`
	Cost     *float64 `json:"cost_usd"`
}

type AgentsV struct {
	Agents []struct{ Name, Status string }
	Models []struct {
		Name, Status string
		Builtin      bool
	}
}

type SignInRow struct {
	Provider, URL, Code, Error string
	Paste                      bool
}
