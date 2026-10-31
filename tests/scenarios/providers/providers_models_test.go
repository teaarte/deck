package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"deck2/internal/health"
	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/tests/harness"
)

// TestTwinsOfAModelMergeIntoTheFirst: models of one connection with the same API name, kind and options become the
// first of them on upgrade, which takes their agent, assignments, chat topics, processes and sessions.
func TestTwinsOfAModelMergeIntoTheFirst(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "deck.db")
	st, err := store.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`INSERT INTO connection (workspace_id, name, url) VALUES (1, 'google', 'http://127.0.0.1:9')`,
		`INSERT INTO model (workspace_id, connection_id, name, model, options) SELECT 1, id, 'google/flash', 'flash', '{}' FROM connection WHERE name = 'google'`,
		`INSERT INTO model (workspace_id, connection_id, name, model, options, agent) SELECT 1, id, 'google-flash', 'flash', '{ }', 'goose' FROM connection WHERE name = 'google'`,
		`INSERT INTO model (workspace_id, connection_id, name, model, options) SELECT 1, id, 'flash-ru', 'flash', '{"language": "ru"}' FROM connection WHERE name = 'google'`,
		`INSERT INTO assignment (workspace_id, role, models) VALUES (1, 'agent', '["google-flash", "flash-ru", "google/flash"]')`,
		`INSERT INTO chat_topic (workspace_id, title, model) VALUES (1, 'talk', 'google-flash')`,
		`INSERT INTO chat_session (topic_id, provider, session_id) SELECT id, 'google-flash', 's1' FROM chat_topic`,
		`INSERT INTO project (workspace_id, name) VALUES (1, 'work')`,
		`INSERT INTO process (project_id, command, state, provider) SELECT id, 'run', 'paused', 'google-flash' FROM project WHERE name = 'work'`,
		`DROP TABLE picture`, `DROP TABLE layout`,
		`ALTER TABLE file DROP COLUMN thumb_id`, `ALTER TABLE file DROP COLUMN width`, `ALTER TABLE file DROP COLUMN height`,
		`ALTER TABLE file DROP COLUMN color`,
		`DROP INDEX process_finished`, `ALTER TABLE process DROP COLUMN allows`, `ALTER TABLE process DROP COLUMN wrote`,
		`DROP INDEX feed_line`, `DROP INDEX feed_run`, `ALTER TABLE feed DROP COLUMN line_id`, `ALTER TABLE feed DROP COLUMN what`,
		`DELETE FROM goose_db_version WHERE version_id >= 46`,
	} {
		if _, err := st.DB.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	st.Close()
	if st, err = store.Open(path); err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	var names, agent, models, topic, session, provider string
	for _, r := range []struct {
		q   string
		out *string
	}{
		{`SELECT group_concat(name, ',') FROM (SELECT name FROM model WHERE model = 'flash' ORDER BY id)`, &names},
		{`SELECT agent FROM model WHERE name = 'google/flash'`, &agent},
		{`SELECT models FROM assignment WHERE role = 'agent'`, &models},
		{`SELECT model FROM chat_topic`, &topic},
		{`SELECT provider FROM chat_session`, &session},
		{`SELECT provider FROM process`, &provider},
	} {
		if err := st.DB.QueryRow(r.q).Scan(r.out); err != nil {
			t.Fatalf("%s: %v", r.q, err)
		}
	}
	if names != "google/flash,flash-ru" || agent != "goose" || models != `["google/flash","flash-ru"]` ||
		topic != "google/flash" || session != "google/flash" || provider != "google/flash" {
		t.Fatalf("a twin merges into the first, one with its own options stays: %s %s %s %s %s %s", names, agent, models, topic, session, provider)
	}
}

