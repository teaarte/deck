package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"deck2/internal/health"
	"deck2/internal/provider"
)

type modelsOut struct {
	Agents []struct {
		Name    string `json:"name"`
		Version string `json:"version"`
		Status  string `json:"status"`
		Error   string `json:"error"`
	} `json:"agents"`
	Connections []struct {
		Name   string `json:"name"`
		API    string `json:"api"`
		URL    string `json:"url"`
		Key    string `json:"key"`
		Local  bool   `json:"local"`
		Models int    `json:"models"`
	} `json:"connections"`
	Models []struct {
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
	t.Parallel()
	home := t.TempDir()
	e := newEnvWith(t, home, &Fake{})
	p := e.createProject("plain", plainManifest)
	e.olderBase(8)
	for key, value := range map[string]string{
		"provider:openrouter-qwen-free":             `{"url":"https://openrouter.ai/api/v1","model":"qwen/qwen3.8-27b:free","key_secret":"openrouter","agent":"dsh"}`,
		"provider:local-qwen":                       `{"url":"http://localhost:11434/v1","model":"qwen3.6:35b-a3b-nvfp4","no_cloud":true,"agent":"dsh"}`,
		"provider:nvidia-nemotron-free":             `{"url":"https://integrate.api.nvidia.com/v1","model":"nvidia/nemotron-3-ultra-550b-a55b","key_secret":"nvidia","agent":"dsh"}`,
		"provider:openrouter-thinkingmachines-free": `{"url":"https://openrouter.ai/api/v1","model":"thinkingmachines/inkling:free","key_secret":"openrouter","agent":"dsh"}`,
		"provider:openrouter-nemotron-free":         `{"url":"https://openrouter.ai/api/v1","model":"nvidia/nemotron-3-ultra-550b-a55b:free","key_secret":"openrouter","agent":"dsh"}`,
		"provider:google-gemini-3-8-flash":          `{"url":"https://generativelanguage.googleapis.com/v1beta/openai/","model":"gemini-3.8-flash","key_secret":"google","agent":"dsh","check_minutes":5}`,
		"provider:old-env":                          `{"url":"https://api.example.com/v1","model":"m","key_secret":"OPENAI_API_KEY","agent":"dsh"}`,
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
		"openrouter":     "https://openrouter.ai/api/v1 secret openrouter openai",
		"nvidia":         "https://integrate.api.nvidia.com/v1 secret nvidia openai",
		"google":         "https://generativelanguage.googleapis.com/v1beta/openai/ secret google openai",
		"local":          "http://localhost:11434/v1 none openai",
		"stt":            "https://api.openai.com/v1/audio/transcriptions secret openai raw",
		"openai-api-key": "https://api.example.com/v1 secret OPENAI_API_KEY openai",
	}
	if len(conns) != len(want) || counts["openrouter"] != 3 {
		t.Fatalf("one connection per address and key: %v %v", conns, counts)
	}
	for name, w := range want {
		if conns[name] != w {
			t.Fatalf("connection %s: %q, want %q", name, conns[name], w)
		}
	}
	agents, builtin := 0, 0
	for _, x := range m.Models {
		if x.Agent == "dsh" && x.Kind == "text" {
			agents++
		}
		if x.Builtin {
			builtin++
		}
	}
	if len(m.Models)-builtin != 8 || agents != 7 {
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

	var cmds []struct{ Name, Provider string }
	e.must("deck.commands_list", map[string]any{"project": "plain"}, &cmds)
	conn := map[string]string{}
	for _, x := range m.Models {
		conn[x.Name] = x.Connection
	}
	if i := slices.IndexFunc(cmds, func(c struct{ Name, Provider string }) bool { return c.Name == "plain.work" }); i < 0 ||
		cmds[i].Provider != "openrouter-thinkingmachines-free" || conn["openrouter-qwen-free"] != "openrouter" || conn["local-qwen"] != "local" {
		t.Fatalf("the providers are the models with an agent, the command keeps its own: %+v %v", cmds, conn)
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
	t.Parallel()
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

// fakeModels is an API of models that answers each path with what its script gives and keeps what it got.
type fakeModels struct {
	URL  string
	mu   sync.Mutex
	got  []modelCall
	path map[string]func(n int, body map[string]any) (int, string, string)
}

type modelCall struct {
	Path, Auth, Model string
	// Header is the X-Deck-Model the API got, which deck keeps to itself.
	Header string
	Body   map[string]any
}

func newModels(t *testing.T, path map[string]func(n int, body map[string]any) (int, string, string)) *fakeModels {
	f := &fakeModels{path: path}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		model, _ := body["model"].(string)
		f.mu.Lock()
		f.got = append(f.got, modelCall{Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Model: model, Header: r.Header.Get("X-Deck-Model"), Body: body})
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

func (f *fakeModels) calls() []modelCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]modelCall{}, f.got...)
}

// pictureAnswer is how OpenRouter answers chat completions with an image: the picture in base64, the usage with its cost.
func pictureAnswer(cost float64) string {
	b, _ := json.Marshal(map[string]any{"id": "gen-1", "model": "google/gemini-3.1-flash-lite-image", "choices": []any{map[string]any{
		"message": map[string]any{"role": "assistant", "content": "", "images": []any{map[string]any{"type": "image_url",
			"image_url": map[string]any{"url": "data:image/png;base64,iVBORw0KGgo="}}}}}},
		"usage": map[string]any{"prompt_tokens": 58, "completion_tokens": 1290, "total_tokens": 1348, "cost": cost}})
	return string(b)
}

// modelScript is a script that posts a body to a path under DECK_MODELS, as a module calls a model, and returns
// what came back.
const modelScript = `import json, os, sys, urllib.request, urllib.error
inp = json.load(sys.stdin)
method = inp.get("method") or "POST"
body = None if method == "GET" else inp["form"].encode() if inp.get("form") else json.dumps(json.loads(inp["other"])).encode()
req = urllib.request.Request(os.environ["DECK_MODELS"] + "/" + inp["key"], data=body, method=method,
    headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": inp.get("type") or "application/json"})
if inp.get("use"):
    req.add_header("X-Deck-Model", inp["use"])
try:
    with urllib.request.urlopen(req, timeout=60) as r:
        status, text = r.status, r.read().decode()
except urllib.error.HTTPError as e:
    status, text = e.code, e.read().decode()
keys = [k for k, v in os.environ.items() if "sk-or" in v]
print(json.dumps({"status": status, "answer": text[:4000], "keys": keys}))
`

// picture is the body the shorts module sends for a picture, as its script builds it.
const picture = `{"model": "google/gemini-3.1-flash-lite-image", "messages": [{"role": "user", "content": "A medieval manuscript painter"}],
  "modalities": ["image", "text"], "image_config": {"aspect_ratio": "9:16"}}`

type spendRow struct {
	Provider string   `json:"gen_ai.provider.name"`
	Model    string   `json:"gen_ai.request.model"`
	Process  int64    `json:"process"`
	Command  string   `json:"command"`
	Cost     *float64 `json:"cost_usd"`
}

// TestAScriptCallsAModelThroughDeckAndNoLimitOfABaseFromBeforeHoldsIt: a script calls a model through DECK_MODELS
// without its key, deck keeps what OpenRouter says it cost; the daily limit of an older base goes.
func TestAScriptCallsAModelThroughDeckAndNoLimitOfABaseFromBeforeHoldsIt(t *testing.T) {
	t.Parallel()
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/api/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", pictureAnswer(0.033631)
		},
	})
	home := t.TempDir()
	e := newEnvWith(t, home, &Fake{})
	e.must("deck.secret_set", map[string]any{"name": "openrouter", "value": "sk-or-real"}, nil)
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/api/v1", "key_secret": "openrouter"}, nil)
	e.olderBase(19)
	if _, err := e.App.Store.DB.Exec(`UPDATE connection SET daily_usd = 0.05 WHERE name = 'openrouter'`); err != nil {
		t.Fatal(err)
	}
	_ = e.App.Close()
	e = newEnvWith(t, home, &Fake{})
	var n int
	if err := e.App.Store.DB.QueryRow(`SELECT count(*) FROM pragma_table_info('connection') WHERE name = 'daily_usd'`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("the daily limit of a connection goes with the migration: %d %v", n, err)
	}
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "google/gemini-3.1-flash-lite-image", "kind": "image", "name": "painter", "call_usd": 0.04}, nil)
	e.createProject("shorts", scriptManifest("media", "", `, "models": ["openrouter"]`))
	e.saveScript("shorts", "scripts/main.py", modelScript)
	for i := range 3 {
		got := e.waitScript(e.start("shorts", "media", map[string]any{"key": "openrouter/chat/completions", "other": picture}), "done", "error")
		var out map[string]any
		if got.State != "done" || json.Unmarshal(got.Result, &out) != nil {
			t.Fatalf("the script ran: %+v", got)
		}
		if out["status"] != 200.0 || !strings.Contains(out["answer"].(string), "data:image/png;base64") || len(out["keys"].([]any)) != 0 {
			t.Fatalf("call %d, past the old limit too, reaches the model with the answer as it came and no key in the script: %v", i, out)
		}
	}
	calls := api.calls()
	if len(calls) != 3 || calls[0].Auth != "Bearer sk-or-real" || calls[0].Model != "google/gemini-3.1-flash-lite-image" ||
		calls[0].Body["image_config"].(map[string]any)["aspect_ratio"] != "9:16" {
		t.Fatalf("deck put in the key and passed the body on: %+v", calls)
	}
	var spend []spendRow
	e.modelCalls("shorts", 0, &spend)
	if len(spend) != 3 || spend[0].Provider != "painter" || spend[0].Model != "google/gemini-3.1-flash-lite-image" || spend[0].Command != "media" ||
		spend[0].Process == 0 || spend[0].Cost == nil || *spend[0].Cost != 0.033631 {
		t.Fatalf("each call is in the journal of model calls with what OpenRouter said it cost: %+v", spend)
	}
	e.refused("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/api/v1", "daily_usd": 1}, 422, "daily_usd")
}

