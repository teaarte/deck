package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"deck2/internal/app"
	"deck2/internal/provider"
)

const notesManifest = `{"version": 12,
  "collections": [{"name": "notes", "description": "Notes", "key": "title", "display": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}}, "required": ["title"]}}],
  "tools": {"http": [{"name": "post", "description": "Posts a note", "method": "POST", "url": "https://example.invalid/post",
    "class": "external", "body": {"text": "{text}"}, "input": {"type": "object", "properties": {"text": {"type": "string"}}, "required": ["text"]}}]},
  "commands": [{"name": "collect", "title": "Collect", "description": "Collects notes.", "class": "internal", "requires": ["no_cloud"],
    "executor": {"type": "agent", "prompt": "Collect notes."},
    "result": {"type": "object", "properties": {"written": {"type": "integer"}}, "required": ["written"]}}]}`

func TestNoCloudCommandDoesNotTakeACloudProvider(t *testing.T) {
	t.Parallel()
	e, cloud, local := acpPair(t, ok)
	e.addACP(cloud, 1, "cloud", "goose", acpLLM(t), false)
	p := e.createProject("notes", notesManifest)
	if code, body := e.call("notes.collect", map[string]any{}); code == 200 || !strings.Contains(string(body), "no provider fits") ||
		!strings.Contains(string(body), "add a model that has it in Settings → Models") {
		t.Fatalf("with no provider on own hardware the command does not start, and the owner reads where to add one: %d %s", code, body)
	}
	if code, body := e.call("deck.assignment_set", map[string]any{"role": "agent", "project": "notes", "command": "collect", "models": []string{"cloud"}}); code == 200 ||
		!strings.Contains(string(body), "no_cloud") {
		t.Fatalf("a cloud provider is not picked for a command that stays off the cloud: %d %s", code, body)
	}
	e.addACP(local, 1, "local", "goose", acpLLM(t), true)
	var list []struct {
		Name, Model string
		Choices     []string
	}
	e.must("deck.commands_list", map[string]any{"project": "notes"}, &list)
	if list[0].Name != "notes.collect" || list[0].Model != "local" || !slices.Equal(list[0].Choices, []string{"local"}) {
		t.Fatalf("only the provider on own hardware fits: %+v", list)
	}
	e.useModels("notes.collect", "local")
	local.streamText(4, acpTurn(`{"written": 0}`))
	if got := e.waitState(e.run(p, "collect"), "done", "error"); got.State != "done" || local.calls() != 4 {
		t.Fatalf("the command runs on the chosen provider: %+v", got)
	}
	e.addACP(local, 5, "local", "goose", acpLLM(t), false)
	if code, body := e.call("notes.collect", map[string]any{}); code == 200 || !strings.Contains(string(body), "no_cloud") {
		t.Fatalf("a chosen provider that moved to the cloud is refused at the start: %d %s", code, body)
	}
	if n := cloud.calls(); n != 3 {
		t.Fatalf("nothing but its self-check reached the cloud provider: %d calls", n)
	}
}

func TestProviderThatLeftTheOwnersHardwareStopsTheNextTurn(t *testing.T) {
	t.Parallel()
	e, local, _ := acpPair(t, nil)
	e.addACP(local, 1, "acp", "goose", acpLLM(t), true)
	local.streamText(4, acpTurn(`{"written": 1}`))
	id := e.run(e.acpProject("notes", notesManifest), "collect")
	e.waitState(id, "done")
	e.addACP(local, 5, "acp", "goose", acpLLM(t), false)
	e.must("deck.process_rework", map[string]any{"process": id, "remark": "one more"}, nil)
	got := e.waitState(id, "error", "done")
	if got.State != "error" || !strings.Contains(got.Error, "no longer fits") || local.calls() != 7 {
		t.Fatalf("a session that must stay off the cloud does not go on once its provider moved there: %+v, %d calls", got, local.calls())
	}
}