// TestAnAssignmentTakesItsFallbackWhileTheFirstIsDown: a new turn of a project starts on the first provider of
// its assignment, and on the next one while the first is red, waiting for a login or paused by a limit.
func TestAnAssignmentTakesItsFallbackWhileTheFirstIsDown(t *testing.T) {
	t.Parallel()
	main := &harness.Fake{Script: func(context.Context, *harness.Call) provider.Result { return provider.Result{Text: "main"} }}
	other := &harness.Fake{ProviderName: "other", Script: func(context.Context, *harness.Call) provider.Result { return provider.Result{Text: "other"} }}
	e := harness.New(t, harness.WithProvider(main, other))
	p := e.Project("plain", harness.PlainManifest)
	e.Must("deck.assignment_set", map[string]any{"role": "agent", "project": "plain", "models": []string{"other", "fake"}}, nil)
	if got := e.WaitState(e.Run(p, "work"), "done"); !strings.Contains(string(got.Result), "other") {
		t.Fatalf("the first of the assignment of the project runs its commands: %s", got.Result)
	}
	health.Write(t.Context(), e.App.Store, health.Of{}, "provider:other", "paused", "the usage limit")
	if got := e.WaitState(e.Run(p, "work"), "done"); !strings.Contains(string(got.Result), "main") {
		t.Fatalf("a fallback takes the turn while the first is paused: %s", got.Result)
	}
	var m harness.ModelsOut
	e.Must("deck.models_list", nil, &m)
	for _, a := range m.Assignments {
		if a.Project == "plain" && a.Using != "fake" {
			t.Fatalf("the assignment says the fallback works: %+v", a)
		}
	}
	health.Write(t.Context(), e.App.Store, health.Of{}, "provider:other", "ok", "")
	if got := e.WaitState(e.Run(p, "work"), "done"); !strings.Contains(string(got.Result), "other") {
		t.Fatalf("back to the first once it is up: %s", got.Result)
	}
	e.Must("deck.assignment_set", map[string]any{"role": "agent", "project": "plain", "models": []string{}}, nil)
	if got := e.WaitState(e.Run(p, "work"), "done"); !strings.Contains(string(got.Result), "main") {
		t.Fatalf("without the assignment the main provider runs: %s", got.Result)
	}
	e.Refused("deck.assignment_set", map[string]any{"role": "agent", "project": "plain", "models": []string{"nobody"}}, 404, "no provider")
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

// picture is the body the films module sends for a picture, as its script builds it.
const picture = `{"model": "google/gemini-3.1-flash-lite-image", "messages": [{"role": "user", "content": "A medieval manuscript painter"}],
  "modalities": ["image", "text"], "image_config": {"aspect_ratio": "9:16"}}`

// TestAScriptCallsAModelThroughDeckWithoutItsKey: a script calls a model through DECK_MODELS without its key, deck
// keeps what OpenRouter says it cost; a connection takes no daily limit.
func TestAScriptCallsAModelThroughDeckWithoutItsKey(t *testing.T) {
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/api/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", harness.PictureAnswer(0.033631)
		},
	})
	e := harness.New(t)
	e.Secret("films", "openrouter", "sk-or-real")
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/api/v1", "key_secret": "openrouter"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "google/gemini-3.1-flash-lite-image", "kind": "image", "name": "painter", "call_usd": 0.04}, nil)
	e.Project("films", harness.ScriptManifest("media", "", `, "models": ["openrouter"]`))
	e.SaveScript("films", "scripts/main.py", modelScript)
	for i := range 3 {
		got := e.WaitState(e.Start("films", "media", map[string]any{"key": "openrouter/chat/completions", "other": picture}), "done", "error")
		var out map[string]any
		if got.State != "done" || json.Unmarshal(got.Result, &out) != nil {
			t.Fatalf("the script ran: %+v", got)
		}
		if out["status"] != 200.0 || !strings.Contains(out["answer"].(string), "data:image/png;base64") || len(out["keys"].([]any)) != 0 {
			t.Fatalf("call %d reaches the model with the answer as it came and no key in the script: %v", i, out)
		}
	}
	calls := api.Calls()
	if len(calls) != 3 || calls[0].Auth != "Bearer sk-or-real" || calls[0].Model != "google/gemini-3.1-flash-lite-image" ||
		calls[0].Body["image_config"].(map[string]any)["aspect_ratio"] != "9:16" {
		t.Fatalf("deck put in the key and passed the body on: %+v", calls)
	}
	var spend []harness.SpendRow
	e.ModelCalls("films", 0, &spend)
	if len(spend) != 3 || spend[0].Provider != "painter" || spend[0].Model != "google/gemini-3.1-flash-lite-image" || spend[0].Command != "media" ||
		spend[0].Process == 0 || spend[0].Cost == nil || *spend[0].Cost != 0.033631 {
		t.Fatalf("each call is in the journal of model calls with what OpenRouter said it cost: %+v", spend)
	}
	e.Refused("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/api/v1", "daily_usd": 1}, 422, "daily_usd")
}

// TestAnAgentTurnReachesOnlyTheModelOfItsTurn: a turn of an ACP agent calls its model through deck's proxy, which
// sends every call to the model the turn started on, whatever model the agent names, priced by that model.
func TestAnAgentTurnReachesOnlyTheModelOfItsTurn(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	llm := harness.NewLLM(t, func(int, harness.ChatRequest) harness.ChatReply { return harness.TextReply("ok") })
	f.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"model": {"model": "pricier-model", "messages": [{"role": "user", "content": "hi"}]}}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"color": "blue"}}}}`, `{"end": "end_turn"}`))
	f.StreamText(5, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-2"}}`, `{"reply": "session/prompt"}`, harness.AcpModel,
		`{"fail": {"code": -32603, "message": "Internal error: turn failed"}}`))
	e := harness.New(t, harness.WithACP(f, llm))
	e.Must("deck.model_set", map[string]any{"connection": "acp", "model": "m", "name": "acp", "input_usd": 1000, "output_usd": 2000}, nil)
	p := e.AcpProject("paint", harness.ColorManifest)
	if got := e.WaitState(e.Run(p, "work"), "done", "error"); got.State != "done" {
		t.Fatalf("the first turn runs: %+v", got)
	}
	reqs := llm.Requests()
	if last := reqs[len(reqs)-1]; last.Model != "m" {
		t.Fatalf("an agent reaches only the model of its turn: %+v", last)
	}
	var spend []harness.SpendRow
	e.ModelCalls("paint", 0, &spend)
	if len(spend) != 1 || spend[0].Cost == nil || math.Abs(*spend[0].Cost-0.12) > 1e-9 {
		t.Fatalf("a turn is priced by its model, 100 tokens in and 10 out: %+v", spend)
	}
}