// TestAnAgentTurnReachesOnlyTheModelOfItsTurn: a turn of an ACP agent calls its model through deck's proxy, which
// sends every call to the model the turn started on, whatever model the agent names, priced by that model.
func TestAnAgentTurnReachesOnlyTheModelOfItsTurn(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	llm := newLLM(t, func(int, chatRequest) chatReply { return textReply("ok") })
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"model": {"model": "pricier-model", "messages": [{"role": "user", "content": "hi"}]}}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	f.streamText(5, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-2"}}`, `{"reply": "session/prompt"}`, acpModel,
		`{"fail": {"code": -32603, "message": "Internal error: turn failed"}}`))
	e := acpEnv(t, f, "dsh", llm)
	e.must("deck.model_set", map[string]any{"connection": "acp", "model": "m", "name": "acp", "input_usd": 1000, "output_usd": 2000}, nil)
	p := e.acpProject("paint", colorManifest)
	if got := e.waitState(e.run(p, "work"), "done", "error"); got.State != "done" {
		t.Fatalf("the first turn runs: %+v", got)
	}
	reqs := llm.requests()
	if last := reqs[len(reqs)-1]; last.Model != "m" {
		t.Fatalf("an agent reaches only the model of its turn: %+v", last)
	}
	var spend []spendRow
	e.modelCalls("paint", 0, &spend)
	if len(spend) != 1 || spend[0].Cost == nil || math.Abs(*spend[0].Cost-0.12) > 1e-9 {
		t.Fatalf("a turn is priced by its model, 100 tokens in and 10 out: %+v", spend)
	}
}

