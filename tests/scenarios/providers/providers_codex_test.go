package providers

import (
	"context"
	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

// TestCodexCommandAgentWorksThroughTheGateway runs a command agent on Codex: it asks the owner with the ask tool
// of deck and hands its result in by the schema with result_submit, on the owner's login and nothing else of theirs.
func TestCodexCommandAgentWorksThroughTheGateway(t *testing.T) {
	c := harness.NewCodex(t)
	c.Stream(1, "ask.jsonl")
	t.Setenv("OPENAI_API_KEY", "sk-deck-test")
	t.Setenv("CODEX_API_KEY", "sk-deck-test")
	t.Setenv("CODEX_HOME", t.TempDir())
	e := harness.New(t, harness.WithCodex(c))
	home := e.Home
	withSkills := strings.Replace(harness.ColorManifest, `"type": "agent",`, `"type": "agent", "skills": ["module-builder", "module-conventions"],`, 1)
	p := e.Project("paint", withSkills)
	id := e.Run(p, "work")
	a := e.WaitAsk()
	if a.Kind != "question" || !strings.Contains(a.Text, "Which color should the report use?") {
		t.Fatalf("the question of the agent reaches the owner: %+v", a)
	}
	var skillRoots []string
	_ = json.Unmarshal(c.Call(1).Request("skills/extraRoots/set")["extraRoots"], &skillRoots)
	if len(skillRoots) != 1 || !strings.HasPrefix(skillRoots[0], deckhome.Layout(home).Work()+string(os.PathSeparator)) {
		t.Fatalf("the skills are outside deck's closed state: %v", skillRoots)
	}
	for _, name := range []string{"module-builder", "module-conventions"} {
		text, err := os.ReadFile(filepath.Join(skillRoots[0], name, "SKILL.md"))
		if err != nil || len(text) == 0 {
			t.Fatalf("the turn receives the skill %s: %v", name, err)
		}
	}
	permissions, _ := c.Call(1).Config("permissions.deck.filesystem")
	if !strings.Contains(permissions, harness.QuoteTOML(skillRoots[0])+`="read"`) {
		t.Fatalf("the skills are read-only: %s", permissions)
	}
	e.Answer(a.ID, "blue")
	got := e.WaitState(id, "done", "error")
	cc := c.Call(1)
	if got.State != "done" || string(got.Result) != `{"color":"blue"}` || cc.Violation != "" {
		t.Fatalf("the result comes in by the schema with result_submit: %+v %s", got, cc.Violation)
	}
	if _, err := os.Stat(skillRoots[0]); !os.IsNotExist(err) {
		t.Fatalf("the skills of the finished turn are removed: %v", err)
	}
	if len(cc.Mcp) != 2 || !strings.Contains(cc.Mcp[0]["text"].(string), "blue") || cc.Mcp[1]["is_error"] != false {
		t.Fatalf("the agent got the owner's answer from ask and deck took its result: %v", cc.Mcp)
	}
	for _, k := range []string{"OPENAI_API_KEY", "CODEX_API_KEY"} {
		if _, ok := cc.Env[k]; ok {
			t.Fatalf("%s must not reach Codex", k)
		}
	}
	if cc.Env["CODEX_HOME"] == "" || cc.Env["HOME"] == "" {
		t.Fatalf("Codex runs on the owner's shared login: %v", cc.Env)
	}
	work := filepath.Join(deckhome.Layout(home).Work(), "runs", harness.Itoa(id), "work")
	fs, _ := cc.Config("permissions.deck.filesystem")
	user, _ := os.UserHomeDir()
	data := filepath.Join(deckhome.Layout(home).Work(), "projects", harness.Itoa(p), "data")
	shell, _ := cc.Config("shell_environment_policy.set")
	if strings.Contains(fs, harness.QuoteTOML(home)+`=`) || !strings.Contains(shell, "XDG_CACHE_HOME="+harness.QuoteTOML(filepath.Join(data, ".cache"))) {
		t.Fatalf("deck's folder stays open above a run, so its paths resolve, and caches go to the data folder: %s %s", fs, shell)
	}
	codexPrivate := cc.Env["CODEX_HOME"]
	if runtime.GOOS == "linux" {
		codexPrivate = filepath.Join(codexPrivate, "{[!t]*,t[!m]*,tm[!p]*,tmp?*,t,tm}")
	}
	for _, want := range []string{harness.QuoteTOML(deckhome.Layout(home).State()) + `="deny"`, harness.QuoteTOML(codexPrivate) + `="deny"`, harness.QuoteTOML(work) + `="write"`,
		harness.QuoteTOML(filepath.Join(user, ".ssh")) + `="deny"`, harness.QuoteTOML(filepath.Join(user, ".claude")) + `="deny"`,
		harness.QuoteTOML(filepath.Join(deckhome.Layout(home).Work(), "projects", harness.Itoa(p), "data")) + `="write"`, harness.QuoteTOML(filepath.Join(deckhome.Layout(home).Work(), "runs", harness.Itoa(id), "results")) + `="write"`} {
		if !strings.Contains(fs, want) {
			t.Fatalf("the profile of the turn has %s: %s", want, fs)
		}
	}
	if v, _ := cc.Config("web_search"); v != `"live"` {
		t.Fatalf("a project with network gets the web of Codex: %s", v)
	}
	if v, _ := cc.Config("permissions.deck.network.enabled"); v != "true" {
		t.Fatalf("a project with network gets the network in its shell: %s", v)
	}
	for _, off := range []string{"features.plugins=false", "features.hooks=false", "features.apps=false", "agents.enabled=false", "notify=[]",
		"features.exec_permission_approvals=false"} {
		if !slices.Contains(cc.Args, off) {
			t.Fatalf("the owner's %s stays out of the session: %v", off, cc.Args)
		}
	}
	start := cc.Request("thread/start")
	var cfg struct {
		MCPServers map[string]map[string]any `json:"mcp_servers"`
		Skills     struct {
			Config []map[string]any `json:"config"`
		} `json:"skills"`
	}
	_ = json.Unmarshal(start["config"], &cfg)
	deck := cfg.MCPServers["deck"]
	if cfg.MCPServers["loom"]["enabled"] != false || !strings.HasPrefix(deck["url"].(string), e.URL+"/mcp/") ||
		deck["default_tools_approval_mode"] != "approve" || !slices.Contains(deck["disabled_tools"].([]any), any("approve")) {
		t.Fatalf("the thread has the deck gateway and none of the owner's MCP servers: %v", cfg.MCPServers)
	}
	if len(cfg.Skills.Config) != 1 || cfg.Skills.Config[0]["enabled"] != false || !strings.Contains(cfg.Skills.Config[0]["path"].(string), "owner-skill") {
		t.Fatalf("the owner's skills are off in the thread: %v", cfg.Skills.Config)
	}
	if string(start["approvalPolicy"]) != `"on-request"` {
		t.Fatalf("the thread runs on-request: %s", start["approvalPolicy"])
	}
	turn := cc.Request("turn/start")
	var input []struct{ Text string }
	_ = json.Unmarshal(turn["input"], &input)
	if string(turn["permissions"]) != `"deck"` || turn["outputSchema"] != nil || len(input) != 1 ||
		!strings.Contains(input[0].Text, "result_submit") || !strings.Contains(input[0].Text, "mcp__deck__ask") {
		t.Fatalf("the turn runs under the profile of deck and hands its result in with result_submit: %v", turn)
	}
	var calls []map[string]any
	e.ModelCalls("", id, &calls)
	if len(calls) != 4 || calls[0]["gen_ai.provider.name"] != "codex" || calls[0]["gen_ai.response.model"] != "gpt-5.6-sol" ||
		calls[0]["gen_ai.usage.input_tokens"].(float64) <= 0 {
		t.Fatalf("each model call of the turn goes to the journal with its tokens: %v", calls)
	}
}

func TestCodexProjectWithoutNetworkHasNoWebAndNoNetwork(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	c.Stream(0, "ok.jsonl")
	e := harness.New(t, harness.WithCodex(c))
	p := e.Project("paint", harness.ManifestWith(harness.ColorManifest, `"no_network": true`))
	id := e.Run(p, "work")
	got := e.WaitState(id, "done", "error")
	cc := c.Call(1)
	web, _ := cc.Config("web_search")
	net, _ := cc.Config("permissions.deck.network.enabled")
	if got.State != "done" || web != `"disabled"` || net != "false" {
		t.Fatalf("no web of Codex and no network in its shell: %+v web %s network %s", got, web, net)
	}
}

var outsideManifest = harness.Manifest(`{"commands": [{"name": "work", "title": "Work", "description": "Writes outside", "class": "internal",
    "result": {"type": "object", "required": ["written"], "properties": {"written": {"type": "boolean"}}},
    "executor": {"type": "agent", "prompt": "Write the word hi into a file outside your folder."}}]}`)

// TestCodexEscalationWaitsForTheOwner hands what Codex would ask a person to deck as a confirmation of exactly
// that call: yes accepts it, no with a reason declines it and steers the reason into the turn.
func TestCodexEscalationWaitsForTheOwner(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ fixture, answer, decision string }{
		{"escalation.jsonl", "deny:not outside your folder", `"decline"`},
		{"escalation_allow.jsonl", "allow", `"accept"`},
	} {
		c := harness.NewCodex(t)
		c.Stream(1, tc.fixture)
		e := harness.New(t, harness.WithCodex(c))
		p := e.Project("outside", outsideManifest)
		id := e.Run(p, "work")
		a := e.WaitAsk()
		if a.Kind != "confirm" || !strings.Contains(a.Text, "shell") || !strings.Contains(a.Text, "deck-codex-outside.txt") {
			t.Fatalf("%s: the escalation reaches the owner as a confirmation of the command: %+v", tc.fixture, a)
		}
		e.Answer(a.ID, tc.answer)
		got := e.WaitState(id, "done", "error")
		cc := c.Call(1)
		var decision struct {
			Result struct{ Decision json.RawMessage }
		}
		if len(cc.Answers) == 1 {
			_ = json.Unmarshal(harness.MustJSON(cc.Answers[0]), &decision)
		}
		steer := cc.Request("turn/steer")
		if got.State != "done" || string(decision.Result.Decision) != tc.decision || cc.Violation != "" {
			t.Fatalf("%s: codex gets %s: %+v %v %s", tc.fixture, tc.decision, got, cc.Answers, cc.Violation)
		}
		if tc.decision == `"decline"` && !strings.Contains(string(steer["input"]), "not outside your folder") {
			t.Fatalf("the reason of the owner is steered into the turn: %v", steer)
		}
		if tc.decision == `"accept"` && steer != nil {
			t.Fatalf("a yes steers nothing: %v", steer)
		}
	}
}

