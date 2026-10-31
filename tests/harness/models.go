package harness

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

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