// TestAScriptCallsARoleAndAFallbackTakesARefusal: @tts goes to the first model assigned, past a 429 to the next, priced
// per call; a role reaches only the paths of model calls.
func TestAScriptCallsARoleAndAFallbackTakesARefusal(t *testing.T) {
	t.Parallel()
	busy := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/audio/speech": func(int, map[string]any) (int, string, string) {
			return 429, "application/json", `{"error": "rate limited"}`
		},
	})
	spare := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/audio/speech": func(int, map[string]any) (int, string, string) { return 200, "audio/mpeg", "ID3-voice" },
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "busy", "api": "openai", "url": busy.URL + "/v1"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "spare", "api": "openai", "url": spare.URL + "/v1"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "busy", "model": "voice-1", "kind": "tts", "call_usd": 0.01}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "spare", "model": "voice-2", "kind": "tts", "call_usd": 0.02}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "tts", "models": []string{"busy/voice-1", "spare/voice-2"}}, nil)
	e.Project("films", harness.ScriptManifest("voice", "", `, "models": ["@tts", "spare"]`))
	e.SaveScript("films", "scripts/main.py", modelScript)
	speech := `{"model": "@tts", "input": "Knights against snails", "voice": "alloy", "response_format": "mp3"}`
	got := e.WaitState(e.Start("films", "voice", map[string]any{"key": "@/audio/speech", "other": speech}), "done", "error")
	var out map[string]any
	if json.Unmarshal(got.Result, &out) != nil || out["status"] != 200.0 || out["answer"] != "ID3-voice" {
		t.Fatalf("the fallback answers after the refusal of the first: %+v", got)
	}
	if b, s := busy.Calls(), spare.Calls(); len(b) != 1 || b[0].Model != "voice-1" || len(s) != 1 || s[0].Model != "voice-2" || s[0].Body["voice"] != "alloy" {
		t.Fatalf("each model got the body with its own name in place of the role: %+v %+v", b, s)
	}
	var spend []harness.SpendRow
	e.ModelCalls("films", 0, &spend)
	if len(spend) != 1 || spend[0].Provider != "spare/voice-2" || spend[0].Cost == nil || *spend[0].Cost != 0.02 {
		t.Fatalf("the call is counted on the model that made it, by its price per call: %+v", spend)
	}

	got = e.WaitState(e.Start("films", "voice", map[string]any{"key": "@/files", "other": `{"model": "@tts"}`}), "done", "error")
	if json.Unmarshal(got.Result, &out) != nil || out["status"] != 403.0 || !strings.Contains(out["answer"].(string), "not a call of a model") {
		t.Fatalf("a role reaches only the paths of model calls, not the rest of the connection behind it: %+v", got)
	}
	got = e.WaitState(e.Start("films", "voice", map[string]any{"key": "spare/images/generations", "other": `{"model": "@image"}`}), "done", "error")
	if json.Unmarshal(got.Result, &out) != nil || out["status"] != 422.0 || !strings.Contains(out["answer"].(string), "no model is assigned to image") {
		t.Fatalf("a role nothing is assigned to is refused with what to do: %+v", got)
	}
}