// TestAScriptCallsARoleAndAFallbackTakesARefusal: @tts goes to the first model assigned, past a 429 to the next, priced
// per call; a role reaches only the paths of model calls.
func TestAScriptCallsARoleAndAFallbackTakesARefusal(t *testing.T) {
	t.Parallel()
	busy := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/audio/speech": func(int, map[string]any) (int, string, string) {
			return 429, "application/json", `{"error": "rate limited"}`
		},
	})
	spare := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/audio/speech": func(int, map[string]any) (int, string, string) { return 200, "audio/mpeg", "ID3-voice" },
	})
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "busy", "api": "openai", "url": busy.URL + "/v1"}, nil)
	e.must("deck.connection_set", map[string]any{"name": "spare", "api": "openai", "url": spare.URL + "/v1"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "busy", "model": "voice-1", "kind": "tts", "call_usd": 0.01}, nil)
	e.must("deck.model_set", map[string]any{"connection": "spare", "model": "voice-2", "kind": "tts", "call_usd": 0.02}, nil)
	e.must("deck.assignment_set", map[string]any{"role": "tts", "models": []string{"busy/voice-1", "spare/voice-2"}}, nil)
	e.createProject("shorts", scriptManifest("voice", "", `, "models": ["@tts", "spare"]`))
	e.saveScript("shorts", "scripts/main.py", modelScript)
	speech := `{"model": "@tts", "input": "Knights against snails", "voice": "alloy", "response_format": "mp3"}`
	got := e.waitScript(e.start("shorts", "voice", map[string]any{"key": "@/audio/speech", "other": speech}), "done", "error")
	var out map[string]any
	if json.Unmarshal(got.Result, &out) != nil || out["status"] != 200.0 || out["answer"] != "ID3-voice" {
		t.Fatalf("the fallback answers after the refusal of the first: %+v", got)
	}
	if b, s := busy.calls(), spare.calls(); len(b) != 1 || b[0].Model != "voice-1" || len(s) != 1 || s[0].Model != "voice-2" || s[0].Body["voice"] != "alloy" {
		t.Fatalf("each model got the body with its own name in place of the role: %+v %+v", b, s)
	}
	var spend []spendRow
	e.modelCalls("shorts", 0, &spend)
	if len(spend) != 1 || spend[0].Provider != "spare/voice-2" || spend[0].Cost == nil || *spend[0].Cost != 0.02 {
		t.Fatalf("the call is counted on the model that made it, by its price per call: %+v", spend)
	}

	got = e.waitScript(e.start("shorts", "voice", map[string]any{"key": "@/files", "other": `{"model": "@tts"}`}), "done", "error")
	if json.Unmarshal(got.Result, &out) != nil || out["status"] != 403.0 || !strings.Contains(out["answer"].(string), "not a call of a model") {
		t.Fatalf("a role reaches only the paths of model calls, not the rest of the connection behind it: %+v", got)
	}
	got = e.waitScript(e.start("shorts", "voice", map[string]any{"key": "spare/images/generations", "other": `{"model": "@image"}`}), "done", "error")
	if json.Unmarshal(got.Result, &out) != nil || out["status"] != 422.0 || !strings.Contains(out["answer"].(string), "no model is assigned to image") {
		t.Fatalf("a role nothing is assigned to is refused with what to do: %+v", got)
	}
}

