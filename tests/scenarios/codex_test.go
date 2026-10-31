package scenarios

import (
	"context"
	deckhome "deck2/internal/home"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
)

const colorManifest = `{"version": 13,
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
	e := New(t, WithCodex(c))
	home := e.Home
	withSkills := strings.Replace(colorManifest, `"type": "agent",`, `"type": "agent", "skills": ["module-builder", "module-conventions"],`, 1)
	p := e.Project("paint", withSkills)
	id := e.Run(p, "work")
	a := e.WaitAsk()
	if a.Kind != "question" || !strings.Contains(a.Text, "Which color should the report use?") {
		t.Fatalf("the question of the agent reaches the owner: %+v", a)
	}
	var skillRoots []string
	_ = json.Unmarshal(c.call(1).request("skills/extraRoots/set")["extraRoots"], &skillRoots)
	if len(skillRoots) != 1 || !strings.HasPrefix(skillRoots[0], deckhome.Layout(home).Work()+string(os.PathSeparator)) {
		t.Fatalf("the skills are outside deck's closed state: %v", skillRoots)
	}
	for _, name := range []string{"module-builder", "module-conventions"} {
		text, err := os.ReadFile(filepath.Join(skillRoots[0], name, "SKILL.md"))
		if err != nil || len(text) == 0 {
			t.Fatalf("the turn receives the skill %s: %v", name, err)
		}
	}
	permissions, _ := c.call(1).config("permissions.deck.filesystem")
	if !strings.Contains(permissions, quoteTOML(skillRoots[0])+`="read"`) {
		t.Fatalf("the skills are read-only: %s", permissions)
	}
	e.Answer(a.ID, "blue")
	got := e.WaitState(id, "done", "error")
	cc := c.call(1)
	if got.State != "done" || string(got.Result) != `{"color":"blue"}` || cc.violation != "" {
		t.Fatalf("the result comes in by the schema with result_submit: %+v %s", got, cc.violation)
	}
	if _, err := os.Stat(skillRoots[0]); !os.IsNotExist(err) {
		t.Fatalf("the skills of the finished turn are removed: %v", err)
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
	work := filepath.Join(deckhome.Layout(home).Work(), "runs", itoa(id), "work")
	fs, _ := cc.config("permissions.deck.filesystem")
	user, _ := os.UserHomeDir()
	data := filepath.Join(deckhome.Layout(home).Work(), "projects", itoa(p), "data")
	shell, _ := cc.config("shell_environment_policy.set")
	if strings.Contains(fs, quoteTOML(home)+`=`) || !strings.Contains(shell, "XDG_CACHE_HOME="+quoteTOML(filepath.Join(data, ".cache"))) {
		t.Fatalf("deck's folder stays open above a run, so its paths resolve, and caches go to the data folder: %s %s", fs, shell)
	}
	codexPrivate := cc.env["CODEX_HOME"]
	if runtime.GOOS == "linux" {
		codexPrivate = filepath.Join(codexPrivate, "{[!t]*,t[!m]*,tm[!p]*,tmp?*,t,tm}")
	}
	for _, want := range []string{quoteTOML(deckhome.Layout(home).State()) + `="deny"`, quoteTOML(codexPrivate) + `="deny"`, quoteTOML(work) + `="write"`,
		quoteTOML(filepath.Join(user, ".ssh")) + `="deny"`, quoteTOML(filepath.Join(user, ".claude")) + `="deny"`,
		quoteTOML(filepath.Join(deckhome.Layout(home).Work(), "projects", itoa(p), "data")) + `="write"`, quoteTOML(filepath.Join(deckhome.Layout(home).Work(), "runs", itoa(id), "results")) + `="write"`} {
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
	e.modelCalls("", id, &calls)
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
	t.Parallel()
	c := newCodex(t)
	c.stream(0, "ok.jsonl")
	e := New(t, WithCodex(c))
	p := e.Project("paint", strings.Replace(colorManifest, `"version": 13,`, `"version": 13, "no_network": true,`, 1))
	id := e.Run(p, "work")
	got := e.WaitState(id, "done", "error")
	cc := c.call(1)
	web, _ := cc.config("web_search")
	net, _ := cc.config("permissions.deck.network.enabled")
	if got.State != "done" || web != `"disabled"` || net != "false" {
		t.Fatalf("no web of Codex and no network in its shell: %+v web %s network %s", got, web, net)
	}
}

const outsideManifest = `{"version": 13,
  "commands": [{"name": "work", "title": "Work", "description": "Writes outside", "class": "internal",
    "result": {"type": "object", "required": ["written"], "properties": {"written": {"type": "boolean"}}},
    "executor": {"type": "agent", "prompt": "Write the word hi into a file outside your folder."}}]}`

// TestCodexEscalationWaitsForTheOwner hands what Codex would ask a person to deck as a confirmation of exactly
// that call: yes accepts it, no with a reason declines it and steers the reason into the turn.
func TestCodexEscalationWaitsForTheOwner(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ fixture, answer, decision string }{
		{"escalation.jsonl", "deny:not outside your folder", `"decline"`},
		{"escalation_allow.jsonl", "allow", `"accept"`},
	} {
		c := newCodex(t)
		c.stream(1, tc.fixture)
		e := New(t, WithCodex(c))
		p := e.Project("outside", outsideManifest)
		id := e.Run(p, "work")
		a := e.WaitAsk()
		if a.Kind != "confirm" || !strings.Contains(a.Text, "shell") || !strings.Contains(a.Text, "deck-codex-outside.txt") {
			t.Fatalf("%s: the escalation reaches the owner as a confirmation of the command: %+v", tc.fixture, a)
		}
		e.Answer(a.ID, tc.answer)
		got := e.WaitState(id, "done", "error")
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
	t.Parallel()
	c := newCodex(t)
	c.stream(1, "nosubmit.jsonl")
	c.stream(2, "resume.jsonl")
	e := New(t, WithCodex(c))
	p := e.Project("paint", colorManifest)
	id := e.Run(p, "work")
	got := e.WaitState(id, "done", "error")
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
	t.Parallel()
	c := newCodex(t)
	c.stream(1, "nosubmit.jsonl")
	c.stream(2, "norollout.jsonl")
	c.stream(3, "ok.jsonl")
	e := New(t, WithCodex(c))
	p := e.Project("paint", colorManifest)
	id := e.Run(p, "work")
	got := e.WaitState(id, "done", "error")
	if got.State != "done" || c.calls() != 3 || c.call(3).request("thread/start") == nil {
		t.Fatalf("a thread Codex lost starts over: %+v, %d calls", got, c.calls())
	}
}

func TestCodexLostLoginAndLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ fixture, state string }{
		{"nologin.jsonl", "needs_login"}, {"unauthorized.jsonl", "needs_login"}, {"ratelimit.jsonl", "rate_limited"},
	} {
		c := newCodex(t)
		c.stream(1, tc.fixture)
		e := New(t, WithCodex(c))
		p := e.Project("paint", colorManifest)
		id := e.Run(p, "work")
		e.WaitState(id, tc.state)
		if tc.state == "rate_limited" {
			pp, _ := e.App.Store.Q.GetProcess(t.Context(), id)
			if pp.PauseUntilMs.Int64 != 1790481864*1000 {
				t.Fatalf("the pause lasts until the spent window resets: %d", pp.PauseUntilMs.Int64)
			}
		}
	}
}

// TestCodexOutOfCreditsEndsItsTurn: a limit codex reports reached mid-turn, while it waits without ending the turn,
// ends the turn as the model's limit at once instead of leaving the process hanging.
func TestCodexOutOfCreditsEndsItsTurn(t *testing.T) {
	t.Parallel()
	c := newCodex(t)
	c.stream(1, "credits.jsonl")
	e := New(t, WithCodex(c))
	id := e.Run(e.Project("paint", colorManifest), "work")
	if got := e.WaitState(id, "rate_limited", "error", "done"); got.State != "rate_limited" {
		t.Fatalf("the process waits for the limit, its turn ended: %+v", got)
	}
	if h := e.WaitHealth("provider:codex", "paused"); !strings.Contains(h.Error, "workspace_member_credits_depleted") {
		t.Fatalf("health says which limit: %+v", h)
	}
}

func TestCodexTurnSucceedsOnlyStrictly(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ fixture, want string }{
		{"two_completions.jsonl", "completions of one turn"}, {"nocompletion.jsonl", "without finishing the turn"},
	} {
		c := newCodex(t)
		c.stream(1, tc.fixture)
		e := New(t, WithCodex(c))
		p := e.Project("paint", colorManifest)
		id := e.Run(p, "work")
		if got := e.WaitState(id, "done", "error"); got.State != "error" || !strings.Contains(got.Error, tc.want) {
			t.Fatalf("%s: want an error with %q: %+v", tc.fixture, tc.want, got)
		}
	}
}

func TestCodexSubAgentEventsAreNotProcessEvents(t *testing.T) {
	t.Parallel()
	c := newCodex(t)
	c.stream(1, "subagent.jsonl")
	e := New(t, WithCodex(c))
	p := e.Project("paint", colorManifest)
	id := e.Run(p, "work")
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("a completed turn of another thread or an earlier turn does not end the turn: %+v", got)
	}
	var calls []map[string]any
	e.modelCalls("", id, &calls)
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
	t.Parallel()
	c := newCodex(t)
	c.stream(1, "hang.jsonl")
	e := New(t, WithCodex(c))
	p := e.Project("paint", colorManifest)
	start := time.Now()
	id := e.Run(p, "work")
	if got := e.WaitState(id, "done", "error"); got.State != "done" || time.Since(start) > 8*time.Second {
		t.Fatalf("an app-server that stays after its turn is killed and the turn counts: %+v in %s", got, time.Since(start))
	}
}

// TestCodexThatClosesItsOutputButStaysIsKilled: an app-server that ends its output, stays and does not take a TERM is
// killed with its group, and the turn counts.
func TestCodexThatClosesItsOutputButStaysIsKilled(t *testing.T) {
	t.Parallel()
	c := newCodex(t)
	raw, err := os.ReadFile(Testdata("codex/hang.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	c.streamText(1, strings.Replace(string(raw), `{"hang": true}`, `{"deaf": true}`, 1))
	e := New(t, WithCodex(c))
	p := e.Project("paint", colorManifest)
	start := time.Now()
	id := e.Run(p, "work")
	if got := e.WaitState(id, "done", "error"); got.State != "done" || time.Since(start) > 15*time.Second {
		t.Fatalf("the app-server is killed and the turn counts: %+v in %s", got, time.Since(start))
	}
}

// TestCodexLimitReachedAfterItsTurnLeavesTheResult: a limit the last call of a completed turn reached does not undo
// the turn's result.
func TestCodexLimitReachedAfterItsTurnLeavesTheResult(t *testing.T) {
	t.Parallel()
	c := newCodex(t)
	c.stream(1, "spent_after.jsonl")
	e := New(t, WithCodex(c))
	if got := e.WaitState(e.Run(e.Project("paint", colorManifest), "work"), "done", "error", "rate_limited"); got.State != "done" {
		t.Fatalf("the completed turn counts: %+v", got)
	}
}

func TestFakeCodexRejectsWhatTheSchemaLacks(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	c := newCodex(t)
	c.stream(0, "ok.jsonl")
	home := t.TempDir()
	clock := &Clock{now: t0}
	fake := &Fake{script: func(ctx context.Context, call *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"color":"red"}`)}
	}}
	e := New(t, WithHome(home), WithProvider(fake, c.adapter(home)), WithClock(clock))
	ctx := context.Background()
	e.App.Providers.Checks(ctx, e.Clock.Now())
	h := e.WaitHealth("provider:codex", "error")
	if h.Version != "0.155.1" || !strings.Contains(h.Error, "self-check of 0.155.1 failed") {
		t.Fatalf("a failed self-check of Codex is red with the version and the reason: %+v", h)
	}
	on := e.Project("paint", colorManifest)
	main := e.Project("main", colorManifest)
	e.UseModels("paint.work", "codex")
	id := e.Run(on, "work")
	e.WaitState(id, "waiting_provider")
	if got := e.WaitState(e.Run(main, "work"), "done", "error"); got.State != "done" {
		t.Fatalf("the main provider goes on while Codex is red: %+v", got)
	}
	before := c.calls()
	c.stream(before+1, "selfcheck.jsonl")
	c.stream(before+2, "selfcheck_resume.jsonl")
	c.stream(before+3, "selfcheck_offline.jsonl")
	e.App.Providers.Checks(ctx, e.Clock.Add(10*time.Minute))
	e.Settle()
	if c.calls() != before {
		t.Fatalf("a red self-check is retried by its timer, not sooner: %d calls", c.calls())
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
		c := newCodex(t)
		for i, s := range tc.streams {
			c.stream(i+1, s)
		}
		e := New(t, WithCodex(c))
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

// TestCodexResumeAfterALateAnswerAsksForTheResultAgain parks a process whose question was not answered in time;
// the resume with the owner's answer asks for the whole result again, since each turn hands in its own.
func TestCodexResumeAfterALateAnswerAsksForTheResultAgain(t *testing.T) {
	t.Parallel()
	c := newCodex(t)
	c.stream(1, "ask.jsonl")
	c.stream(2, "resume.jsonl")
	home := t.TempDir()
	e := New(t, WithHome(home), WithProvider(c.adapter(home)), WithConfig(func(cfg *app.Config) { cfg.QuestionWait = 200 * time.Millisecond }))
	p := e.Project("paint", colorManifest)
	id := e.Run(p, "work")
	e.WaitState(id, "waiting_answer")
	if m := c.call(1).mcp; len(m) == 0 || m[0]["is_error"] != true || !strings.Contains(m[0]["text"].(string), "not answered yet") {
		t.Fatalf("an unanswered ask tells the agent to stop and wait: %v", m)
	}
	e.Answer(e.WaitAsk().ID, "blue")
	got := e.WaitState(id, "done", "error")
	var input []struct{ Text string }
	_ = json.Unmarshal(c.call(2).request("turn/start")["input"], &input)
	if got.State != "done" || len(input) != 1 || !strings.Contains(input[0].Text, "blue") ||
		!strings.Contains(input[0].Text, "hand in the whole final result with the result_submit tool") {
		t.Fatalf("the resume carries the answer and asks for the result again: %+v %v", got, input)
	}
}

func TestCodexSessionWithoutItsThreadStartsOver(t *testing.T) {
	t.Parallel()
	c := newCodex(t)
	c.stream(1, "ask.jsonl")
	c.stream(2, "ok.jsonl")
	c.stream(3, "resume.jsonl")
	home := t.TempDir()
	e := New(t, WithHome(home), WithProvider(c.adapter(home)), WithConfig(func(cfg *app.Config) { cfg.QuestionWait = 200 * time.Millisecond }))
	p := e.Project("paint", colorManifest)
	id := e.Run(p, "work")
	e.WaitState(id, "waiting_answer")
	if err := os.RemoveAll(filepath.Join(deckhome.Layout(home).State(), "codex", "sessions")); err != nil {
		t.Fatal(err)
	}
	e.Answer(e.WaitAsk().ID, "blue")
	if got := e.WaitState(id, "done", "error"); got.State != "done" || c.call(2).request("thread/start") == nil {
		t.Fatalf("a session whose thread deck no longer knows starts a new thread: %+v", got)
	}
}

func TestCodexNetworkEscalationInAProjectWithoutNetworkIsDeclined(t *testing.T) {
	t.Parallel()
	c := newCodex(t)
	c.stream(1, "escalation_network.jsonl")
	e := New(t, WithCodex(c))
	p := e.Project("outside", strings.Replace(outsideManifest, `"version": 13,`, `"version": 13, "no_network": true,`, 1))
	id := e.Run(p, "work")
	got := e.WaitState(id, "done", "error")
	cc := c.call(1)
	var asks []Ask
	_, out := e.Call("deck.asks_list", nil)
	_ = json.Unmarshal(out, &asks)
	if got.State != "done" || len(asks) != 0 || len(cc.answers) != 1 || !strings.Contains(string(mustJSON(cc.answers[0])), `"decline"`) ||
		!strings.Contains(string(cc.request("turn/steer")["input"]), "no network") {
		t.Fatalf("network for a project without network is declined without asking the owner: %+v %v %v", got, asks, cc.answers)
	}
}

func TestCodexFileChangeConfirmationShowsTheChange(t *testing.T) {
	t.Parallel()
	c := newCodex(t)
	raw, _ := os.ReadFile(Testdata("codex/filechange.jsonl"))
	lines := strings.SplitAfter(string(raw), "\n")
	ids := regexp.MustCompile(`"threadId": "[^"]+", "turnId": "[^"]+"`).FindString(lines[9])
	dynamic := `{"method": "item/started", "params": {"item": {"type": "dynamicToolCall", "id": "dyn1", "tool": "lookup", "arguments": {"q": "moon"}, "status": "inProgress"}, ` + ids + `}}` + "\n" +
		`{"method": "item/completed", "params": {"item": {"type": "dynamicToolCall", "id": "dyn1", "tool": "lookup", "arguments": {"q": "moon"}, "status": "failed"}, ` + ids + `}}` + "\n"
	c.streamText(1, strings.Join(lines[:9], "")+dynamic+strings.Join(lines[9:], ""))
	e := New(t, WithCodex(c))
	p := e.Project("outside", outsideManifest)
	id := e.Run(p, "work")
	a := e.WaitAsk()
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
	e.Answer(a.ID, "allow")
	got := e.WaitState(id, "done", "error")
	if cc := c.call(1); got.State != "done" || len(cc.answers) != 1 || !strings.Contains(string(mustJSON(cc.answers[0])), `"accept"`) {
		t.Fatalf("yes accepts the patch: %+v %v", got, cc.answers)
	}
}

func TestCodexApprovalOfAnotherTurnIsDeclinedWithoutAsking(t *testing.T) {
	t.Parallel()
	c := newCodex(t)
	c.stream(1, "escalation_staleturn.jsonl")
	e := New(t, WithCodex(c))
	p := e.Project("outside", outsideManifest)
	id := e.Run(p, "work")
	got := e.WaitState(id, "done", "error")
	var asks []Ask
	_, out := e.Call("deck.asks_list", nil)
	_ = json.Unmarshal(out, &asks)
	if cc := c.call(1); got.State != "done" || len(asks) != 0 || len(cc.answers) != 1 || !strings.Contains(string(mustJSON(cc.answers[0])), `"decline"`) {
		t.Fatalf("a request that is not of the running turn is declined without the owner: %+v %v %v", got, asks, cc.answers)
	}
}

func TestCodexOwnerServerNamedDeckIsAnError(t *testing.T) {
	t.Parallel()
	c := newCodex(t)
	c.stream(1, "deckserver.jsonl")
	e := New(t, WithCodex(c))
	p := e.Project("paint", colorManifest)
	id := e.Run(p, "work")
	if got := e.WaitState(id, "done", "error"); got.State != "error" || !strings.Contains(got.Error, "MCP server named deck") {
		t.Fatalf("an MCP server of the owner named deck stops the turn with a clear error: %+v", got)
	}
}

func TestCodexTurnGoesOnWhenTheAppServerStopsReadingWhileItWrites(t *testing.T) {
	t.Parallel()
	c := newCodex(t)
	b, _ := os.ReadFile(Testdata("codex/ok.jsonl"))
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	var out []string
	for _, l := range lines {
		if strings.HasPrefix(l, `{"reply": "initialize"`) {
			out = append(out, `{"deafAfter": "thread/start"}`)
		}
		out = append(out, l)
		if strings.HasPrefix(l, `{"reply": "thread/start"`) {
			out = append(out, `{"flood": 6000}`)
		}
	}
	c.streamText(0, strings.Join(out, "\n")+"\n")
	e := New(t, WithCodex(c))
	m := strings.Replace(colorManifest, "Ask the owner which color the report should use.", strings.Repeat("Pick blue. ", 30000), 1)
	p := e.Project("paint", m)
	if got := e.WaitState(e.Run(p, "work"), "done", "error"); got.State != "done" || string(got.Result) != `{"color":"blue"}` {
		t.Fatalf("deck reads the output while its long input waits for the app-server: %+v", got)
	}
}

func TestTheFinalAnswerOfCodexIsTheTextOfItsTurn(t *testing.T) {
	t.Parallel()
	c := newCodex(t)
	c.stream(0, "reply.jsonl")
	e := New(t, WithCodex(c))
	topic := e.say(0, "We are out of oat milk.")
	if said := e.masterSaid(topic, 1); said[0].Text != "Put it into groceries." {
		t.Fatalf("the final answer of a Codex turn is its text: %+v", said)
	}
}

// TestCodexShellHasTheNetworkOrNone: the shell of Codex has the whole network, as the owner's, so its local ports
// work; a list of hosts went through the proxy of Codex, which closed them. A project without network has none.
func TestCodexShellHasTheNetworkOrNone(t *testing.T) {
	t.Parallel()
	c := newCodex(t)
	c.stream(0, "ok.jsonl")
	c.stream(1, "ok.jsonl")
	e := New(t, WithCodex(c))
	listed := e.Project("paint", strings.Replace(colorManifest, `"version": 13,`, `"version": 13, "domains": {"read": ["data.example.org"]},`, 1))
	e.WaitState(e.Run(listed, "work"), "done", "error")
	cc := c.call(1)
	mode, _ := cc.config("permissions.deck.network.mode")
	proxy, _ := cc.config("features.network_proxy")
	net, _ := cc.config("permissions.deck.network.enabled")
	if mode != "" || proxy != "" || net != "true" {
		t.Fatalf("the shell has the whole network without the proxy of Codex: %s %s %s", mode, proxy, net)
	}
	closed := e.Project("closed", strings.Replace(colorManifest, `"version": 13,`, `"version": 13, "no_network": true,`, 1))
	e.WaitState(e.Run(closed, "work"), "done", "error")
	if net, _ := c.call(2).config("permissions.deck.network.enabled"); net != "false" {
		t.Fatalf("a project without network gives the shell none: %s", net)
	}
}

// TestCodexIsNotOfferedACommandThatNeedsALocalBrowser: a command whose shell starts a local browser does not run on
// Codex, whose sandbox cannot, and a project without network cannot require one.
func TestCodexIsNotOfferedACommandThatNeedsALocalBrowser(t *testing.T) {
	c := newCodex(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	e := New(t, WithCodex(c))
	browser := strings.Replace(colorManifest, `"class": "internal",`, `"class": "internal", "requires": ["local_browser"],`, 1)
	e.Project("paint", browser)
	e.Refused("paint.work", map[string]any{}, 422, "local_browser")
	if code, out := e.Call("deck.project_create", map[string]any{"name": "quiet", "manifest": strings.Replace(browser, `"version": 13,`, `"version": 13, "no_network": true,`, 1)}); code != 422 || !strings.Contains(string(out), "requires local_browser") {
		t.Fatalf("a project without network cannot require a local browser: %d %s", code, out)
	}
}