// TestAModelTakesItsPriceAndKindFromTheCatalogOfItsConnection: a model added without a price takes the one OpenRouter
// lists and its kind, video too; free is kept when left out, false clears it, a zero of a model making more than text is no proof.
func TestAModelTakesItsPriceAndKindFromTheCatalogOfItsConnection(t *testing.T) {
	t.Parallel()
	catalog := `{"data": [{"id": "google/gemini-3.1-flash-lite-image", "pricing": {"prompt": "0.0000003", "completion": "0.0000025", "request": "0", "image": "0.00003"},
	  "architecture": {"input_modalities": ["text", "image"], "output_modalities": ["image", "text"]}},
	  {"id": "qwen/qwen3.8-27b:free", "pricing": {"prompt": "0", "completion": "0"}, "architecture": {"output_modalities": ["text"]}},
	  {"id": "google/lyria-3-pro-preview", "pricing": {"prompt": "0", "completion": "0"}, "architecture": {"output_modalities": ["text", "audio"]}},
	  {"id": "alibaba/wan-2.6", "pricing": {"prompt": "0", "completion": "0"}, "architecture": {"input_modalities": ["text", "image"], "output_modalities": ["video"]}},
	  {"id": "nvidia/nemotron-3:free", "architecture": {"output_modalities": ["text"]}}]}`
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/api/v1/models": func(int, map[string]any) (int, string, string) { return 200, "application/json", catalog },
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/api/v1", "key": "sk-or-new"}, nil)
	var m struct {
		Name      string
		Kind      string
		InputUSD  float64 `json:"input_usd"`
		OutputUSD float64 `json:"output_usd"`
		Free      bool
	}
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "google/gemini-3.1-flash-lite-image"}, &m)
	if m.Name != "openrouter/google/gemini-3.1-flash-lite-image" || m.Kind != "image" || math.Abs(m.InputUSD-0.3) > 1e-9 || math.Abs(m.OutputUSD-2.5) > 1e-9 || m.Free {
		t.Fatalf("the price per million tokens and the kind come from the catalog: %+v", m)
	}
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen/qwen3.8-27b:free", "name": "qwen"}, &m)
	if m.Kind != "text" || !m.Free {
		t.Fatalf("a model the catalog lists at zero is free: %+v", m)
	}
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "google/lyria-3-pro-preview", "name": "lyria"}, &m)
	if m.Free {
		t.Fatalf("a zero in tokens is no price of a song: a model making more than text is free only when the catalog says so: %+v", m)
	}
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "alibaba/wan-2.6", "name": "wan"}, &m)
	if m.Kind != "video" || m.Free {
		t.Fatalf("a model of the catalog making video makes video and is not free on a zero: %+v", m)
	}
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "nvidia/nemotron-3:free", "name": "nemotron"}, &m)
	if !m.Free {
		t.Fatalf("a model whose id ends in :free is free though the catalog lists no price: %+v", m)
	}
	if c := api.Calls(); len(c) != 5 || c[0].Auth != "Bearer sk-or-new" {
		t.Fatalf("the key given with the connection is kept in a secret and used: %+v", c)
	}
	var secrets []struct{ Name string }
	e.Must("deck.secrets_list", nil, &secrets)
	if len(secrets) != 1 || secrets[0].Name != "openrouter" {
		t.Fatalf("the key is a secret named after the connection: %+v", secrets)
	}
	m.InputUSD = 0
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen/qwen3.8-27b:free", "name": "qwen", "kind": "text"}, &m)
	if !m.Free || math.Abs(m.InputUSD) > 1e-9 {
		t.Fatalf("a model changed without free or a price keeps them: %+v", m)
	}
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen/qwen3.8-27b:free", "name": "qwen", "free": false}, &m)
	if m.Free {
		t.Fatalf("free false makes a model paid again: %+v", m)
	}
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen/qwen3.8-27b:free", "name": "qwen", "free": true}, &m)
}

// TestAModuleCallsOnlyTheModelsTheOwnerGranted: a project calls models only through what its manifest names; a new
// name waits for the owner's button, and a draft calls only a model that is free on this machine.
func TestAModuleCallsOnlyTheModelsTheOwnerGranted(t *testing.T) {
	t.Parallel()
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": func(int, map[string]any) (int, string, string) {
			return 200, "application/json", harness.ChatText("hi")
		},
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/v1"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "ollama", "api": "openai", "url": api.URL + "/v1", "local": true}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "ollama", "model": "qwen", "kind": "text", "free": true}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "kind": "text", "call_usd": 0.01}, nil)
	call := func(project, path string) map[string]any {
		got := e.WaitState(e.Start(project, "media", map[string]any{"key": path, "other": `{"model": "qwen", "messages": []}`}), "done", "error")
		var out map[string]any
		if json.Unmarshal(got.Result, &out) != nil {
			t.Fatalf("the script ran: %+v", got)
		}
		return out
	}
	e.Project("plain", harness.ScriptManifest("media", "", ""))
	e.SaveScript("plain", "scripts/main.py", modelScript)
	if out := call("plain", "openrouter/chat/completions"); out["status"] != 403.0 || !strings.Contains(out["answer"].(string), `add "openrouter" to models`) {
		t.Fatalf("a call through a connection the manifest does not name is refused with what to do: %v", out)
	}
	_, tok := e.Token("terminal", "deck.*")
	var w struct {
		Waiting int64
		Widens  []string
	}
	code, raw := e.As(tok).Call("deck.manifest_save", map[string]any{"project": "plain", "manifest": harness.ScriptManifest("media", "", `, "models": ["openrouter"]`)})
	if code != 200 || json.Unmarshal(raw, &w) != nil || w.Waiting == 0 || !slices.Equal(w.Widens, []string{"calls models through openrouter"}) {
		t.Fatalf("a new connection for models widens the rights and waits for the owner: %d %s", code, raw)
	}
	e.Answer(w.Waiting, "allow")
	if out := call("plain", "openrouter/chat/completions"); out["status"] != 200.0 {
		t.Fatalf("once granted the call goes: %v", out)
	}

	if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "draft", "manifest": harness.ScriptManifest("media", "", `, "models": ["openrouter", "ollama"]`)}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	e.SaveScript("draft", "scripts/main.py", modelScript)
	before := len(api.Calls())
	if out := call("draft", "openrouter/chat/completions"); out["status"] != 403.0 || !strings.Contains(out["answer"].(string), "only the free models") || len(api.Calls()) != before {
		t.Fatalf("a draft's call of a paid model is refused with what to do: %v", out)
	}
	if out := call("draft", "ollama/chat/completions"); out["status"] != 200.0 || len(api.Calls()) != before+1 {
		t.Fatalf("a draft's call of a free model on the owner's machine goes: %v", out)
	}
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "paid", "kind": "text", "call_usd": 0.1}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"ollama/qwen", "openrouter/paid"}}, nil)
	e.Must("deck.manifest_save", map[string]any{"project": "draft", "manifest": harness.ScriptManifest("media", "", `, "models": ["@text"]`)}, nil)
	got := e.WaitState(e.Start("draft", "media", map[string]any{"key": "@/chat/completions", "other": `{"model": "@text", "messages": []}`}), "done", "error")
	var out map[string]any
	if json.Unmarshal(got.Result, &out) != nil || out["status"] != 403.0 || len(api.Calls()) != before+1 {
		t.Fatalf("a draft's call that may fall back to a paid model is refused: %+v", got)
	}
}