// TestAModelTakesItsPriceAndKindFromTheCatalogOfItsConnection: a model added without a price takes the one OpenRouter
// lists and its kind; free is kept when left out, false clears it, a zero of a model making more than text is no proof.
func TestAModelTakesItsPriceAndKindFromTheCatalogOfItsConnection(t *testing.T) {
	t.Parallel()
	catalog := `{"data": [{"id": "google/gemini-3.1-flash-lite-image", "pricing": {"prompt": "0.0000003", "completion": "0.0000025", "request": "0", "image": "0.00003"},
	  "architecture": {"input_modalities": ["text", "image"], "output_modalities": ["image", "text"]}},
	  {"id": "qwen/qwen3.8-27b:free", "pricing": {"prompt": "0", "completion": "0"}, "architecture": {"output_modalities": ["text"]}},
	  {"id": "google/lyria-3-pro-preview", "pricing": {"prompt": "0", "completion": "0"}, "architecture": {"output_modalities": ["text", "audio"]}}]}`
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/api/v1/models": func(int, map[string]any) (int, string, string) { return 200, "application/json", catalog },
	})
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/api/v1", "key": "sk-or-new"}, nil)
	var m struct {
		Name      string
		Kind      string
		InputUSD  float64 `json:"input_usd"`
		OutputUSD float64 `json:"output_usd"`
		Free      bool
	}
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "google/gemini-3.1-flash-lite-image"}, &m)
	if m.Name != "openrouter/google/gemini-3.1-flash-lite-image" || m.Kind != "image" || math.Abs(m.InputUSD-0.3) > 1e-9 || math.Abs(m.OutputUSD-2.5) > 1e-9 || m.Free {
		t.Fatalf("the price per million tokens and the kind come from the catalog: %+v", m)
	}
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen/qwen3.8-27b:free", "name": "qwen"}, &m)
	if m.Kind != "text" || !m.Free {
		t.Fatalf("a model the catalog lists at zero is free: %+v", m)
	}
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "google/lyria-3-pro-preview", "name": "lyria"}, &m)
	if m.Free {
		t.Fatalf("a zero in tokens is no price of a song: a model making more than text is free only when the catalog says so: %+v", m)
	}
	if c := api.calls(); len(c) != 3 || c[0].Auth != "Bearer sk-or-new" {
		t.Fatalf("the key given with the connection is kept in a secret and used: %+v", c)
	}
	var secrets []struct{ Name string }
	e.must("deck.secrets_list", nil, &secrets)
	if len(secrets) != 1 || secrets[0].Name != "openrouter" {
		t.Fatalf("the key is a secret named after the connection: %+v", secrets)
	}
	m.InputUSD = 0
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen/qwen3.8-27b:free", "name": "qwen", "kind": "text"}, &m)
	if !m.Free || math.Abs(m.InputUSD) > 1e-9 {
		t.Fatalf("a model changed without free or a price keeps them: %+v", m)
	}
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen/qwen3.8-27b:free", "name": "qwen", "free": false}, &m)
	if m.Free {
		t.Fatalf("free false makes a model paid again: %+v", m)
	}
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen/qwen3.8-27b:free", "name": "qwen", "free": true}, &m)
}