func TestProviderIsSwitchedWithoutARestart(t *testing.T) {
	t.Parallel()
	e, api, _ := acpPair(t, func(context.Context, *Call) provider.Result { return provider.Result{Text: "from the main provider"} })
	api.streamText(0, acpTurn(`"from the API"`))
	e.addACP(api, 1, "api", "goose", acpLLM(t), false)
	p := e.createProject("plain", plainManifest)
	if got := e.waitState(e.run(p, "work"), "done"); string(got.Result) != `"from the main provider"` {
		t.Fatalf("without a setting the main provider runs: %+v", got)
	}
	e.useModels("plain.work", "api")
	if got := e.waitState(e.run(p, "work"), "done"); string(got.Result) != `"from the API"` {
		t.Fatalf("the setting takes effect at the next start: %+v", got)
	}
	var list []struct{ Name, Model string }
	e.must("deck.commands_list", map[string]any{"project": "plain"}, &list)
	e.useModels("plain.work")
	if got := e.waitState(e.run(p, "work"), "done"); list[0].Model != "api" || string(got.Result) != `"from the main provider"` {
		t.Fatalf("an empty setting goes back to the default: %+v %+v", list, got)
	}
	if code, _ := e.call("deck.assignment_set", map[string]any{"role": "agent", "project": "plain", "command": "nothing", "models": []string{"api"}}); code == 200 {
		t.Fatal("only agent commands take a provider")
	}
	suggests := strings.Replace(plainManifest, `"class": "internal",`, `"class": "internal", "provider": "api",`, 1)
	s := e.createProject("suggests", suggests)
	if got := e.waitState(e.run(s, "work"), "done"); string(got.Result) != `"from the API"` {
		t.Fatalf("without a setting the provider the manifest suggests runs: %+v", got)
	}
}

func TestProviderWithoutAKeySendsNone(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-from-the-environment-of-deck")
	e, local, _ := acpPair(t, nil)
	local.selfCheck()
	local.streamText(4, acpTurn(`"ok"`))
	llm := acpLLM(t)
	e.mustAgentModel(map[string]any{"name": "acp", "url": llm.URL, "model": "m", "agent": "goose", "no_cloud": true})
	e.waitFor("the self-check", func() bool {
		h, err := e.App.Store.Q.GetHealth(t.Context(), "provider:acp")
		return err == nil && h.Status == "ok"
	})
	e.waitState(e.run(e.acpProject("plain", plainManifest), "work"), "done")
	for _, r := range llm.requests() {
		if r.Auth != "" {
			t.Fatalf("deck's own OPENAI_API_KEY never reaches a provider: %q", r.Auth)
		}
	}
}