// TestAModuleCallsOnlyTheModelsOfDecksSettings: a call names a model the owner added (body, form, Google's path or
// X-Deck-Model) on a path of a model call; any other model or path is refused with what to do.
func TestAModuleCallsOnlyTheModelsOfDecksSettings(t *testing.T) {
	t.Parallel()
	ok := func(int, map[string]any) (int, string, string) {
		return 200, "application/json", harness.ChatText("hi")
	}
	api := harness.NewModels(t, map[string]func(int, map[string]any) (int, string, string){
		"/v1/chat/completions": ok, "/v1/images/edits": ok, "/v1/key": ok, "/v1/models/gemma:generateContent": ok, "/svc/v1/generate": ok,
		"/v1/models/other:generateContent": ok, "/v1/videos/job-1": ok,
	})
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": api.URL + "/v1"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "gemini", "api": "google", "url": api.URL + "/v1"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "svc", "api": "raw", "url": api.URL + "/svc"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "kind": "text", "call_usd": 0.01}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "painter", "kind": "image", "call_usd": 0.02}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "gemini", "model": "gemma", "kind": "text", "call_usd": 0.03}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "svc", "model": "track", "kind": "text", "name": "svc-track", "call_usd": 0.05}, nil)
	e.Project("films", harness.ScriptManifest("media", "", `, "models": ["openrouter", "gemini", "svc"]`))
	e.SaveScript("films", "scripts/main.py", modelScript)
	call := func(in map[string]any) map[string]any {
		got := e.WaitState(e.Start("films", "media", in), "done", "error")
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
	before := len(api.Calls())
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
	calls := api.Calls()
	if len(calls) != before {
		t.Fatalf("a refused call reaches no API: %+v", calls[before:])
	}
	for _, c := range calls {
		if c.Header != "" {
			t.Fatalf("X-Deck-Model stays with deck: %+v", c)
		}
	}
	if out := call(map[string]any{"key": "@/chat/completions", "other": `{"model": "@search", "messages": []}`}); out["status"] != 422.0 ||
		!strings.Contains(out["answer"].(string), "web search of agents") {
		t.Fatalf("@search is not a model to call: %v", out)
	}
	e.Refused("deck.model_request", map[string]any{"connection": "openrouter", "path": "chat/completions",
		"body": `{"model": "qwen", "messages": []}`}, 404, "model_request")
	var spend []harness.SpendRow
	e.ModelCalls("films", 0, &spend)
	got := map[string]float64{}
	for _, s := range spend {
		if s.Cost != nil {
			got[s.Provider] += *s.Cost
		}
	}
	if len(spend) != 5 || got["openrouter/qwen"] != 0.01 || got["openrouter/painter"] != 0.02 || got["gemini/gemma"] != 0.03 || got["svc-track"] != 0.05 {
		t.Fatalf("each call is counted on its model of deck's settings: %+v", spend)
	}
	e.Project("plain", harness.PlainManifest)
	var models []string
	for _, r := range harness.BlockData[[]harness.ScreenRow](t, e.ScreenBlock("films", "settings", "Model calls", nil)["rows"]) {
		models = append(models, fmt.Sprint(r.Cells[1]))
	}
	slices.Sort(models)
	if !slices.Equal(models, []string{"gemini/gemma", "openrouter", "openrouter/painter", "openrouter/qwen", "svc-track"}) {
		t.Fatalf("the settings of a module show the models its scripts called, a poll of a job on its connection: %v", models)
	}
	var ps []struct {
		Name   string
		Models []string
	}
	e.Must("deck.projects_list", nil, &ps)
	for _, p := range ps {
		if p.Name == "films" && !slices.Equal(p.Models, []string{"openrouter", "gemini", "svc"}) {
			t.Fatalf("the project says what it may call: %+v", p)
		}
	}
	if drawn, _ := json.Marshal(e.ScreenMessages("plain", "settings", nil)); strings.Contains(string(drawn), `"Model calls"`) {
		t.Fatalf("only the module's own calls, none of another: %s", drawn)
	}
}

// TestAModuleDoesNotHoldTheKeyOfAConnection: a tool of a manifest that sends the key of a connection reaches models
// past deck; a manifest that has one is read with a warning and saved only without it.
func TestAModuleDoesNotHoldTheKeyOfAConnection(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	tool := `, "tools": {"http": [{"name": "image", "description": "A picture.", "class": "internal", "method": "POST",
	  "url": "https://openrouter.ai/api/v1/chat/completions", "auth": {"type": "secret", "secret": "openrouter"}, "input": {"type": "object"}}]}`
	e.Project("films", harness.ScriptManifest("media", "", tool))
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1", "key": "sk-or"}, nil)
	var m struct{ Warnings []string }
	e.Must("deck.manifest_get", map[string]any{"project": "films"}, &m)
	if len(m.Warnings) != 1 || !strings.Contains(m.Warnings[0], "key of connection openrouter") {
		t.Fatalf("a manifest that hands the key of a connection to a tool is read with a warning: %+v", m)
	}
	e.Refused("deck.manifest_save", map[string]any{"project": "films", "manifest": harness.ScriptManifest("media", "", tool)}, 422, "key of connection openrouter")
	e.Must("deck.manifest_save", map[string]any{"project": "films", "manifest": harness.ScriptManifest("media", "", "")}, nil)
}

// TestAModuleProposesAModelAndTheOwnerAddsItWithOneButton: an agent of a module that needs a model deck lacks calls
// deck.model_set; its turn waits for the owner, and the owner's yes adds the model.
func TestAModuleProposesAModelAndTheOwnerAddsItWithOneButton(t *testing.T) {
	t.Parallel()
	var said string
	var bad bool
	turns := 0
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if turns++; turns == 1 {
			said, bad = c.Tool(ctx, "model_set", map[string]any{"connection": "openrouter", "model": "google/lyria-3-pro-preview", "name": "lyria", "kind": "music", "call_usd": 0.08})
		}
		return provider.Result{Text: "proposed"}
	}))

	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	p := e.Project("films", harness.PlainManifest)
	id := e.Run(p, "work")
	e.WaitState(id, "waiting_answer")
	var m harness.ModelsOut
	e.Must("deck.models_list", nil, &m)
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
	for _, a := range e.Asks() {
		if a.Kind == "call" && strings.Contains(a.Text, "model_set") {
			ask = a.ID
		}
	}
	if ask == 0 {
		t.Fatalf("the owner has the proposal to answer: %+v", e.Asks())
	}
	e.Answer(ask, "allow")
	if got := e.WaitState(id, "done"); got.State != "done" || bad {
		t.Fatalf("the agent goes on once the owner answered: %v %s", bad, said)
	}
	e.Must("deck.models_list", nil, &m)
	if !lyria() {
		t.Fatalf("the owner's yes adds the model: %+v", m.Models)
	}
}