// TestAModuleCallsOnlyTheModelsTheOwnerGranted: a project calls models only through what its manifest names; a new
// name waits for the owner's button, and a draft's call waits for a yes unless its model is free on this machine.
func TestAModuleCallsOnlyTheModelsTheOwnerGranted(t *testing.T) {
	t.Parallel()
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) { return 200, "application/json", chatText("hi") },
	})
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/v1"}, nil)
	e.must("deck.connection_set", map[string]any{"name": "ollama", "api": "openai", "url": api.URL + "/v1", "local": true}, nil)
	e.must("deck.model_set", map[string]any{"connection": "ollama", "model": "qwen", "kind": "text", "free": true}, nil)
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "kind": "text", "call_usd": 0.01}, nil)
	call := func(project, path string) map[string]any {
		got := e.waitScript(e.start(project, "media", map[string]any{"key": path, "other": `{"model": "qwen", "messages": []}`}), "done", "error")
		var out map[string]any
		if json.Unmarshal(got.Result, &out) != nil {
			t.Fatalf("the script ran: %+v", got)
		}
		return out
	}
	e.createProject("plain", scriptManifest("media", "", ""))
	e.saveScript("plain", "scripts/main.py", modelScript)
	if out := call("plain", "openrouter/chat/completions"); out["status"] != 403.0 || !strings.Contains(out["answer"].(string), `add "openrouter" to models`) {
		t.Fatalf("a call through a connection the manifest does not name is refused with what to do: %v", out)
	}
	_, tok := e.token("terminal", "deck.*")
	var w struct {
		Waiting int64
		Widens  []string
	}
	code, raw := e.callWith(tok, "deck.manifest_save", map[string]any{"project": "plain", "manifest": scriptManifest("media", "", `, "models": ["openrouter"]`)})
	if code != 200 || json.Unmarshal(raw, &w) != nil || w.Waiting == 0 || !slices.Equal(w.Widens, []string{"calls models through openrouter"}) {
		t.Fatalf("a new connection for models widens the rights and waits for the owner: %d %s", code, raw)
	}
	e.answer(w.Waiting, "allow")
	if out := call("plain", "openrouter/chat/completions"); out["status"] != 200.0 {
		t.Fatalf("once granted the call goes: %v", out)
	}

	if code, out := e.callWith(tok, "deck.project_create", map[string]any{"name": "draft", "manifest": scriptManifest("media", "", `, "models": ["openrouter", "ollama"]`)}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	e.saveScript("draft", "scripts/main.py", modelScript)
	before := len(api.calls())
	if out := call("draft", "openrouter/chat/completions"); out["status"] != 202.0 || len(api.calls()) != before {
		t.Fatalf("a draft's call of a paid model waits for the owner's yes: %v", out)
	}
	if out := call("draft", "ollama/chat/completions"); out["status"] != 200.0 || len(api.calls()) != before+1 {
		t.Fatalf("a draft's call of a free model on the owner's machine goes: %v", out)
	}
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "paid", "kind": "text", "call_usd": 0.1}, nil)
	e.must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"ollama/qwen", "openrouter/paid"}}, nil)
	e.must("deck.manifest_save", map[string]any{"project": "draft", "manifest": scriptManifest("media", "", `, "models": ["@text"]`)}, nil)
	got := e.waitScript(e.start("draft", "media", map[string]any{"key": "@/chat/completions", "other": `{"model": "@text", "messages": []}`}), "done", "error")
	var out map[string]any
	if json.Unmarshal(got.Result, &out) != nil || out["status"] != 202.0 || len(api.calls()) != before+1 {
		t.Fatalf("a draft's call that may fall back to a paid model waits for the owner: %+v", got)
	}
}

