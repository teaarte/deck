package providers

import (
	"context"
	"deck2/internal/app"
	"deck2/internal/events"
	"deck2/internal/health"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestNoCloudCommandDoesNotTakeACloudProvider(t *testing.T) {
	t.Parallel()
	cloud, local := harness.NewACP(t), harness.NewACP(t)
	e := harness.New(t, harness.WithAgent(harness.Ok), harness.WithACPPair(cloud, local))
	e.SetACP(cloud, 1, "cloud", "goose", harness.AcpLLM(t), false)
	p := e.Project("notes", harness.NotesManifest)
	if code, body := e.Call("notes.collect", map[string]any{}); code == 200 || !strings.Contains(string(body), "no provider fits") ||
		!strings.Contains(string(body), "add a model that has it in Settings → Models") {
		t.Fatalf("with no provider on own hardware the command does not start, and the owner reads where to add one: %d %s", code, body)
	}
	if code, body := e.Call("deck.assignment_set", map[string]any{"role": "agent", "project": "notes", "command": "collect", "models": []string{"cloud"}}); code == 200 ||
		!strings.Contains(string(body), "no_cloud") {
		t.Fatalf("a cloud provider is not picked for a command that stays off the cloud: %d %s", code, body)
	}
	e.SetACP(local, 1, "local", "goose", harness.AcpLLM(t), true)
	var list []struct {
		Name, Model string
		Choices     []string
	}
	e.Must("deck.commands_list", map[string]any{"project": "notes"}, &list)
	if list[0].Name != "notes.collect" || list[0].Model != "local" || !slices.Equal(list[0].Choices, []string{"local"}) {
		t.Fatalf("only the provider on own hardware fits: %+v", list)
	}
	e.Must("deck.assignment_set", map[string]any{"role": "agent", "project": "notes", "models": []string{"cloud"}}, nil)
	e.Refused("notes.collect", map[string]any{}, 422, "in the settings of the module: local")
	e.UseModels("notes.collect", "local")
	local.StreamText(4, harness.AcpTurn(`{"written": 0}`))
	if got := e.WaitState(e.Run(p, "collect"), "done", "error"); got.State != "done" || local.Calls() != 4 {
		t.Fatalf("the command runs on the chosen provider: %+v", got)
	}
	e.SetACP(local, 5, "local", "goose", harness.AcpLLM(t), false)
	if code, body := e.Call("notes.collect", map[string]any{}); code == 200 || !strings.Contains(string(body), "no_cloud") {
		t.Fatalf("a chosen provider that moved to the cloud is refused at the start: %d %s", code, body)
	}
	if n := cloud.Calls(); n != 3 {
		t.Fatalf("nothing but its self-check reached the cloud provider: %d calls", n)
	}
}

func TestProviderThatLeftTheOwnersHardwareStopsTheNextTurn(t *testing.T) {
	t.Parallel()
	local := harness.NewACP(t)
	e := harness.New(t, harness.WithACPPair(local, harness.NewACP(t)))
	e.SetACP(local, 1, "acp", "goose", harness.AcpLLM(t), true)
	local.StreamText(4, harness.AcpTurn(`{"written": 1}`))
	id := e.Run(e.AcpProject("notes", harness.NotesManifest), "collect")
	e.WaitState(id, "done")
	e.SetACP(local, 5, "acp", "goose", harness.AcpLLM(t), false)
	e.Must("deck.process_rework", map[string]any{"process": id, "remark": "one more"}, nil)
	got := e.WaitState(id, "error", "done")
	if got.State != "error" || !strings.Contains(got.Error, "no longer fits") || local.Calls() != 7 {
		t.Fatalf("a session that must stay off the cloud does not go on once its provider moved there: %+v, %d calls", got, local.Calls())
	}
}

func TestProviderIsSwitchedWithoutARestart(t *testing.T) {
	t.Parallel()
	api := harness.NewACP(t)
	e := harness.New(t, harness.WithAgent(func(context.Context, *harness.Call) provider.Result {
		return provider.Result{Text: "from the main provider"}
	}), harness.WithACPPair(api, harness.NewACP(t)))
	api.StreamText(0, harness.AcpTurn(`"from the API"`))
	e.SetACP(api, 1, "api", "goose", harness.AcpLLM(t), false)
	p := e.Project("plain", harness.PlainManifest)
	if got := e.WaitState(e.Run(p, "work"), "done"); string(got.Result) != `"from the main provider"` {
		t.Fatalf("without a setting the main provider runs: %+v", got)
	}
	e.UseModels("plain.work", "api")
	if got := e.WaitState(e.Run(p, "work"), "done"); string(got.Result) != `"from the API"` {
		t.Fatalf("the setting takes effect at the next start: %+v", got)
	}
	var list []struct{ Name, Model string }
	e.Must("deck.commands_list", map[string]any{"project": "plain"}, &list)
	e.UseModels("plain.work")
	if got := e.WaitState(e.Run(p, "work"), "done"); list[0].Model != "api" || string(got.Result) != `"from the main provider"` {
		t.Fatalf("an empty setting goes back to the default: %+v %+v", list, got)
	}
	if code, _ := e.Call("deck.assignment_set", map[string]any{"role": "agent", "project": "plain", "command": "nothing", "models": []string{"api"}}); code == 200 {
		t.Fatal("only agent commands take a provider")
	}
	suggests := strings.Replace(harness.PlainManifest, `"class": "internal",`, `"class": "internal", "provider": "api",`, 1)
	s := e.Project("suggests", suggests)
	if got := e.WaitState(e.Run(s, "work"), "done"); string(got.Result) != `"from the API"` {
		t.Fatalf("without a setting the provider the manifest suggests runs: %+v", got)
	}
}

func TestProviderWithoutAKeySendsNone(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk-from-the-environment-of-deck")
	local := harness.NewACP(t)
	e := harness.New(t, harness.WithACPPair(local, harness.NewACP(t)))
	local.SelfCheck()
	local.StreamText(4, harness.AcpTurn(`"ok"`))
	llm := harness.AcpLLM(t)
	e.MustAgentModel(map[string]any{"name": "acp", "url": llm.URL, "model": "m", "agent": "goose", "no_cloud": true})
	e.Until("the self-check", func() bool {
		h, err := health.Get(t.Context(), e.App.Store.Q, health.Of{}, "provider:acp")
		return err == nil && h.Status == "ok"
	})
	e.WaitState(e.Run(e.AcpProject("plain", harness.PlainManifest), "work"), "done")
	for _, r := range llm.Requests() {
		if r.Auth != "" {
			t.Fatalf("deck's own OPENAI_API_KEY never reaches a provider: %q", r.Auth)
		}
	}
}

func TestResultSubmitGetsOneRebuild(t *testing.T) {
	t.Parallel()
	local := harness.NewACP(t)
	e := harness.New(t, harness.WithACPPair(local, harness.NewACP(t)))
	e.SetACP(local, 1, "acp", "goose", harness.AcpLLM(t), true)
	local.StreamText(4, harness.AcpStream(harness.AcpInit, `{"reply": "session/new", "result": {"sessionId": "s-1"}}`, `{"reply": "session/prompt"}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"written": "one"}}}}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"written": "still one"}}}}`,
		`{"mcp": {"tool": "result_submit", "arguments": {"result": {"written": 3}}}}`, `{"end": "end_turn"}`))
	got := e.WaitState(e.Run(e.AcpProject("notes", harness.NotesManifest), "collect"), "done", "error")
	if got.State != "done" || !got.NotBySchema || string(got.Result) != `{"written":"still one"}` {
		t.Fatalf("one rebuild, then the result stays marked as off the schema and nothing more is taken: %+v", got)
	}
}

func TestProviderURLCarriesNoKey(t *testing.T) {
	t.Parallel()
	f := harness.NewACP(t)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}), harness.WithConfig(

		func(c *app.Config) { c.ACPBinaries = map[string]string{"goose": filepath.Join(f.Dir, "goose")} }))

	for _, u := range []string{"https://me:sk-1@openrouter.example/api/v1", "https://api.example/v1?key=sk-1"} {
		code, out := e.AgentModel(map[string]any{"name": "cloud", "url": u, "model": "m", "agent": "goose"})
		if code == 200 || !strings.Contains(string(out), "give the key in key") {
			t.Fatalf("a connection URL with a key in it is refused and points to the key field: %s → %d %s", u, code, out)
		}
	}
	e.MustAgentModel(map[string]any{"name": "cloud", "url": "https://api.example/v1", "model": "m", "key_secret": "k", "agent": "goose"})
	// A connection an older deck stored with the key in its URL, which a save now refuses.
	e.SQL(`INSERT INTO connection (workspace_id, name, url) VALUES (1, 'old', 'https://api.example/v1?key=sk-stored');
		INSERT INTO model (workspace_id, connection_id, name, model, agent) SELECT 1, id, 'old', 'm', 'goose' FROM connection WHERE name = 'old'`)
	var list map[string]any
	e.Must("deck.models_list", nil, &list)
	if strings.Contains(fmt.Sprint(list), "sk-stored") {
		t.Fatalf("a provider stored with a key in its url is neither shown nor used: %v", list)
	}
	srv := harness.Manifest(`{"tools": {"mcp": {"servers": {"far": {"url": "https://mcp.example/mcp?key=sk-1"}}}}}`)
	if code, out := e.Call("deck.project_create", map[string]any{"name": "far", "manifest": srv}); code == 200 || !strings.Contains(string(out), "secret") {
		t.Fatalf("an MCP server url with a key in it is refused: %d %s", code, out)
	}
}