// TestTheSettingsOfAModuleShowTheModelsOfItsWork: the settings of a project list the models assigned to its kinds of
// work and commands, the one at work now first in mind, and only its own.
func TestTheSettingsOfAModuleShowTheModelsOfItsWork(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "media", "api": "openai", "url": "http://127.0.0.1:9/v1", "local": true}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "media", "model": "klein", "name": "local-image", "kind": "image", "free": true}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "media", "model": "cloud", "name": "cloud-image", "kind": "image", "call_usd": 0.03}, nil)
	e.Project("films", harness.ScriptManifest("media", "", `, "models": ["@image"]`))
	e.Project("plain", harness.PlainManifest)
	e.Must("deck.assignment_set", map[string]any{"role": "image", "project": "films", "models": []string{"local-image", "cloud-image"}}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "image", "project": "plain", "models": []string{"cloud-image"}}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "image", "models": []string{"cloud-image"}}, nil)
	e.Project("pics", harness.ScriptManifest("media", "", `, "models": ["@image"]`))
	pics := harness.BlockData[[]harness.ScreenRow](t, e.ScreenBlock("pics", "settings", "Models", nil)["rows"])
	if len(pics) != 2 || fmt.Sprint(pics[1].Cells[:3]...) != "imagecloud-imagedefault" {
		t.Fatalf("a kind of work on the model deck's settings give everywhere is the default, not the module's own: %+v", pics)
	}
	rows := harness.BlockData[[]harness.ScreenRow](t, e.ScreenBlock("films", "settings", "Models", nil)["rows"])
	if len(rows) != 2 || fmt.Sprint(rows[0].Cells[0], rows[0].Cells[2]) != "Publishdefault" || fmt.Sprint(rows[1].Cells...) != "imagelocal-imageherefree" {
		t.Fatalf("the settings of a module show the models of its commands and scripts and only its own: %+v", rows)
	}
	rows = rows[1:]
	plain := harness.BlockData[[]harness.ScreenRow](t, e.ScreenBlock("plain", "settings", "Models", nil)["rows"])
	if len(plain) != 1 || fmt.Sprint(plain[0].Cells[0], plain[0].Cells[2]) != "Workdefault" || plain[0].Cells[1] == "" {
		t.Fatalf("a command runs on a model, the default one when the module picked none: %+v", plain)
	}
	pick := rows[0].Actions[slices.IndexFunc(rows[0].Actions, func(a map[string]any) bool { return a["verb"] == "model" })]
	if fmt.Sprint(pick["options"]) != "[cloud-image local-image]" {
		t.Fatalf("the model is picked from the models of its kind: %v", pick)
	}
	params := pick["params"].(map[string]any)
	params["model"] = "cloud-image"
	e.Must(pick["command"].(string), params, nil)
	rows = harness.BlockData[[]harness.ScreenRow](t, e.ScreenBlock("films", "settings", "Models", nil)["rows"])[1:]
	var list harness.ModelsOut
	e.Must("deck.models_list", map[string]any{"project": "films"}, &list)
	if fmt.Sprint(rows[0].Cells...) != "imagecloud-imagehere$0.03 a call" || fmt.Sprint(list.Assignments[0].Models) != "[cloud-image local-image]" {
		t.Fatalf("a picked model goes first and the one before stays its fallback: %v %+v", rows[0].Cells, list.Assignments)
	}
}