// TestAModuleCallsOnlyTheModelsOfDecksSettings: a call names a model the owner added (body, form, Google's path or
// X-Deck-Model) on a path of a model call; any other model or path is refused with what to do.
func TestAModuleCallsOnlyTheModelsOfDecksSettings(t *testing.T) {
	t.Parallel()
	ok := func(int, map[string]any) (int, string, string) { return 200, "application/json", chatText("hi") }
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": ok, "/v1/images/edits": ok, "/v1/key": ok, "/v1/models/gemma:generateContent": ok, "/svc/v1/generate": ok,
		"/v1/models/other:generateContent": ok, "/v1/videos/job-1": ok,
	})
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/v1"}, nil)
	e.must("deck.connection_set", map[string]any{"name": "gemini", "api": "google", "url": api.URL + "/v1"}, nil)
	e.must("deck.connection_set", map[string]any{"name": "svc", "api": "raw", "url": api.URL + "/svc"}, nil)
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "kind": "text", "call_usd": 0.01}, nil)
	e.must("deck.model_set", map[string]any{"connection": "openrouter", "model": "painter", "kind": "image", "call_usd": 0.02}, nil)
	e.must("deck.model_set", map[string]any{"connection": "gemini", "model": "gemma", "kind": "text", "call_usd": 0.03}, nil)
	e.must("deck.model_set", map[string]any{"connection": "svc", "model": "track", "kind": "text", "name": "svc-track", "call_usd": 0.05}, nil)
	e.createProject("shorts", scriptManifest("media", "", `, "models": ["openrouter", "gemini", "svc"]`))
	e.saveScript("shorts", "scripts/main.py", modelScript)
	call := func(in map[string]any) map[string]any {
		got := e.waitScript(e.start("shorts", "media", in), "done", "error")
		var out map[string]any
		if json.Unmarshal(got.Result, &out) != nil {
			t.Fatalf("the script ran: %+v", got)
		}
		return out
	}
	form := "--b\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\npainter\r\n--b\r\nContent-Disposition: form-data; name=\"prompt\"\r\n\r\nthe same knight\r\n--b--\r\n"
	for _, in := range []map[string]any{
		{"key": "openrouter/chat/completions", "other": `{"model": "qwen", "messages": []}`},
		{"key": "openrouter/images/edits", "form": form, "type": "multipart/form-data; boundary=b"},
		{"key": "gemini/models/gemma:generateContent", "other": `{"contents": []}`},
		{"key": "svc/v1/generate", "other": `{"prompt": "calm piano"}`, "use": "track"},
		{"key": "openrouter/videos/job-1", "method": "GET"},
	} {
		if out := call(in); out["status"] != 200.0 {
			t.Fatalf("a model of deck's settings is called: %v %v", in, out)
		}
	}
	before := len(api.calls())
	twice := strings.Replace(form, "--b--", "--b\r\nContent-Disposition: form-data; name=\"model\"\r\n\r\nanthropic/claude-opus\r\n--b--", 1)
	for _, c := range []struct {
		in   map[string]any
		says string
	}{
		{map[string]any{"key": "openrouter/chat/completions", "other": `{"model": "anthropic/claude-opus", "messages": []}`}, "not in deck's settings"},
		{map[string]any{"key": "openrouter/key", "method": "GET"}, "not a call of a model"},
		{map[string]any{"key": "gemini/models/other:generateContent", "other": `{"contents": []}`}, "not in deck's settings"},
		{map[string]any{"key": "svc/v1/generate", "other": `{"prompt": "calm piano"}`}, "not in deck's settings"},
		{map[string]any{"key": "svc/v1/generate", "other": `{"prompt": "calm piano"}`, "use": "qwen"}, "not in deck's settings"},
		{map[string]any{"key": "openrouter/chat/completions", "other": `{"model": "anthropic/claude-opus", "MODEL": "qwen", "messages": []}`}, "not MODEL"},
		{map[string]any{"key": "openrouter/chat/completions", "other": `{"model": ["qwen"], "messages": []}`}, "a string"},
		{map[string]any{"key": "gemini/models/other:generateContent", "other": `{"model": "gemma", "contents": []}`}, "two models"},
		{map[string]any{"key": "openrouter/key", "method": "GET", "use": "qwen"}, "not a call of a model"},
		{map[string]any{"key": "openrouter/chat/completions", "other": `{"messages": []}`, "use": "qwen"}, "raw service"},
		{map[string]any{"key": "openrouter/chat/completions", "other": `{"model": "qwen", "models": ["anthropic/claude-opus"], "messages": []}`}, "models"},
		{map[string]any{"key": "openrouter/images/edits", "form": twice, "type": "multipart/form-data; boundary=b"}, "once"},
		{map[string]any{"key": "openrouter/chat/completions", "other": `{"model": "qwen", "Model": "anthropic/claude-opus", "messages": []}`}, "not Model"},
		{map[string]any{"key": "gemini/models/gemma:x/%2E%2E/%2E%2E/models/other:generateContent", "other": `{"contents": []}`}, ". or .."},
		{map[string]any{"key": "openrouter/files", "other": `{"model": "qwen", "purpose": "batch"}`}, "not a call of a model"},
		{map[string]any{"key": "openrouter/chat/completions", "method": "DELETE", "other": `{"model": "qwen"}`}, "not a call of a model"},
		{map[string]any{"key": "openrouter/videos/job-1", "method": "DELETE", "other": `{}`}, "not a call of a model"},
	} {
		if out := call(c.in); out["status"] != 403.0 || !strings.Contains(out["answer"].(string), c.says) {
			t.Fatalf("a request past the models of deck's settings is refused with what to do: %v %v", c.in, out)
		}
	}
	calls := api.calls()
	if len(calls) != before {
		t.Fatalf("a refused call reaches no API: %+v", calls[before:])
	}
	for _, c := range calls {
		if c.Header != "" {
			t.Fatalf("X-Deck-Model stays with deck: %+v", c)
		}
	}
	e.refused("deck.model_request", map[string]any{"connection": "openrouter", "path": "chat/completions",
		"body": `{"model": "anthropic/claude-opus", "messages": []}`}, 403, "not in deck's settings")
	e.refused("deck.model_request", map[string]any{"connection": "openrouter", "path": "fine_tuning/jobs",
		"body": `{"model": "qwen", "training_file": "f"}`}, 403, "not a call of a model")
	e.refused("deck.model_request", map[string]any{"connection": "openrouter", "path": "chat/completions", "model": "qwen",
		"body": `{"model": "anthropic/claude-opus", "temperature": NaN}`}, 422, "is JSON")
	var spend []spendRow
	e.modelCalls("shorts", 0, &spend)
	got := map[string]float64{}
	for _, s := range spend {
		if s.Cost != nil {
			got[s.Provider] += *s.Cost
		}
	}
	if len(spend) != 5 || got["openrouter/qwen"] != 0.01 || got["openrouter/painter"] != 0.02 || got["gemini/gemma"] != 0.03 || got["svc-track"] != 0.05 {
		t.Fatalf("each call is counted on its model of deck's settings: %+v", spend)
	}
	e.createProject("plain", plainManifest)
	var models []string
	for _, r := range blockData[[]screenRow](t, e.screenBlock("shorts", "settings", "Models it called", nil)["rows"]) {
		models = append(models, fmt.Sprint(r.Cells[0]))
	}
	slices.Sort(models)
	if !slices.Equal(models, []string{"gemini/gemma", "openrouter", "openrouter/painter", "openrouter/qwen", "svc-track"}) {
		t.Fatalf("the settings of a module show the models its scripts called, a poll of a job on its connection: %v", models)
	}
	var ps []struct {
		Name   string
		Models []string
	}
	e.must("deck.projects_list", nil, &ps)
	for _, p := range ps {
		if p.Name == "shorts" && !slices.Equal(p.Models, []string{"openrouter", "gemini", "svc"}) {
			t.Fatalf("the project says what it may call: %+v", p)
		}
	}
	if rows := blockData[[]screenRow](t, e.screenBlock("plain", "settings", "Models it called", nil)["rows"]); len(rows) != 0 {
		t.Fatalf("only the module's own calls: %+v", rows)
	}
}

