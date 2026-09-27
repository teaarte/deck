package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
)

const colorManifest = `{"version": 5,
  "commands": [{"name": "work", "title": "Work", "description": "Asks the owner", "class": "internal",
    "result": {"type": "object", "required": ["color"], "properties": {"color": {"type": "string"}}},
    "executor": {"type": "agent", "prompt": "Ask the owner which color the report should use."}}]}`

// TestCodexCommandAgentWorksThroughTheGateway runs a command agent on Codex: it asks the owner with the ask tool
// of deck and hands its result in by the schema with result_submit, on the owner's login and nothing else of theirs.
func TestCodexCommandAgentWorksThroughTheGateway(t *testing.T) {
	c := newCodex(t)
	c.stream(1, "ask.jsonl")
	t.Setenv("OPENAI_API_KEY", "sk-deck-test")
	t.Setenv("CODEX_API_KEY", "sk-deck-test")
	t.Setenv("CODEX_HOME", t.TempDir())
	e, home := codexEnv(t, c)
	p := e.createProject("paint", colorManifest)
	id := e.run(p, "work")
	a := e.waitAsk()
	if a.Kind != "question" || !strings.Contains(a.Text, "Which color should the report use?") {
		t.Fatalf("the question of the agent reaches the owner: %+v", a)
	}
	e.answer(a.ID, "blue")
	got := e.waitState(id, "done", "error")
	cc := c.call(1)
	if got.State != "done" || string(got.Result) != `{"color":"blue"}` || cc.violation != "" {
		t.Fatalf("the result comes in by the schema with result_submit: %+v %s", got, cc.violation)
	}
	if len(cc.mcp) != 2 || !strings.Contains(cc.mcp[0]["text"].(string), "blue") || cc.mcp[1]["is_error"] != false {
		t.Fatalf("the agent got the owner's answer from ask and deck took its result: %v", cc.mcp)
	}
	for _, k := range []string{"OPENAI_API_KEY", "CODEX_API_KEY"} {
		if _, ok := cc.env[k]; ok {
			t.Fatalf("%s must not reach Codex", k)
		}
	}
	if cc.env["CODEX_HOME"] == "" || cc.env["HOME"] == "" {
		t.Fatalf("Codex runs on the owner's shared login: %v", cc.env)
	}
	work := filepath.Join(home, "runs", itoa(id), "work")
	fs, _ := cc.config("permissions.deck.filesystem")
	for _, want := range []string{quoteTOML(home) + `="deny"`, quoteTOML(cc.env["CODEX_HOME"]) + `="deny"`, quoteTOML(work) + `="write"`,
		quoteTOML(filepath.Join(home, "projects", itoa(p), "data")) + `="write"`, quoteTOML(filepath.Join(home, "runs", itoa(id), "results")) + `="write"`} {
		if !strings.Contains(fs, want) {
			t.Fatalf("the profile of the turn has %s: %s", want, fs)
		}
	}
	if v, _ := cc.config("web_search"); v != `"live"` {
		t.Fatalf("a project with network gets the web of Codex: %s", v)
	}
	if v, _ := cc.config("permissions.deck.network.enabled"); v != "true" {
		t.Fatalf("a project with network gets the network in its shell: %s", v)
	}
	for _, off := range []string{"features.plugins=false", "features.hooks=false", "features.apps=false", "agents.enabled=false", "notify=[]",
		"features.exec_permission_approvals=false"} {
		if !slices.Contains(cc.args, off) {
			t.Fatalf("the owner's %s stays out of the session: %v", off, cc.args)
		}
	}
	start := cc.request("thread/start")
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
	turn := cc.request("turn/start")
	var input []struct{ Text string }
	_ = json.Unmarshal(turn["input"], &input)
	if string(turn["permissions"]) != `"deck"` || turn["outputSchema"] != nil || len(input) != 1 ||
		!strings.Contains(input[0].Text, "result_submit") || !strings.Contains(input[0].Text, "mcp__deck__ask") {
		t.Fatalf("the turn runs under the profile of deck and hands its result in with result_submit: %v", turn)
	}
	var calls []map[string]any
	e.must("deck.llm_calls_list", map[string]any{"process": id}, &calls)
	if len(calls) != 4 || calls[0]["gen_ai.provider.name"] != "codex" || calls[0]["gen_ai.response.model"] != "gpt-5.6-sol" ||
		calls[0]["gen_ai.usage.input_tokens"].(float64) <= 0 {
		t.Fatalf("each model call of the turn goes to the journal with its tokens: %v", calls)
	}
}