// TestFailedCodexSelfCheckHoldsCodexProcesses keeps processes on Codex waiting while its self-check is red; the
// main provider goes on, and the retried check on its timer lets them run.
func TestFailedCodexSelfCheckHoldsCodexProcesses(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	c.Stream(0, "ok.jsonl")
	home := t.TempDir()
	clock := &harness.Clock{At: harness.T0}
	fake := &harness.Fake{Script: func(ctx context.Context, call *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"color":"red"}`)}
	}}
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(fake, c.Adapter(home)), harness.WithClock(clock))
	ctx := context.Background()
	e.App.Providers.Checks(ctx, e.Clock.Now())
	h := e.WaitHealth("provider:codex", "error")
	if h.Version != "0.155.1" || !strings.Contains(h.Error, "self-check of 0.155.1 failed") {
		t.Fatalf("a failed self-check of Codex is red with the version and the reason: %+v", h)
	}
	on := e.Project("paint", harness.ColorManifest)
	main := e.Project("main", harness.ColorManifest)
	e.UseModels("paint.work", "codex")
	id := e.Run(on, "work")
	e.WaitState(id, "waiting_provider")
	if got := e.WaitState(e.Run(main, "work"), "done", "error"); got.State != "done" {
		t.Fatalf("the main provider goes on while Codex is red: %+v", got)
	}
	before := c.Calls()
	c.Stream(before+1, "selfcheck.jsonl")
	c.Stream(before+2, "selfcheck_resume.jsonl")
	c.Stream(before+3, "selfcheck_offline.jsonl")
	e.App.Providers.Checks(ctx, e.Clock.Add(10*time.Minute))
	e.Settle()
	if c.Calls() != before {
		t.Fatalf("a red self-check is retried by its timer, not sooner: %d calls", c.Calls())
	}
	e.App.Providers.Checks(ctx, e.Clock.Add(21*time.Minute))
	if h := e.WaitHealth("provider:codex", "ok"); h.Version != "0.155.1" {
		t.Fatalf("the retried self-check is green: %+v", h)
	}
	if got := e.WaitState(id, "done", "error"); got.State != "done" || string(got.Result) != `{"color":"blue"}` {
		t.Fatalf("the held process runs on Codex once it is green: %+v", got)
	}
}

func TestCodexSelfCheckIsRedWhenAutoModeStopsWorking(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		streams []string
		want    string
	}{
		{[]string{"selfcheck_asked.jsonl"}, "asked deck about a write in its own folder"},
		{[]string{"selfcheck_nousage.jsonl"}, "token counts"},
		{[]string{"selfcheck_leak.jsonl"}, "read a file deck hides"},
		{[]string{"selfcheck_noprobe.jsonl"}, "did not run"},
		{[]string{"selfcheck.jsonl", "selfcheck_resume.jsonl", "selfcheck_online.jsonl"}, "turn without network"},
		{[]string{"nologin.jsonl"}, "no valid login"},
	} {
		c := harness.NewCodex(t)
		for i, s := range tc.streams {
			c.Stream(i+1, s)
		}
		e := harness.New(t, harness.WithCodex(c))
		e.App.Providers.Checks(context.Background(), time.Now())
		status := "error"
		if tc.want == "no valid login" {
			status = "needs_login"
		}
		if h := e.WaitHealth("provider:codex", status); !strings.Contains(h.Error, tc.want) {
			t.Fatalf("%v: the self-check is %s with %q: %+v", tc.streams, status, tc.want, h)
		}
	}
}

func TestCodexNetworkEscalationInAProjectWithoutNetworkIsDeclined(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	c.Stream(1, "escalation_network.jsonl")
	e := harness.New(t, harness.WithCodex(c))
	p := e.Project("outside", harness.ManifestWith(outsideManifest, `"no_network": true`))
	id := e.Run(p, "work")
	got := e.WaitState(id, "done", "error")
	cc := c.Call(1)
	var asks []harness.Ask
	_, out := e.Call("deck.asks_list", nil)
	_ = json.Unmarshal(out, &asks)
	if got.State != "done" || len(asks) != 0 || len(cc.Answers) != 1 || !strings.Contains(string(harness.MustJSON(cc.Answers[0])), `"decline"`) ||
		!strings.Contains(string(cc.Request("turn/steer")["input"]), "no network") {
		t.Fatalf("network for a project without network is declined without asking the owner: %+v %v %v", got, asks, cc.Answers)
	}
}

func TestCodexFileChangeConfirmationShowsTheChange(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	raw, _ := os.ReadFile(harness.Testdata("codex/filechange.jsonl"))
	lines := strings.SplitAfter(string(raw), "\n")
	ids := regexp.MustCompile(`"threadId": "[^"]+", "turnId": "[^"]+"`).FindString(lines[9])
	dynamic := `{"method": "item/started", "params": {"item": {"type": "dynamicToolCall", "id": "dyn1", "tool": "lookup", "arguments": {"q": "moon"}, "status": "inProgress"}, ` + ids + `}}` + "\n" +
		`{"method": "item/completed", "params": {"item": {"type": "dynamicToolCall", "id": "dyn1", "tool": "lookup", "arguments": {"q": "moon"}, "status": "failed"}, ` + ids + `}}` + "\n"
	c.StreamText(1, strings.Join(lines[:9], "")+dynamic+strings.Join(lines[9:], ""))
	e := harness.New(t, harness.WithCodex(c))
	p := e.Project("outside", outsideManifest)
	id := e.Run(p, "work")
	a := e.WaitAsk()
	if l := e.Log(id); !slices.ContainsFunc(l, func(x map[string]any) bool {
		return x["kind"] == "tool" && x["id"] == "dyn1" && x["title"] == "lookup" && strings.Contains(fmt.Sprint(x["text"]), "moon")
	}) || !slices.ContainsFunc(l, func(x map[string]any) bool {
		return x["kind"] == "tool_update" && x["id"] == "dyn1" && x["status"] == "failed"
	}) {
		t.Fatalf("other tool items of Codex are tool calls in the log too: %v", l)
	}
	if a.Kind != "confirm" || !strings.Contains(a.Text, "apply_patch") || !strings.Contains(a.Text, "/Users/owner/notes.txt") {
		t.Fatalf("the owner sees which file the patch changes: %+v", a)
	}
	if l := e.Log(id); !slices.ContainsFunc(l, func(x map[string]any) bool {
		return x["kind"] == "tool" && x["title"] == "apply_patch" && x["id"] != nil && strings.Contains(fmt.Sprint(x["text"]), "notes.txt")
	}) {
		t.Fatalf("the log has the patch as a tool call with its id and file: %v", l)
	}
	e.Answer(a.ID, "allow")
	got := e.WaitState(id, "done", "error")
	if cc := c.Call(1); got.State != "done" || len(cc.Answers) != 1 || !strings.Contains(string(harness.MustJSON(cc.Answers[0])), `"accept"`) {
		t.Fatalf("yes accepts the patch: %+v %v", got, cc.Answers)
	}
}

// TestCodexShellHasTheNetworkOrNone: the shell of Codex has the whole network, as the owner's, so its local ports
// work; a list of hosts went through the proxy of Codex, which closed them. A project without network has none.
func TestCodexShellHasTheNetworkOrNone(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	c.Stream(0, "ok.jsonl")
	c.Stream(1, "ok.jsonl")
	e := harness.New(t, harness.WithCodex(c))
	listed := e.Project("paint", harness.ManifestWith(harness.ColorManifest, `"domains": {"read": ["data.example.org"]}`))
	e.WaitState(e.Run(listed, "work"), "done", "error")
	cc := c.Call(1)
	mode, _ := cc.Config("permissions.deck.network.mode")
	proxy, _ := cc.Config("features.network_proxy")
	net, _ := cc.Config("permissions.deck.network.enabled")
	if mode != "" || proxy != "" || net != "true" {
		t.Fatalf("the shell has the whole network without the proxy of Codex: %s %s %s", mode, proxy, net)
	}
	closed := e.Project("closed", harness.ManifestWith(harness.ColorManifest, `"no_network": true`))
	e.WaitState(e.Run(closed, "work"), "done", "error")
	if net, _ := c.Call(2).Config("permissions.deck.network.enabled"); net != "false" {
		t.Fatalf("a project without network gives the shell none: %s", net)
	}
}

// TestCodexIsNotOfferedACommandThatNeedsALocalBrowser: a command whose shell starts a local browser does not run on
// Codex, whose sandbox cannot, and a project without network cannot require one.
func TestCodexIsNotOfferedACommandThatNeedsALocalBrowser(t *testing.T) {
	c := harness.NewCodex(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	e := harness.New(t, harness.WithCodex(c))
	browser := strings.Replace(harness.ColorManifest, `"class": "internal",`, `"class": "internal", "requires": ["local_browser"],`, 1)
	e.Project("paint", browser)
	e.Refused("paint.work", map[string]any{}, 422, "local_browser")
	if code, out := e.Call("deck.project_create", map[string]any{"name": "quiet", "manifest": harness.ManifestWith(browser, `"no_network": true`)}); code != 422 || !strings.Contains(string(out), "requires local_browser") {
		t.Fatalf("a project without network cannot require a local browser: %d %s", code, out)
	}
}