// TestAModuleDoesNotHoldTheKeyOfAConnection: a tool of a manifest that sends the key of a connection reaches models
// past deck; a manifest that has one is read with a warning and saved only without it.
func TestAModuleDoesNotHoldTheKeyOfAConnection(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	tool := `, "tools": {"http": [{"name": "image", "description": "A picture.", "class": "internal", "method": "POST",
	  "url": "https://openrouter.ai/api/v1/chat/completions", "auth": {"type": "secret", "secret": "openrouter"}, "input": {"type": "object"}}]}`
	e.createProject("shorts", scriptManifest("media", "", tool))
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1", "key": "sk-or"}, nil)
	var m struct{ Warnings []string }
	e.must("deck.manifest_get", map[string]any{"project": "shorts"}, &m)
	if len(m.Warnings) != 1 || !strings.Contains(m.Warnings[0], "key of connection openrouter") {
		t.Fatalf("a manifest that hands the key of a connection to a tool is read with a warning: %+v", m)
	}
	e.refused("deck.manifest_save", map[string]any{"project": "shorts", "manifest": scriptManifest("media", "", tool)}, 422, "key of connection openrouter")
	e.must("deck.manifest_save", map[string]any{"project": "shorts", "manifest": scriptManifest("media", "", "")}, nil)
}