func quoteTOML(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func TestCodexProjectWithoutNetworkHasNoWebAndNoNetwork(t *testing.T) {
	c := newCodex(t)
	c.stream(0, "ok.jsonl")
	e, _ := codexEnv(t, c)
	p := e.createProject("paint", strings.Replace(colorManifest, `"version": 5,`, `"version": 5, "no_network": true,`, 1))
	id := e.run(p, "work")
	got := e.waitState(id, "done", "error")
	cc := c.call(1)
	web, _ := cc.config("web_search")
	net, _ := cc.config("permissions.deck.network.enabled")
	if got.State != "done" || web != `"disabled"` || net != "false" {
		t.Fatalf("no web of Codex and no network in its shell: %+v web %s network %s", got, web, net)
	}
}

const outsideManifest = `{"version": 5,
  "commands": [{"name": "work", "title": "Work", "description": "Writes outside", "class": "internal",
    "result": {"type": "object", "required": ["written"], "properties": {"written": {"type": "boolean"}}},
    "executor": {"type": "agent", "prompt": "Write the word hi into a file outside your folder."}}]}`

// TestCodexEscalationWaitsForTheOwner hands what Codex would ask a person to deck as a confirmation of exactly
// that call: yes accepts it, no with a reason declines it and steers the reason into the turn.
func TestCodexEscalationWaitsForTheOwner(t *testing.T) {
	for _, tc := range []struct{ fixture, answer, decision string }{
		{"escalation.jsonl", "deny:not outside your folder", `"decline"`},
		{"escalation_allow.jsonl", "allow", `"accept"`},
	} {
		c := newCodex(t)
		c.stream(1, tc.fixture)
		e, _ := codexEnv(t, c)
		p := e.createProject("outside", outsideManifest)
		id := e.run(p, "work")
		a := e.waitAsk()
		if a.Kind != "confirm" || !strings.Contains(a.Text, "shell") || !strings.Contains(a.Text, "deck-codex-outside.txt") {
			t.Fatalf("%s: the escalation reaches the owner as a confirmation of the command: %+v", tc.fixture, a)
		}
		e.answer(a.ID, tc.answer)
		got := e.waitState(id, "done", "error")
		cc := c.call(1)
		var decision struct {
			Result struct{ Decision json.RawMessage }
		}
		if len(cc.answers) == 1 {
			_ = json.Unmarshal(mustJSON(cc.answers[0]), &decision)
		}
		steer := cc.request("turn/steer")
		if got.State != "done" || string(decision.Result.Decision) != tc.decision || cc.violation != "" {
			t.Fatalf("%s: codex gets %s: %+v %v %s", tc.fixture, tc.decision, got, cc.answers, cc.violation)
		}
		if tc.decision == `"decline"` && !strings.Contains(string(steer["input"]), "not outside your folder") {
			t.Fatalf("the reason of the owner is steered into the turn: %v", steer)
		}
		if tc.decision == `"accept"` && steer != nil {
			t.Fatalf("a yes steers nothing: %v", steer)
		}
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func TestCodexRetryResumesTheThreadOfTheSession(t *testing.T) {
	c := newCodex(t)
	c.stream(1, "nosubmit.jsonl")
	c.stream(2, "resume.jsonl")
	e, _ := codexEnv(t, c)
	p := e.createProject("paint", colorManifest)
	id := e.run(p, "work")
	got := e.waitState(id, "done", "error")
	first, second := c.call(1), c.call(2)
	resume := second.request("thread/resume")
	if got.State != "done" || string(got.Result) != `{"color":"blue"}` || string(resume["threadId"]) != `"01a0e033-872e-7e12-ad56-5fb4f01528ec"` {
		t.Fatalf("the retry resumes the thread Codex started for the session: %+v %v", got, resume)
	}
	if first.request("thread/resume") != nil || second.request("thread/start") != nil {
		t.Fatal("the first turn starts the thread, the retry resumes it")
	}
	var input []struct{ Text string }
	_ = json.Unmarshal(second.request("turn/start")["input"], &input)
	if len(input) != 1 || !strings.Contains(input[0].Text, "result_submit") {
		t.Fatalf("the retry asks for result_submit: %v", input)
	}
}

func TestCodexLostThreadStartsTheSessionOver(t *testing.T) {
	c := newCodex(t)
	c.stream(1, "nosubmit.jsonl")
	c.stream(2, "norollout.jsonl")
	c.stream(3, "ok.jsonl")
	e, _ := codexEnv(t, c)
	p := e.createProject("paint", colorManifest)
	id := e.run(p, "work")
	got := e.waitState(id, "done", "error")
	if got.State != "done" || c.calls() != 3 || c.call(3).request("thread/start") == nil {
		t.Fatalf("a thread Codex lost starts over: %+v, %d calls", got, c.calls())
	}
}

func TestCodexLostLoginAndLimit(t *testing.T) {
	for _, tc := range []struct{ fixture, state string }{
		{"nologin.jsonl", "needs_login"}, {"unauthorized.jsonl", "needs_login"}, {"ratelimit.jsonl", "rate_limited"},
	} {
		c := newCodex(t)
		c.stream(1, tc.fixture)
		e, _ := codexEnv(t, c)
		p := e.createProject("paint", colorManifest)
		id := e.run(p, "work")
		e.waitState(id, tc.state)
		if tc.state == "rate_limited" {
			pp, _ := e.App.Store.Q.GetProcess(t.Context(), id)
			if pp.PauseUntilMs.Int64 != 1790481864*1000 {
				t.Fatalf("the pause lasts until the spent window resets: %d", pp.PauseUntilMs.Int64)
			}
		}
	}
}

func TestCodexTurnSucceedsOnlyStrictly(t *testing.T) {
	for _, tc := range []struct{ fixture, want string }{
		{"two_completions.jsonl", "completions of one turn"}, {"nocompletion.jsonl", "without finishing the turn"},
	} {
		c := newCodex(t)
		c.stream(1, tc.fixture)
		e, _ := codexEnv(t, c)
		p := e.createProject("paint", colorManifest)
		id := e.run(p, "work")
		if got := e.waitState(id, "done", "error"); got.State != "error" || !strings.Contains(got.Error, tc.want) {
			t.Fatalf("%s: want an error with %q: %+v", tc.fixture, tc.want, got)
		}
	}
}

func TestCodexSubAgentEventsAreNotProcessEvents(t *testing.T) {
	c := newCodex(t)
	c.stream(1, "subagent.jsonl")
	e, _ := codexEnv(t, c)
	p := e.createProject("paint", colorManifest)
	id := e.run(p, "work")
	if got := e.waitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("a completed turn of another thread or an earlier turn does not end the turn: %+v", got)
	}
	var calls []map[string]any
	e.must("deck.llm_calls_list", map[string]any{"process": id}, &calls)
	for _, call := range calls {
		if call["gen_ai.usage.input_tokens"] == 800.0 || call["gen_ai.usage.input_tokens"] == 700.0 {
			t.Fatalf("model calls of another thread or an earlier turn are not the turn's: %v", calls)
		}
	}
	if len(calls) != 3 {
		t.Fatalf("only the calls of the thread count: %d", len(calls))
	}
}

func TestCodexThatDoesNotExitAfterItsTurnIsKilled(t *testing.T) {
	c := newCodex(t)
	c.stream(1, "hang.jsonl")
	e, _ := codexEnv(t, c)
	p := e.createProject("paint", colorManifest)
	start := time.Now()
	id := e.run(p, "work")
	if got := e.waitState(id, "done", "error"); got.State != "done" || time.Since(start) > 8*time.Second {
		t.Fatalf("an app-server that stays after its turn is killed and the turn counts: %+v in %s", got, time.Since(start))
	}
}

func TestFakeCodexRejectsWhatTheSchemaLacks(t *testing.T) {
	c := newCodex(t)
	c.stream(0, "ok.jsonl")
	for _, tc := range []struct {
		args  []string
		input string
		want  string
	}{
		{[]string{"app-server", "--full-auto"}, "", "unexpected argument"},
		{[]string{"app-server", "-c", "model=\"o3\""}, "", "unexpected argument"},
		{[]string{"app-server"}, `{"id":1,"method":"initialize","params":{"clientInfo":{"name":"x","version":"0"},"bogus":1}}` + "\n", "unknown field bogus"},
		{[]string{"app-server"}, `{"id":1,"method":"no/such","params":{}}` + "\n", "unknown method"},
		{[]string{"app-server"}, `{"id":1,"method":"initialize","params":{}}` + "\n", "do not match the schema"},
		{[]string{"app-server"}, `{"id":1,"method":"turn/start","params":{"threadId":7,"input":[]}}` + "\n", "do not match the schema"},
	} {
		cmd := exec.Command(filepath.Join(c.dir, "codex"), tc.args...)
		cmd.Stdin = strings.NewReader(tc.input)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), tc.want) {
			t.Fatalf("%v: want a refusal with %q: %v %s", tc.args, tc.want, err, out)
		}
	}
}