// TestTheModelColumnFollowsAssignmentSet: a model assigned to a command is heard live and redraws the
// commands of the project's settings.
func TestTheModelColumnFollowsAssignmentSet(t *testing.T) {
	t.Parallel()
	api := harness.NewACP(t)
	e := harness.New(t, harness.WithACPPair(api, harness.NewACP(t)))
	e.SetACP(api, 1, "api", "goose", harness.AcpLLM(t), false)
	e.Project("plain", harness.PlainManifest)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phone := e.Events(ctx)
	e.UseModels("plain.work", "api")
	harness.WaitEvent(t, phone, events.Event{Topic: "assignments"})
	drawn, _ := json.Marshal(e.ScreenMessages("plain", "settings", map[string]any{"topic": "assignments", "changed": "plain"}))
	if !strings.Contains(string(drawn), `"cells":["Work","api","here","unknown",""]`) {
		t.Fatalf("the commands of the project are read again with the provider picked: %s", drawn)
	}
}

// TestTheCommandsOfAProjectOfferOnlyProvidersThatFit: the commands of a project list only the providers an
// assignment of the command would take.
func TestTheCommandsOfAProjectOfferOnlyProvidersThatFit(t *testing.T) {
	t.Parallel()
	cloud, local := harness.NewACP(t), harness.NewACP(t)
	e := harness.New(t, harness.WithAgent(harness.Ok), harness.WithACPPair(cloud, local))
	e.SetACP(cloud, 1, "cloud", "goose", harness.AcpLLM(t), false)
	e.SetACP(local, 1, "local", "goose", harness.AcpLLM(t), true)
	e.Project("notes", harness.NotesManifest)
	var rows []struct {
		Name    string   `json:"name"`
		Model   string   `json:"model"`
		Choices []string `json:"choices"`
	}
	e.Must("deck.commands_list", map[string]any{"project": "notes"}, &rows)
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
	e := harness.New(t, harness.WithAgent(harness.Ok))
	e.Project("plain", harness.PlainManifest)
	var list []struct{ Name, Model string }
	e.Must("deck.commands_list", map[string]any{"project": "plain"}, &list)
	if len(list) != 1 || list[0].Model == "" || list[0].Model == "quiet" {
		t.Fatalf("the command runs on a model deck picks: %+v", list)
	}
	ran := list[0].Model
	e.Must("deck.assignment_set", map[string]any{"role": "agent", "project": "plain", "command": "work", "model": "quiet"}, nil)
	var m struct {
		Assignments []struct {
			Project, Command string
			Models           []string
		}
	}
	e.Must("deck.models_list", nil, &m)
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