// TestAModuleProposesAModelAndTheOwnerAddsItWithOneButton: an agent of a module that needs a model deck lacks calls
// deck.model_set; its turn waits for the owner, and the owner's yes adds the model.
func TestAModuleProposesAModelAndTheOwnerAddsItWithOneButton(t *testing.T) {
	t.Parallel()
	var said string
	var bad bool
	turns := 0
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if turns++; turns == 1 {
			said, bad = c.Tool(ctx, "model_set", map[string]any{"connection": "openrouter", "model": "google/lyria-3-pro-preview", "name": "lyria", "kind": "music", "call_usd": 0.08})
		}
		return provider.Result{Text: "proposed"}
	})
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	p := e.createProject("shorts", plainManifest)
	id := e.run(p, "work")
	e.waitState(id, "waiting_answer")
	var m modelsOut
	e.must("deck.models_list", nil, &m)
	lyria := func() bool {
		for _, x := range m.Models {
			if x.Name == "lyria" {
				return true
			}
		}
		return false
	}
	if lyria() {
		t.Fatalf("no model before the owner's yes: %+v", m.Models)
	}
	var ask int64
	if err := e.App.Store.DB.QueryRow(`SELECT id FROM ask WHERE kind = 'call' AND state = 'open' AND payload LIKE '%model_set%'`).Scan(&ask); err != nil {
		t.Fatalf("the owner has the proposal to answer: %v", err)
	}
	e.answer(ask, "allow")
	if got := e.waitState(id, "done"); got.State != "done" || bad {
		t.Fatalf("the agent goes on once the owner answered: %v %s", bad, said)
	}
	e.must("deck.models_list", nil, &m)
	if !lyria() {
		t.Fatalf("the owner's yes adds the model: %+v", m.Models)
	}
}

// TestTheSettingsOfAModuleShowTheModelsOfItsWork: the settings of a project list the models assigned to its kinds of
// work and commands, the one at work now first in mind, and only its own.
func TestTheSettingsOfAModuleShowTheModelsOfItsWork(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": "http://127.0.0.1:9/v1", "local": true}, nil)
	e.must("deck.model_set", map[string]any{"connection": "media", "model": "klein", "name": "local-image", "kind": "image", "free": true}, nil)
	e.must("deck.model_set", map[string]any{"connection": "media", "model": "cloud", "name": "cloud-image", "kind": "image", "call_usd": 0.03}, nil)
	e.createProject("shorts", scriptManifest("media", "", `, "models": ["@image"]`))
	e.createProject("plain", plainManifest)
	e.must("deck.assignment_set", map[string]any{"role": "image", "project": "shorts", "models": []string{"local-image", "cloud-image"}}, nil)
	e.must("deck.assignment_set", map[string]any{"role": "image", "project": "plain", "models": []string{"cloud-image"}}, nil)
	rows := blockData[[]screenRow](t, e.screenBlock("shorts", "settings", "Models of its work", nil)["rows"])
	if len(rows) != 1 || fmt.Sprint(rows[0].Cells...) != "imageevery commandlocal-image → cloud-imagelocal-image" {
		t.Fatalf("the settings of a module show the models of its work and only its own: %+v", rows)
	}
}