// TestFailedCodexSelfCheckHoldsCodexProcesses keeps processes on Codex waiting while its self-check is red; the
// main provider goes on, and the retried check on its timer lets them run.
func TestFailedCodexSelfCheckHoldsCodexProcesses(t *testing.T) {
	c := newCodex(t)
	c.stream(0, "ok.jsonl")
	home := t.TempDir()
	clock := &Clock{now: t0}
	fake := &Fake{script: func(ctx context.Context, call *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"color":"red"}`)}
	}}
	e := newEnvBuiltin(t, home, []provider.Provider{fake, c.adapter(home)}, withClock(clock))
	e.Clock = clock
	ctx := context.Background()
	e.App.Checks(ctx, e.Clock.Now())
	h := e.waitHealth("provider:codex", "error")
	if h.Version != "0.155.1" || !strings.Contains(h.Error, "self-check of 0.155.1 failed") {
		t.Fatalf("a failed self-check of Codex is red with the version and the reason: %+v", h)
	}
	on := e.createProject("paint", colorManifest)
	main := e.createProject("main", colorManifest)
	e.must("deck.provider_set", map[string]any{"command": "paint.work", "provider": "codex"}, nil)
	id := e.run(on, "work")
	e.waitState(id, "waiting_provider")
	if got := e.waitState(e.run(main, "work"), "done", "error"); got.State != "done" {
		t.Fatalf("the main provider goes on while Codex is red: %+v", got)
	}
	before := c.calls()
	c.stream(before+1, "selfcheck.jsonl")
	c.stream(before+2, "selfcheck_resume.jsonl")
	c.stream(before+3, "selfcheck_offline.jsonl")
	e.App.Checks(ctx, e.Clock.Add(10*time.Minute))
	e.settle()
	if c.calls() != before {
		t.Fatalf("a red self-check is retried by its timer, not sooner: %d calls", c.calls())
	}
	e.App.Checks(ctx, e.Clock.Add(21*time.Minute))
	if h := e.waitHealth("provider:codex", "ok"); h.Version != "0.155.1" {
		t.Fatalf("the retried self-check is green: %+v", h)
	}
	if got := e.waitState(id, "done", "error"); got.State != "done" || string(got.Result) != `{"color":"blue"}` {
		t.Fatalf("the held process runs on Codex once it is green: %+v", got)
	}
}

func TestCodexSelfCheckIsRedWhenAutoModeStopsWorking(t *testing.T) {
	for _, tc := range []struct {
		streams []string
		want    string
	}{
		{[]string{"selfcheck_asked.jsonl"}, "asked deck about a write in its own folder"},
		{[]string{"selfcheck_nousage.jsonl"}, "token counts"},
		{[]string{"selfcheck_leak.jsonl"}, "read a file deck hides"},
		{[]string{"selfcheck_noprobe.jsonl"}, "did not run"},
		{[]string{"selfcheck.jsonl", "selfcheck_resume.jsonl", "selfcheck_online.jsonl"}, "no network"},
		{[]string{"nologin.jsonl"}, "no valid login"},
	} {
		c := newCodex(t)
		for i, s := range tc.streams {
			c.stream(i+1, s)
		}
		e, _ := codexEnv(t, c)
		e.App.Checks(context.Background(), time.Now())
		status := "error"
		if tc.want == "no valid login" {
			status = "needs_login"
		}
		if h := e.waitHealth("provider:codex", status); !strings.Contains(h.Error, tc.want) {
			t.Fatalf("%v: the self-check is %s with %q: %+v", tc.streams, status, tc.want, h)
		}
	}
}

// TestCodexResumeAfterALateAnswerAsksForTheResultAgain parks a process whose question was not answered in time;
// the resume with the owner's answer asks for the whole result again, since each turn hands in its own.
func TestCodexResumeAfterALateAnswerAsksForTheResultAgain(t *testing.T) {
	c := newCodex(t)
	c.stream(1, "ask.jsonl")
	c.stream(2, "resume.jsonl")
	home := t.TempDir()
	e := newEnvWith(t, home, c.adapter(home), func(cfg *app.Config) { cfg.QuestionWait = 200 * time.Millisecond })
	p := e.createProject("paint", colorManifest)
	id := e.run(p, "work")
	e.waitState(id, "waiting_answer")
	if m := c.call(1).mcp; len(m) == 0 || m[0]["is_error"] != true || !strings.Contains(m[0]["text"].(string), "not answered yet") {
		t.Fatalf("an unanswered ask tells the agent to stop and wait: %v", m)
	}
	e.answer(e.waitAsk().ID, "blue")
	got := e.waitState(id, "done", "error")
	var input []struct{ Text string }
	_ = json.Unmarshal(c.call(2).request("turn/start")["input"], &input)
	if got.State != "done" || len(input) != 1 || !strings.Contains(input[0].Text, "blue") ||
		!strings.Contains(input[0].Text, "hand in the whole final result with the result_submit tool") {
		t.Fatalf("the resume carries the answer and asks for the result again: %+v %v", got, input)
	}
}

func TestCodexSessionWithoutItsThreadStartsOver(t *testing.T) {
	c := newCodex(t)
	c.stream(1, "ask.jsonl")
	c.stream(2, "ok.jsonl")
	c.stream(3, "resume.jsonl")
	home := t.TempDir()
	e := newEnvWith(t, home, c.adapter(home), func(cfg *app.Config) { cfg.QuestionWait = 200 * time.Millisecond })
	p := e.createProject("paint", colorManifest)
	id := e.run(p, "work")
	e.waitState(id, "waiting_answer")
	if err := os.RemoveAll(filepath.Join(home, "codex", "sessions")); err != nil {
		t.Fatal(err)
	}
	e.answer(e.waitAsk().ID, "blue")
	if got := e.waitState(id, "done", "error"); got.State != "done" || c.call(2).request("thread/start") == nil {
		t.Fatalf("a session whose thread deck no longer knows starts a new thread: %+v", got)
	}
}

func TestCodexNetworkEscalationInAProjectWithoutNetworkIsDeclined(t *testing.T) {
	c := newCodex(t)
	c.stream(1, "escalation_network.jsonl")
	e, _ := codexEnv(t, c)
	p := e.createProject("outside", strings.Replace(outsideManifest, `"version": 5,`, `"version": 5, "no_network": true,`, 1))
	id := e.run(p, "work")
	got := e.waitState(id, "done", "error")
	cc := c.call(1)
	var asks []askView
	_, out := e.call("deck.asks_list", nil)
	_ = json.Unmarshal(out, &asks)
	if got.State != "done" || len(asks) != 0 || len(cc.answers) != 1 || !strings.Contains(string(mustJSON(cc.answers[0])), `"decline"`) ||
		!strings.Contains(string(cc.request("turn/steer")["input"]), "no network") {
		t.Fatalf("network for a project without network is declined without asking the owner: %+v %v %v", got, asks, cc.answers)
	}
}

func TestCodexFileChangeConfirmationShowsTheChange(t *testing.T) {
	c := newCodex(t)
	raw, _ := os.ReadFile("testdata/codex/filechange.jsonl")
	lines := strings.SplitAfter(string(raw), "\n")
	ids := regexp.MustCompile(`"threadId": "[^"]+", "turnId": "[^"]+"`).FindString(lines[9])
	dynamic := `{"method": "item/started", "params": {"item": {"type": "dynamicToolCall", "id": "dyn1", "tool": "lookup", "arguments": {"q": "moon"}, "status": "inProgress"}, ` + ids + `}}` + "\n" +
		`{"method": "item/completed", "params": {"item": {"type": "dynamicToolCall", "id": "dyn1", "tool": "lookup", "arguments": {"q": "moon"}, "status": "failed"}, ` + ids + `}}` + "\n"
	c.streamText(1, strings.Join(lines[:9], "")+dynamic+strings.Join(lines[9:], ""))
	e, _ := codexEnv(t, c)
	p := e.createProject("outside", outsideManifest)
	id := e.run(p, "work")
	a := e.waitAsk()
	if l := e.log(id); !slices.ContainsFunc(l, func(x map[string]any) bool {
		return x["kind"] == "tool" && x["id"] == "dyn1" && x["title"] == "lookup" && strings.Contains(fmt.Sprint(x["text"]), "moon")
	}) || !slices.ContainsFunc(l, func(x map[string]any) bool {
		return x["kind"] == "tool_update" && x["id"] == "dyn1" && x["status"] == "failed"
	}) {
		t.Fatalf("other tool items of Codex are tool calls in the log too: %v", l)
	}
	if a.Kind != "confirm" || !strings.Contains(a.Text, "apply_patch") || !strings.Contains(a.Text, "/Users/owner/notes.txt") {
		t.Fatalf("the owner sees which file the patch changes: %+v", a)
	}
	if l := e.log(id); !slices.ContainsFunc(l, func(x map[string]any) bool {
		return x["kind"] == "tool" && x["title"] == "apply_patch" && x["id"] != nil && strings.Contains(fmt.Sprint(x["text"]), "notes.txt")
	}) {
		t.Fatalf("the log has the patch as a tool call with its id and file: %v", l)
	}
	e.answer(a.ID, "allow")
	got := e.waitState(id, "done", "error")
	if cc := c.call(1); got.State != "done" || len(cc.answers) != 1 || !strings.Contains(string(mustJSON(cc.answers[0])), `"accept"`) {
		t.Fatalf("yes accepts the patch: %+v %v", got, cc.answers)
	}
}

func TestCodexApprovalOfAnotherTurnIsDeclinedWithoutAsking(t *testing.T) {
	c := newCodex(t)
	c.stream(1, "escalation_staleturn.jsonl")
	e, _ := codexEnv(t, c)
	p := e.createProject("outside", outsideManifest)
	id := e.run(p, "work")
	got := e.waitState(id, "done", "error")
	var asks []askView
	_, out := e.call("deck.asks_list", nil)
	_ = json.Unmarshal(out, &asks)
	if cc := c.call(1); got.State != "done" || len(asks) != 0 || len(cc.answers) != 1 || !strings.Contains(string(mustJSON(cc.answers[0])), `"decline"`) {
		t.Fatalf("a request that is not of the running turn is declined without the owner: %+v %v %v", got, asks, cc.answers)
	}
}

func TestCodexOwnerServerNamedDeckIsAnError(t *testing.T) {
	c := newCodex(t)
	c.stream(1, "deckserver.jsonl")
	e, _ := codexEnv(t, c)
	p := e.createProject("paint", colorManifest)
	id := e.run(p, "work")
	if got := e.waitState(id, "done", "error"); got.State != "error" || !strings.Contains(got.Error, "MCP server named deck") {
		t.Fatalf("an MCP server of the owner named deck stops the turn with a clear error: %+v", got)
	}
}
