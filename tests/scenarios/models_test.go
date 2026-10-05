package scenarios

import (
	"context"
	"encoding/json"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

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
		Name     string   `json:"name"`
		API      string   `json:"api"`
		URL      string   `json:"url"`
		Key      string   `json:"key"`
		Local    bool     `json:"local"`
		DailyUSD *float64 `json:"daily_usd"`
		Models   int      `json:"models"`
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
		"provider:old-env":                          `{"url":"https://api.example.com/v1","model":"m","key_secret":"OPENAI_API_KEY","agent":"opencode"}`,
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
		if x.Agent == "opencode" && x.Kind == "text" {
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
	Body              map[string]any
}

func newModels(t *testing.T, path map[string]func(n int, body map[string]any) (int, string, string)) *fakeModels {
	f := &fakeModels{path: path}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		model, _ := body["model"].(string)
		f.mu.Lock()
		f.got = append(f.got, modelCall{Path: r.URL.Path, Auth: r.Header.Get("Authorization"), Model: model, Body: body})
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
body = json.dumps(json.loads(inp["other"])).encode()
req = urllib.request.Request(os.environ["DECK_MODELS"] + "/" + inp["key"], data=body,
    headers={"Authorization": "Bearer " + os.environ["DECK_TOKEN"], "Content-Type": "application/json"})
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

// TestAScriptCallsAModelThroughDeckWithinTheDayOfItsConnection: a script calls a model deck has no row of through
// DECK_MODELS without its key, deck counts what OpenRouter says it cost; past the day's limit a call waits for the owner.
func TestAScriptCallsAModelThroughDeckWithinTheDayOfItsConnection(t *testing.T) {
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/api/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", pictureAnswer(0.033631)
		},
	})
	e := newEnv(t, nil)
	e.must("deck.secret_set", map[string]any{"name": "openrouter", "value": "sk-or-real"}, nil)
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/api/v1", "key_secret": "openrouter",
		"daily_usd": 0.05}, nil)
	e.createProject("shorts", scriptManifest("media", "", `, "models": ["openrouter"]`))
	e.saveScript("shorts", "scripts/main.py", modelScript)
	run := func() map[string]any {
		got := e.waitScript(e.start("shorts", "media", map[string]any{"key": "openrouter/chat/completions", "other": picture}), "done", "error")
		var out map[string]any
		if got.State != "done" || json.Unmarshal(got.Result, &out) != nil {
			t.Fatalf("the script ran: %+v", got)
		}
		return out
	}
	for i := range 2 {
		out := run()
		if out["status"] != 200.0 || !strings.Contains(out["answer"].(string), "data:image/png;base64") || len(out["keys"].([]any)) != 0 {
			t.Fatalf("call %d reaches the model with the answer as it came and no key in the script: %v", i, out)
		}
	}
	calls := api.calls()
	if len(calls) != 2 || calls[0].Auth != "Bearer sk-or-real" || calls[0].Model != "google/gemini-3.1-flash-lite-image" ||
		calls[0].Body["image_config"].(map[string]any)["aspect_ratio"] != "9:16" {
		t.Fatalf("deck put in the key and passed the body on: %+v", calls)
	}
	var spend []spendRow
	e.must("deck.llm_calls_list", map[string]any{"project": "shorts"}, &spend)
	if len(spend) != 2 || spend[0].Provider != "openrouter" || spend[0].Model != "google/gemini-3.1-flash-lite-image" || spend[0].Command != "media" ||
		spend[0].Process == 0 || spend[0].Cost == nil || *spend[0].Cost != 0.033631 {
		t.Fatalf("each call is in the journal of model calls with what OpenRouter said it cost: %+v", spend)
	}

	out := run()
	if out["status"] != 202.0 || !strings.Contains(out["answer"].(string), `"waiting"`) || len(api.calls()) != 2 {
		t.Fatalf("past the limit of the day the call waits for the owner and is not made: %v", out)
	}
	a := e.waitAsk()
	if !strings.Contains(a.Text, "deck.model_request") || !strings.Contains(a.Text, "connection openrouter spent $0.07 of its $0.05 for today") {
		t.Fatalf("the owner reads what waits and why: %+v", a)
	}
	e.must("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}, nil)
	if n := len(api.calls()); n != 3 {
		t.Fatalf("the owner's yes makes the call: %d calls", n)
	}
	e.must("deck.llm_calls_list", map[string]any{"project": "shorts"}, &spend)
	if len(spend) != 3 {
		t.Fatalf("the call made on the owner's yes is counted too: %+v", spend)
	}
}

