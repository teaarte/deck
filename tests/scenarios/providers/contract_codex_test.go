package providers

import (
	"deck2/internal/app"
	deckhome "deck2/internal/home"
	"deck2/tests/harness"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexRetryResumesTheThreadOfTheSession(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	c.Stream(1, "nosubmit.jsonl")
	c.Stream(2, "resume.jsonl")
	e := harness.New(t, harness.WithCodex(c))
	p := e.Project("paint", harness.ColorManifest)
	id := e.Run(p, "work")
	got := e.WaitState(id, "done", "error")
	first, second := c.Call(1), c.Call(2)
	resume := second.Request("thread/resume")
	if got.State != "done" || string(got.Result) != `{"color":"blue"}` || string(resume["threadId"]) != `"01a0e033-872e-7e12-ad56-5fb4f01528ec"` {
		t.Fatalf("the retry resumes the thread Codex started for the session: %+v %v", got, resume)
	}
	if first.Request("thread/resume") != nil || second.Request("thread/start") != nil {
		t.Fatal("the first turn starts the thread, the retry resumes it")
	}
	var input []struct{ Text string }
	_ = json.Unmarshal(second.Request("turn/start")["input"], &input)
	if len(input) != 1 || !strings.Contains(input[0].Text, "result_submit") {
		t.Fatalf("the retry asks for result_submit: %v", input)
	}
}

func TestCodexLostThreadStartsTheSessionOver(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	c.Stream(1, "nosubmit.jsonl")
	c.Stream(2, "norollout.jsonl")
	c.Stream(3, "ok.jsonl")
	e := harness.New(t, harness.WithCodex(c))
	p := e.Project("paint", harness.ColorManifest)
	id := e.Run(p, "work")
	got := e.WaitState(id, "done", "error")
	if got.State != "done" || c.Calls() != 3 || c.Call(3).Request("thread/start") == nil {
		t.Fatalf("a thread Codex lost starts over: %+v, %d calls", got, c.Calls())
	}
}

func TestCodexLostLoginAndLimit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ fixture, state string }{
		{"nologin.jsonl", "needs_login"}, {"unauthorized.jsonl", "needs_login"}, {"ratelimit.jsonl", "rate_limited"},
	} {
		c := harness.NewCodex(t)
		c.Stream(1, tc.fixture)
		e := harness.New(t, harness.WithCodex(c))
		p := e.Project("paint", harness.ColorManifest)
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
	c := harness.NewCodex(t)
	c.Stream(1, "credits.jsonl")
	e := harness.New(t, harness.WithCodex(c))
	id := e.Run(e.Project("paint", harness.ColorManifest), "work")
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
		c := harness.NewCodex(t)
		c.Stream(1, tc.fixture)
		e := harness.New(t, harness.WithCodex(c))
		p := e.Project("paint", harness.ColorManifest)
		id := e.Run(p, "work")
		if got := e.WaitState(id, "done", "error"); got.State != "error" || !strings.Contains(got.Error, tc.want) {
			t.Fatalf("%s: want an error with %q: %+v", tc.fixture, tc.want, got)
		}
	}
}

func TestCodexSubAgentEventsAreNotProcessEvents(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	c.Stream(1, "subagent.jsonl")
	e := harness.New(t, harness.WithCodex(c))
	p := e.Project("paint", harness.ColorManifest)
	id := e.Run(p, "work")
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("a completed turn of another thread or an earlier turn does not end the turn: %+v", got)
	}
	var calls []map[string]any
	e.ModelCalls("", id, &calls)
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
	c := harness.NewCodex(t)
	c.Stream(1, "hang.jsonl")
	e := harness.New(t, harness.WithCodex(c))
	p := e.Project("paint", harness.ColorManifest)
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
	c := harness.NewCodex(t)
	raw, err := os.ReadFile(harness.Testdata("codex/hang.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	c.StreamText(1, strings.Replace(string(raw), `{"hang": true}`, `{"deaf": true}`, 1))
	e := harness.New(t, harness.WithCodex(c))
	p := e.Project("paint", harness.ColorManifest)
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
	c := harness.NewCodex(t)
	c.Stream(1, "spent_after.jsonl")
	e := harness.New(t, harness.WithCodex(c))
	if got := e.WaitState(e.Run(e.Project("paint", harness.ColorManifest), "work"), "done", "error", "rate_limited"); got.State != "done" {
		t.Fatalf("the completed turn counts: %+v", got)
	}
}

func TestFakeCodexRejectsWhatTheSchemaLacks(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	c.Stream(0, "ok.jsonl")
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
		cmd := exec.Command(filepath.Join(c.Dir, "codex"), tc.args...)
		cmd.Stdin = strings.NewReader(tc.input)
		out, err := cmd.CombinedOutput()
		if err == nil || !strings.Contains(string(out), tc.want) {
			t.Fatalf("%v: want a refusal with %q: %v %s", tc.args, tc.want, err, out)
		}
	}
}