// TestAModuleScriptReadsTheModelsOfDeck: a script of a module reads the models of deck and their assignments to the
// workspace and its own project, without the secrets connections keep their keys in.
func TestAModuleScriptReadsTheModelsOfDeck(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "openrouter", "api": "openai", "url": "https://openrouter.ai/api/v1", "key": "sk-or-hidden"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "qwen", "kind": "text", "call_usd": 0.01}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "openrouter", "model": "painter", "kind": "image", "call_usd": 0.02}, nil)
	e.Project("films", harness.ScriptManifest("media", "", ""))
	e.Project("plain", harness.PlainManifest)
	e.Must("deck.assignment_set", map[string]any{"role": "text", "models": []string{"openrouter/qwen"}}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "image", "project": "films", "models": []string{"openrouter/painter"}}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "image", "project": "plain", "models": []string{"openrouter/painter"}}, nil)
	e.SaveScript("films", "scripts/main.py", harness.PythonLib+`
status, out = deck("deck.models_list", {})
print(json.dumps({"status": status, "out": out}))
`)
	got := e.WaitState(e.Start("films", "media", nil), "done", "error")
	var res struct {
		Status int
		Out    harness.ModelsOut
	}
	if json.Unmarshal(got.Result, &res) != nil || res.Status != 200 {
		t.Fatalf("a script of a module reads the models of deck: %+v %s", got, got.Result)
	}
	var models []string
	for _, m := range res.Out.Models {
		models = append(models, m.Name)
	}
	if !slices.Contains(models, "openrouter/qwen") || !slices.Contains(models, "openrouter/painter") {
		t.Fatalf("the models of deck are listed: %v", models)
	}
	i := slices.IndexFunc(res.Out.Connections, func(c harness.ModelsOutConnection) bool { return c.Name == "openrouter" })
	if i < 0 || res.Out.Connections[i].Key != "secret" || strings.Contains(string(got.Result), "key_secret") ||
		strings.Contains(string(got.Result), "sk-or-hidden") {
		t.Fatalf("a module learns that a connection has a key, not where deck keeps it: %s", got.Result)
	}
	var where []string
	for _, a := range res.Out.Assignments {
		where = append(where, a.Role+":"+a.Project)
	}
	slices.Sort(where)
	if !slices.Equal(where, []string{"image:films", "stt:", "text:"}) {
		t.Fatalf("a module sees the assignments of the workspace and its own, not of other projects: %v", where)
	}
}