func TestResultSubmitGetsOneRebuild(t *testing.T) {
	t.Parallel()
	e, local, _ := acpPair(t, nil)
	e.addACP(local, 1, "acp", "goose", acpLLM(t), true)
	local.streamText(4, acpStream(acpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"written": "one"}}}}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"written": "still one"}}}}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"written": 3}}}}`, `{"end": "end_turn"}`))
	got := e.waitState(e.run(e.acpProject("notes", notesManifest), "collect"), "done", "error")
	if got.State != "done" || !got.NotBySchema || string(got.Result) != `{"written":"still one"}` {
		t.Fatalf("one rebuild, then the result stays marked as off the schema and nothing more is taken: %+v", got)
	}
}

func TestProviderURLCarriesNoKey(t *testing.T) {
	t.Parallel()
	f := newACP(t)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}, func(c *app.Config) { c.ACPBinaries = map[string]string{"goose": filepath.Join(f.dir, "goose")} })
	for _, u := range []string{"https://me:sk-1@openrouter.example/api/v1", "https://api.example/v1?key=sk-1"} {
		code, out := e.agentModel(map[string]any{"name": "cloud", "url": u, "model": "m", "agent": "goose"})
		if code == 200 || !strings.Contains(string(out), "give the key in key") {
			t.Fatalf("a connection URL with a key in it is refused and points to the key field: %s → %d %s", u, code, out)
		}
	}
	e.mustAgentModel(map[string]any{"name": "cloud", "url": "https://api.example/v1", "model": "m", "key_secret": "k", "agent": "goose"})
	_, err := e.App.Store.DB.Exec(`INSERT INTO connection (workspace_id, name, url) VALUES (1, 'old', 'https://api.example/v1?key=sk-stored');
		INSERT INTO model (workspace_id, connection_id, name, model, agent) SELECT 1, id, 'old', 'm', 'goose' FROM connection WHERE name = 'old'`)
	if err != nil {
		t.Fatal(err)
	}
	var list map[string]any
	e.must("deck.models_list", nil, &list)
	if strings.Contains(fmt.Sprint(list), "sk-stored") {
		t.Fatalf("a provider stored with a key in its url is neither shown nor used: %v", list)
	}
	srv := `{"version": 12, "tools": {"mcp": {"servers": {"far": {"url": "https://mcp.example/mcp?key=sk-1"}}}}}`
	if code, out := e.call("deck.project_create", map[string]any{"name": "far", "manifest": srv}); code == 200 || !strings.Contains(string(out), "secret") {
		t.Fatalf("an MCP server url with a key in it is refused: %d %s", code, out)
	}
}

// TestTheModelColumnFollowsAssignmentSet: a model assigned to a command is heard live and redraws the
// commands of the project's settings.
func TestTheModelColumnFollowsAssignmentSet(t *testing.T) {
	t.Parallel()
	e, api, _ := acpPair(t, nil)
	e.addACP(api, 1, "api", "goose", acpLLM(t), false)
	e.createProject("plain", plainManifest)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phone := e.sse(ctx)
	e.useModels("plain.work", "api")
	waitTopic(t, phone, "assignments")
	drawn, _ := json.Marshal(e.screenMessages("plain", "settings", map[string]any{"topic": "assignments", "changed": "plain"}))
	if !strings.Contains(string(drawn), `"cells":["Work","api","here",""]`) {
		t.Fatalf("the commands of the project are read again with the provider picked: %s", drawn)
	}
}

// TestTheCommandsOfAProjectOfferOnlyProvidersThatFit: the commands of a project list only the providers an
// assignment of the command would take.
func TestTheCommandsOfAProjectOfferOnlyProvidersThatFit(t *testing.T) {
	t.Parallel()
	e, cloud, local := acpPair(t, ok)
	e.addACP(cloud, 1, "cloud", "goose", acpLLM(t), false)
	e.addACP(local, 1, "local", "goose", acpLLM(t), true)
	e.createProject("notes", notesManifest)
	var rows []struct {
		Name    string   `json:"name"`
		Model   string   `json:"model"`
		Choices []string `json:"choices"`
	}
	e.must("deck.commands_list", map[string]any{"project": "notes"}, &rows)
	for _, r := range rows {
		if r.Name == "notes.collect" {
			if r.Model != "local" || !slices.Equal(r.Choices, []string{"local"}) {
				t.Fatalf("only the provider on own hardware is offered: %+v", r)
			}
			return
		}
	}
	t.Fatalf("no notes.collect: %+v", rows)
}

// TestAPickedModelKeepsTheOneTheCommandRanOnAsItsFallback: a command with no assignment of its own runs on the model
// deck picks; a model picked in its row goes first and that one stays after it.
func TestAPickedModelKeepsTheOneTheCommandRanOnAsItsFallback(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	e.createProject("plain", plainManifest)
	var list []struct{ Name, Model string }
	e.must("deck.commands_list", map[string]any{"project": "plain"}, &list)
	if len(list) != 1 || list[0].Model == "" || list[0].Model == "quiet" {
		t.Fatalf("the command runs on a model deck picks: %+v", list)
	}
	ran := list[0].Model
	e.must("deck.assignment_set", map[string]any{"role": "agent", "project": "plain", "command": "work", "model": "quiet"}, nil)
	var m struct {
		Assignments []struct {
			Project, Command string
			Models           []string
		}
	}
	e.must("deck.models_list", nil, &m)
	got := ""
	for _, a := range m.Assignments {
		if a.Project == "plain" && a.Command == "work" {
			got = fmt.Sprint(a.Models)
		}
	}
	if got != "[quiet "+ran+"]" {
		t.Fatalf("the picked model goes first and the one it ran on stays its fallback: %+v", m.Assignments)
	}
}