// TestCodexResumeAfterALateAnswerAsksForTheResultAgain parks a process whose question was not answered in time;
// the resume with the owner's answer asks for the whole result again, since each turn hands in its own.
func TestCodexResumeAfterALateAnswerAsksForTheResultAgain(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	c.Stream(1, "ask.jsonl")
	c.Stream(2, "resume.jsonl")
	home := t.TempDir()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(c.Adapter(home)), harness.WithConfig(func(cfg *app.Config) { cfg.QuestionWait = 200 * time.Millisecond }))
	p := e.Project("paint", harness.ColorManifest)
	id := e.Run(p, "work")
	e.WaitState(id, "waiting_answer")
	if m := c.Call(1).Mcp; len(m) == 0 || m[0]["is_error"] != true || !strings.Contains(m[0]["text"].(string), "not answered yet") {
		t.Fatalf("an unanswered ask tells the agent to stop and wait: %v", m)
	}
	e.Answer(e.WaitAsk().ID, "blue")
	got := e.WaitState(id, "done", "error")
	var input []struct{ Text string }
	_ = json.Unmarshal(c.Call(2).Request("turn/start")["input"], &input)
	if got.State != "done" || len(input) != 1 || !strings.Contains(input[0].Text, "blue") ||
		!strings.Contains(input[0].Text, "hand in the whole final result with the result_submit tool") {
		t.Fatalf("the resume carries the answer and asks for the result again: %+v %v", got, input)
	}
}

func TestCodexSessionWithoutItsThreadStartsOver(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	c.Stream(1, "ask.jsonl")
	c.Stream(2, "ok.jsonl")
	c.Stream(3, "resume.jsonl")
	home := t.TempDir()
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(c.Adapter(home)), harness.WithConfig(func(cfg *app.Config) { cfg.QuestionWait = 200 * time.Millisecond }))
	p := e.Project("paint", harness.ColorManifest)
	id := e.Run(p, "work")
	e.WaitState(id, "waiting_answer")
	if err := os.RemoveAll(filepath.Join(deckhome.Layout(home).State(), "codex", "sessions")); err != nil {
		t.Fatal(err)
	}
	e.Answer(e.WaitAsk().ID, "blue")
	if got := e.WaitState(id, "done", "error"); got.State != "done" || c.Call(2).Request("thread/start") == nil {
		t.Fatalf("a session whose thread deck no longer knows starts a new thread: %+v", got)
	}
}

func TestCodexApprovalOfAnotherTurnIsDeclinedWithoutAsking(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	c.Stream(1, "escalation_staleturn.jsonl")
	e := harness.New(t, harness.WithCodex(c))
	p := e.Project("outside", outsideManifest)
	id := e.Run(p, "work")
	got := e.WaitState(id, "done", "error")
	var asks []harness.Ask
	_, out := e.Call("deck.asks_list", nil)
	_ = json.Unmarshal(out, &asks)
	if cc := c.Call(1); got.State != "done" || len(asks) != 0 || len(cc.Answers) != 1 || !strings.Contains(string(harness.MustJSON(cc.Answers[0])), `"decline"`) {
		t.Fatalf("a request that is not of the running turn is declined without the owner: %+v %v %v", got, asks, cc.Answers)
	}
}

func TestCodexOwnerServerNamedDeckIsAnError(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	c.Stream(1, "deckserver.jsonl")
	e := harness.New(t, harness.WithCodex(c))
	p := e.Project("paint", harness.ColorManifest)
	id := e.Run(p, "work")
	if got := e.WaitState(id, "done", "error"); got.State != "error" || !strings.Contains(got.Error, "MCP server named deck") {
		t.Fatalf("an MCP server of the owner named deck stops the turn with a clear error: %+v", got)
	}
}

func TestCodexTurnGoesOnWhenTheAppServerStopsReadingWhileItWrites(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	b, _ := os.ReadFile(harness.Testdata("codex/ok.jsonl"))
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
	c.StreamText(0, strings.Join(out, "\n")+"\n")
	e := harness.New(t, harness.WithCodex(c))
	m := strings.Replace(harness.ColorManifest, "Ask the owner which color the report should use.", strings.Repeat("Pick blue. ", 30000), 1)
	p := e.Project("paint", m)
	if got := e.WaitState(e.Run(p, "work"), "done", "error"); got.State != "done" || string(got.Result) != `{"color":"blue"}` {
		t.Fatalf("deck reads the output while its long input waits for the app-server: %+v", got)
	}
}

func TestTheFinalAnswerOfCodexIsTheTextOfItsTurn(t *testing.T) {
	t.Parallel()
	c := harness.NewCodex(t)
	c.Stream(0, "reply.jsonl")
	e := harness.New(t, harness.WithCodex(c))
	topic := e.Say(0, "We are out of oat milk.")
	if said := e.MasterSaid(topic, 1); said[0].Text != "Put it into groceries." {
		t.Fatalf("the final answer of a Codex turn is its text: %+v", said)
	}
}
