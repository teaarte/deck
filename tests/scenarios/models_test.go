package scenarios

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"deck2/internal/health"
	"deck2/internal/provider"
)

type modelsOut struct {
	Connections []struct {
		Name     string   `json:"name"`
		API      string   `json:"api"`
		URL      string   `json:"url"`
		Key      string   `json:"key"`
		Local    bool     `json:"local"`
		DailyUSD *float64 `json:"daily_usd"`
		Open     bool     `json:"open"`
		Models   int      `json:"models"`
	} `json:"connections"`
	Models []struct {
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
	Assignments []struct {
		Project string   `json:"project"`
		Command string   `json:"command"`
		Role    string   `json:"role"`
		Models  []string `json:"models"`
		Using   string   `json:"using"`
	} `json:"assignments"`
}

// TestTheProvidersOfABaseFromBeforeAreConnectionsModelsAndAssignments: the providers stored as on the owner's machine,
// a command's provider and speech to text become a connection per address and key, a model each and assignments.
func TestTheProvidersOfABaseFromBeforeAreConnectionsModelsAndAssignments(t *testing.T) {
	home := t.TempDir()
	e := newEnvWith(t, home, &Fake{})
	p := e.createProject("plain", plainManifest)
	e.olderBase(8)
	for key, value := range map[string]string{
		"provider:openrouter-qwen-free":             `{"url":"https://openrouter.ai/api/v1","model":"qwen/qwen3.8-27b:free","key_secret":"openrouter","agent":"opencode"}`,
		"provider:local-qwen":                       `{"url":"http://localhost:11434/v1","model":"qwen3.6:35b-a3b-nvfp4","no_cloud":true,"agent":"opencode"}`,
		"provider:nvidia-nemotron-free":             `{"url":"https://integrate.api.nvidia.com/v1","model":"nvidia/nemotron-3-ultra-550b-a55b","key_secret":"nvidia","agent":"opencode"}`,
		"provider:openrouter-thinkingmachines-free": `{"url":"https://openrouter.ai/api/v1","model":"thinkingmachines/inkling:free","key_secret":"openrouter","agent":"opencode"}`,
		"provider:openrouter-nemotron-free":         `{"url":"https://openrouter.ai/api/v1","model":"nvidia/nemotron-3-ultra-550b-a55b:free","key_secret":"openrouter","agent":"opencode"}`,
		"provider:google-gemini-3-8-flash":          `{"url":"https://generativelanguage.googleapis.com/v1beta/openai/","model":"gemini-3.8-flash","key_secret":"google","agent":"opencode","check_minutes":5}`,
		"setting:stt":                               `{"url":"https://api.openai.com/v1/audio/transcriptions","model":"whisper-1","key_secret":"openai","language":"ru"}`,
		"setting:language":                          `ru`,
	} {
		if _, err := e.App.Store.DB.Exec(`INSERT INTO setting (workspace_id, key, value) VALUES (1, ?, ?)`, key, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.App.Store.DB.Exec(`INSERT INTO command_provider (project_id, command, provider) VALUES (?, 'work', 'openrouter-thinkingmachines-free')`, p); err != nil {
		t.Fatal(err)
	}
	_ = e.App.Close()
	e = newEnvWith(t, home, &Fake{})

	var m modelsOut
	e.must("deck.models_list", nil, &m)
	conns := map[string]string{}
	counts := map[string]int{}
	for _, c := range m.Connections {
		conns[c.Name], counts[c.Name] = c.URL+" "+c.Key+" "+c.API, c.Models
		if c.Name == "local" && !c.Local {
			t.Fatalf("a provider on the owner's hardware is a local connection: %+v", c)
		}
	}
	want := map[string]string{
		"openrouter": "https://openrouter.ai/api/v1 secret openrouter openai",
		"nvidia":     "https://integrate.api.nvidia.com/v1 secret nvidia openai",
		"google":     "https://generativelanguage.googleapis.com/v1beta/openai/ secret google openai",
		"local":      "http://localhost:11434/v1 none openai",
		"stt":        "https://api.openai.com/v1/audio/transcriptions secret openai raw",
	}
	if len(conns) != len(want) || counts["openrouter"] != 3 {
		t.Fatalf("one connection per address and key: %v %v", conns, counts)
	}
	for name, w := range want {
		if conns[name] != w {
			t.Fatalf("connection %s: %q, want %q", name, conns[name], w)
		}
	}
	agents := 0
	for _, x := range m.Models {
		if x.Agent == "opencode" && x.Kind == "text" {
			agents++
		}
	}
	if len(m.Models) != 7 || agents != 6 {
		t.Fatalf("a model per provider, run by its agent, and the model of speech to text: %+v", m.Models)
	}
	got := map[string][]string{}
	for _, a := range m.Assignments {
		got[a.Role+" "+a.Project+"."+a.Command] = a.Models
	}
	delete(got, "agent master.on_error")
	if len(got) != 2 || len(got["agent plain.work"]) != 1 || got["agent plain.work"][0] != "openrouter-thinkingmachines-free" ||
		len(got["stt ."]) != 1 || got["stt ."][0] != "stt" {
		t.Fatalf("the provider of a command and speech to text are assignments: %v", got)
	}

	var list struct {
		Providers []struct{ Name, Key, Connection string }
		Chosen    string
	}
	e.must("deck.providers_list", map[string]any{"command": "plain.work"}, &list)
	keys := map[string]string{}
	for _, pv := range list.Providers {
		keys[pv.Name] = pv.Connection + " " + pv.Key
	}
	if list.Chosen != "openrouter-thinkingmachines-free" || len(keys) != len(e.App.Providers.Builtin)+6 || keys["openrouter-qwen-free"] != "openrouter secret openrouter" ||
		keys["local-qwen"] != "local none" {
		t.Fatalf("the providers are the models with an agent, the command keeps its own: %+v", list)
	}
	var left int
	if err := e.App.Store.DB.QueryRow(`SELECT count(*) FROM setting WHERE key LIKE 'provider:%' OR key = 'setting:stt'`).Scan(&left); err != nil || left != 0 {
		t.Fatalf("no provider is left among the settings: %d %v", left, err)
	}
	var lang string
	if err := e.App.Store.DB.QueryRow(`SELECT value FROM setting WHERE key = 'setting:language'`).Scan(&lang); err != nil || lang != "ru" {
		t.Fatalf("the other settings stay: %q %v", lang, err)
	}
	var opts string
	if err := e.App.Store.DB.QueryRow(`SELECT options FROM model WHERE name = 'stt'`).Scan(&opts); err != nil {
		t.Fatal(err)
	}
	var o map[string]string
	if json.Unmarshal([]byte(opts), &o) != nil || o["language"] != "ru" {
		t.Fatalf("speech to text keeps its language: %s", opts)
	}
}

// TestAnAssignmentTakesItsFallbackWhileTheFirstIsDown: a new turn of a project starts on the first provider of
// its assignment, and on the next one while the first is red, waiting for a login or paused by a limit.
func TestAnAssignmentTakesItsFallbackWhileTheFirstIsDown(t *testing.T) {
	main := &Fake{script: func(context.Context, *Call) provider.Result { return provider.Result{Text: "main"} }}
	other := &Fake{name: "other", script: func(context.Context, *Call) provider.Result { return provider.Result{Text: "other"} }}
	e := newEnvBuiltin(t, t.TempDir(), []provider.Provider{main, other})
	p := e.createProject("plain", plainManifest)
	e.must("deck.assignment_set", map[string]any{"role": "agent", "project": "plain", "models": []string{"other", "fake"}}, nil)
	if got := e.waitState(e.run(p, "work"), "done"); !strings.Contains(string(got.Result), "other") {
		t.Fatalf("the first of the assignment of the project runs its commands: %s", got.Result)
	}
	health.Write(t.Context(), e.App.Store, health.Of{}, "provider:other", "paused", "the usage limit")
	if got := e.waitState(e.run(p, "work"), "done"); !strings.Contains(string(got.Result), "main") {
		t.Fatalf("a fallback takes the turn while the first is paused: %s", got.Result)
	}
	var m modelsOut
	e.must("deck.models_list", nil, &m)
	for _, a := range m.Assignments {
		if a.Project == "plain" && a.Using != "fake" {
			t.Fatalf("the assignment says the fallback works: %+v", a)
		}
	}
	health.Write(t.Context(), e.App.Store, health.Of{}, "provider:other", "ok", "")
	if got := e.waitState(e.run(p, "work"), "done"); !strings.Contains(string(got.Result), "other") {
		t.Fatalf("back to the first once it is up: %s", got.Result)
	}
	e.must("deck.assignment_set", map[string]any{"role": "agent", "project": "plain", "models": []string{}}, nil)
	if got := e.waitState(e.run(p, "work"), "done"); !strings.Contains(string(got.Result), "main") {
		t.Fatalf("without the assignment the main provider runs: %s", got.Result)
	}
	e.refused("deck.assignment_set", map[string]any{"role": "agent", "project": "plain", "models": []string{"nobody"}}, 404, "no provider")
}