// TestAnAgentTurnPastTheDayOfItsConnectionPausesUntilTheDayEnds: ACP turns are priced by their model and count to the
// day of its connection; past its limit the turn pauses as on a usage limit until the day ends or the pause limit.
func TestAnAgentTurnPastTheDayOfItsConnectionPausesUntilTheDayEnds(t *testing.T) {
	f := newACP(t)
	llm := newLLM(t, func(int, chatRequest) chatReply { return textReply("ok") })
	f.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`, acpModel,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	f.streamText(5, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-2"}}`, `{"reply": "session/prompt"}`, acpModel,
		`{"fail": {"code": -32603, "message": "Internal error: turn failed"}}`))
	e := acpEnv(t, f, "dsh", llm)
	e.must("deck.model_set", map[string]any{"connection": "acp", "model": "m", "name": "acp", "input_usd": 1000, "output_usd": 2000}, nil)
	p := e.acpProject("paint", colorManifest)
	if got := e.waitState(e.run(p, "work"), "done", "error"); got.State != "done" {
		t.Fatalf("the first turn runs: %+v", got)
	}
	var spend []spendRow
	e.must("deck.llm_calls_list", map[string]any{"project": "paint"}, &spend)
	if len(spend) != 1 || spend[0].Cost == nil || math.Abs(*spend[0].Cost-0.12) > 1e-9 {
		t.Fatalf("a turn is priced by its model, 100 tokens in and 10 out: %+v", spend)
	}
	var m modelsOut
	e.must("deck.models_list", nil, &m)
	url := ""
	for _, c := range m.Connections {
		if c.Name == "acp" {
			url = c.URL
		}
	}
	e.must("deck.connection_set", map[string]any{"name": "acp", "api": "openai", "url": url, "daily_usd": 0.1}, nil)
	e.must("deck.models_list", nil, &m)
	for _, c := range m.Connections {
		if c.Name == "acp" && c.Key != "secret model_key" {
			t.Fatalf("a connection changed without its key keeps it: %+v", c)
		}
	}
	before := len(llm.requests())
	id := e.run(p, "work")
	got := e.waitState(id, "done", "error", "rate_limited")
	if got.State != "rate_limited" || len(llm.requests()) != before {
		t.Fatalf("past the day of its connection the turn pauses and the model is not called: %+v", got)
	}
	pp, _ := e.App.Store.Q.GetProcess(t.Context(), id)
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.Local)
	want := midnight
	if limit := now.Add(6 * time.Hour); limit.Before(want) {
		want = limit
	}
	if until := time.UnixMilli(pp.PauseUntilMs.Int64); until.Sub(want).Abs() > 5*time.Second {
		t.Fatalf("the pause lasts until the day is over or the pause limit comes: %s, want %s", until, want)
	}
}

// TestAnAgentTurnPastTheDayOfItsProjectPausesToo: a turn of an ACP agent calls its model through deck's proxy, so
// the limit of its project holds it as the limit of its connection does, and it reaches only the model of the turn.
func TestAnAgentTurnPastTheDayOfItsProjectPausesToo(t *testing.T) {
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
	e.must("deck.project_model_limit", map[string]any{"project": "paint", "daily_usd": 0.1}, nil)
	before := len(llm.requests())
	id := e.run(p, "work")
	if got := e.waitState(id, "done", "error", "rate_limited"); got.State != "rate_limited" || len(llm.requests()) != before {
		t.Fatalf("past the day of its project the turn pauses and the model is not called: %+v", got)
	}
}

// TestAScriptCallsARoleAndAFallbackTakesARefusal: @tts goes to the first model assigned, past a 429 to the next, priced
// per call; a role reaches only the paths of model calls.
func TestAScriptCallsARoleAndAFallbackTakesARefusal(t *testing.T) {
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
	e.must("deck.llm_calls_list", map[string]any{"project": "shorts"}, &spend)
	if len(spend) != 1 || spend[0].Provider != "spare" || spend[0].Cost == nil || *spend[0].Cost != 0.02 {
		t.Fatalf("the call is counted on the model that made it, by its price per call: %+v", spend)
	}

	got = e.waitScript(e.start("shorts", "voice", map[string]any{"key": "@/files", "other": `{"model": "@tts"}`}), "done", "error")
	if json.Unmarshal(got.Result, &out) != nil || out["status"] != 403.0 || !strings.Contains(out["answer"].(string), "POST of a model call") {
		t.Fatalf("a role reaches only the paths of model calls, not the rest of the connection behind it: %+v", got)
	}
	got = e.waitScript(e.start("shorts", "voice", map[string]any{"key": "spare/images/generations", "other": `{"model": "@image"}`}), "done", "error")
	if json.Unmarshal(got.Result, &out) != nil || out["status"] != 422.0 || !strings.Contains(out["answer"].(string), "no model is assigned to image") {
		t.Fatalf("a role nothing is assigned to is refused with what to do: %+v", got)
	}
}

// TestAModelTakesItsPriceAndKindFromTheCatalogOfItsConnection: a model added without a price takes the one
// OpenRouter lists, USD per token as text, and the kind from what it outputs.
func TestAModelTakesItsPriceAndKindFromTheCatalogOfItsConnection(t *testing.T) {
	catalog := `{"data": [{"id": "google/gemini-3.1-flash-lite-image", "pricing": {"prompt": "0.0000003", "completion": "0.0000025", "request": "0", "image": "0.00003"},
	  "architecture": {"input_modalities": ["text", "image"], "output_modalities": ["image", "text"]}},
	  {"id": "qwen/qwen3.8-27b:free", "pricing": {"prompt": "0", "completion": "0"}, "architecture": {"output_modalities": ["text"]}}]}`
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
	if c := api.calls(); len(c) != 2 || c[0].Auth != "Bearer sk-or-new" {
		t.Fatalf("the key given with the connection is kept in a secret and used: %+v", c)
	}
	var secrets []struct{ Name string }
	e.must("deck.secrets_list", nil, &secrets)
	if len(secrets) != 1 || secrets[0].Name != "openrouter" {
		t.Fatalf("the key is a secret named after the connection: %+v", secrets)
	}
}

// TestAModuleCallsOnlyTheModelsTheOwnerGranted: a project calls models only through what its manifest names; a new
// name waits for the owner's button, and a draft's call waits for a yes unless its model is free on this machine.
func TestAModuleCallsOnlyTheModelsTheOwnerGranted(t *testing.T) {
	api := newModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) { return 200, "application/json", chatText("hi") },
	})
	e := newEnv(t, nil)
	e.must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/v1"}, nil)
	e.must("deck.connection_set", map[string]any{"name": "ollama", "api": "openai", "url": api.URL + "/v1", "local": true}, nil)
	e.must("deck.model_set", map[string]any{"connection": "ollama", "model": "qwen", "kind": "text", "free": true}, nil)
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