// TestAssignmentsGoWithTheirCommandsAndModels: an assignment of a command a save drops goes, one may always be
// removed, a removed model leaves the assignments that named it, and one deck has no model of is in health.
func TestAssignmentsGoWithTheirCommandsAndModels(t *testing.T) {
	t.Parallel()
	other := &harness.Fake{ProviderName: "other", Script: func(context.Context, *harness.Call) provider.Result { return provider.Result{Text: "other"} }}
	e := harness.New(t, harness.WithProvider(&harness.Fake{}, other))
	e.Project("plain", harness.TestManifest([]string{harness.AgentCommand("work", ""), harness.AgentCommand("spare", "")}, nil))
	e.Must("deck.assignment_set", map[string]any{"role": "agent", "project": "plain", "command": "spare", "models": []string{"other"}}, nil)
	assigned := func(command string) *harness.AssignmentOut {
		var m harness.ModelsOut
		e.Must("deck.models_list", nil, &m)
		for _, a := range m.Assignments {
			if a.Project == "plain" && a.Command == command {
				return &a
			}
		}
		return nil
	}
	e.Must("deck.manifest_save", map[string]any{"project": "plain", "manifest": harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil)}, nil)
	e.Until("the assignment of a command the manifest dropped goes", func() bool { return assigned("spare") == nil })

	// An assignment an older deck left to a command no manifest has, which assignment_set refuses.
	e.SQL(`INSERT INTO assignment (workspace_id, project_id, command, role, models) SELECT 1, id, 'gone', 'agent', '["other"]' FROM project WHERE name = 'plain'`)
	e.Must("deck.assignment_set", map[string]any{"role": "agent", "project": "plain", "command": "gone", "models": []string{}}, nil)
	if assigned("gone") != nil {
		t.Fatal("an assignment of a command that is gone is removed")
	}

	// An assignment left to a model deck no longer has, which assignment_set refuses.
	e.SQL(`INSERT INTO assignment (workspace_id, project_id, command, role, models) SELECT 1, id, 'work', 'agent', '["vanished", "other"]' FROM project WHERE name = 'plain'`)
	if a := assigned("work"); a == nil || a.Using != "other" || !strings.Contains(a.Why, "vanished") {
		t.Fatalf("a model deck has none of is not the one that works, and the assignment says why: %+v", a)
	}
	e.Must("deck.connection_set", map[string]any{"name": "router", "api": "openai", "url": "https://openrouter.ai/api/v1"}, nil)
	if h := e.WaitHealth("model:vanished", "warning"); !strings.Contains(h.Error, "plain.work") {
		t.Fatalf("health names the assignments of a model deck has none of: %+v", h)
	}
	e.Must("deck.model_set", map[string]any{"connection": "router", "model": "x/voice", "name": "voice", "kind": "tts"}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "router", "model": "x/voice2", "name": "voice2", "kind": "tts"}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "tts", "models": []string{"voice", "voice2"}}, nil)
	e.Must("deck.assignment_set", map[string]any{"role": "tts", "project": "plain", "models": []string{"voice"}}, nil)
	e.Must("deck.model_delete", map[string]any{"name": "voice"}, nil)
	var m harness.ModelsOut
	e.Must("deck.models_list", nil, &m)
	for _, a := range m.Assignments {
		if a.Role == "tts" && (a.Project != "" || !slices.Equal(a.Models, []string{"voice2"})) {
			t.Fatalf("a removed model leaves its assignments, and one left with none goes: %+v", m.Assignments)
		}
	}
}

// TestAModelOfAConnectionIsAddedOnceAndItsPriceReads: a second name of the same model of a connection is refused,
// one with other options is its own model; a model of a local connection is free, one without a price unknown.
func TestAModelOfAConnectionIsAddedOnceAndItsPriceReads(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Must("deck.connection_set", map[string]any{"name": "api", "api": "openai", "url": "http://127.0.0.1:9/v1"}, nil)
	e.Must("deck.connection_set", map[string]any{"name": "box", "api": "openai", "url": "http://127.0.0.1:9/v1", "local": true}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "api", "model": "flash"}, nil)
	if code, body := e.Call("deck.model_set", map[string]any{"connection": "api", "model": "flash", "name": "api-flash"}); code < 400 ||
		!strings.Contains(string(body), "already model api/flash") {
		t.Fatalf("the same model of a connection under another name is refused, naming the one to change: %d %s", code, body)
	}
	e.Must("deck.model_set", map[string]any{"connection": "api", "model": "flash", "name": "flash-ru", "options": `{"language": "ru"}`}, nil)
	e.Must("deck.model_set", map[string]any{"connection": "box", "model": "qwen"}, nil)
	var m struct {
		Models []struct{ Name, Price string } `json:"models"`
	}
	e.Must("deck.models_list", nil, &m)
	price := map[string]string{}
	for _, v := range m.Models {
		price[v.Name] = v.Price
	}
	if price["api/flash"] != "unknown" || price["flash-ru"] != "unknown" || price["box/qwen"] != "free" {
		t.Fatalf("a model without a price reads unknown, one of a local connection free: %v", price)
	}
}
