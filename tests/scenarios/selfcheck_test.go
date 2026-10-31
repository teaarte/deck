package scenarios

import (
	"context"
	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/tests/harness"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func version(c *harness.FakeCLI, v string) {
	if err := os.WriteFile(filepath.Join(c.Dir, "version"), []byte(v), 0o644); err != nil {
		c.T.Fatal(err)
	}
}

func TestFailedSelfCheckHoldsClaudeCodeProcesses(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	e := harness.ClockCLIEnv(t, c)
	ctx := context.Background()
	e.App.Providers.Checks(ctx, e.Clock.Now())
	h := e.WaitHealth("provider:claude-code", "error")
	if h.Version != "2.1.281" || !strings.Contains(h.Error, "self-check of 2.1.281 failed") || !strings.Contains(h.Error, "output by schema") {
		t.Fatalf("a failed self-check is red with the version and the reason: %+v", h)
	}
	p := e.Project("plain", harness.PlainManifest)
	id := e.Run(p, "work")
	e.WaitState(id, "waiting_provider")
	if n := c.Calls(); n != 1 {
		t.Fatalf("processes on Claude Code wait while the self-check is red: %d CLI calls", n)
	}
	c.Stream(2, "selfcheck.jsonl")
	c.Stream(3, "selfcheck_resume.jsonl")
	c.Approving()
	e.App.Providers.Checks(ctx, e.Clock.Add(10*time.Minute))
	e.Settle()
	if n := c.Calls(); n != 1 {
		t.Fatalf("a red self-check is retried by its timer, not sooner: %d CLI calls", n)
	}
	e.App.Providers.Checks(ctx, e.Clock.Add(21*time.Minute))
	if h := e.WaitHealth("provider:claude-code", "ok"); h.Version != "2.1.281" {
		t.Fatalf("the retried self-check is green: %+v", h)
	}
	if got := e.WaitState(id, "done", "error"); got.State != "done" || c.Calls() != 5 {
		t.Fatalf("the waiting process runs once the self-check is green: %+v, %d calls", got, c.Calls())
	}
	if !strings.Contains(c.Call(2).Stdin, "deck-check-") || !slices.Contains(c.Call(3).Args, "--resume") {
		t.Fatalf("the self-check is a turn and a resume of its session: %v", c.Call(3).Args)
	}
	version(c, "2.1.282")
	e.App.Providers.Checks(ctx, e.Clock.Add(30*time.Minute))
	e.Settle()
	if n := c.Calls(); n != 5 {
		t.Fatalf("a new version of the CLI alone runs no self-check: %d CLI calls", n)
	}
	c.Stream(6, "foreign_session.jsonl")
	c.Stream(7, "ok.jsonl")
	if got := e.WaitState(e.Run(p, "work"), "done", "error"); got.State != "error" {
		t.Fatalf("the broken turn fails: %+v", got)
	}
	if h := e.WaitHealth("provider:claude-code", "error"); h.Version != "2.1.282" {
		t.Fatalf("a turn that breaks checks the CLI again: %+v", h)
	}
	c.Stream(8, "selfcheck.jsonl")
	c.Stream(9, "selfcheck_resume.jsonl")
	e.Must("deck.provider_check", map[string]any{"provider": "claude-code"}, nil)
	e.WaitHealth("provider:claude-code", "ok")
	if code, _ := e.Call("deck.provider_check", map[string]any{"provider": "fake"}); code == 200 {
		t.Fatal("only a provider with a self-check can be checked")
	}
}

func TestSelfCheckHittingTheLimitIsAPauseNotABrokenAdapter(t *testing.T) {
	t.Parallel()
	for _, c := range []struct{ fixture, status string }{{"ratelimit_rejected.jsonl", "paused"}, {"nologin.jsonl", "needs_login"}} {
		t.Run(c.status, func(t *testing.T) {
			t.Parallel()
			cli := harness.NewCLI(t)
			cli.Stream(1, c.fixture)
			cli.Stream(0, "ok.jsonl")
			e := harness.ClockCLIEnv(t, cli)
			e.App.Providers.Checks(context.Background(), e.Clock.Now())
			e.WaitHealth("provider:claude-code", c.status)
			p := e.Project("plain", harness.PlainManifest)
			want := map[string]string{"paused": "waiting_provider", "needs_login": "done"}[c.status]
			if got := e.WaitState(e.Run(p, "work"), "done", "error", "waiting_provider"); got.State != want {
				t.Fatalf("a limit holds turns until it resets, a lost login holds none, the turn asks: %+v", got)
			}
		})
	}
}

// checkedFake is the fake provider with a self-check the test turns red or green.
type checkedFake struct {
	*harness.Fake
	mu     sync.Mutex
	err    error
	checks int
}

func (c *checkedFake) SelfCheck(context.Context) provider.Check {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.checks++
	return provider.Check{Version: "1.0", Err: c.err}
}

func (c *checkedFake) set(err error) {
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
}

func TestAnswerWhileTheProviderIsRedWaitsAndThenResumes(t *testing.T) {
	t.Parallel()
	fake := &harness.Fake{}
	cf := &checkedFake{Fake: fake}
	fake.Script = func(ctx context.Context, c *harness.Call) provider.Result {
		if c.N == 1 {
			c.Tool(ctx, "notes__post", map[string]any{"text": "hi"})
		}
		return provider.Result{Structured: json.RawMessage(`{"written": 1}`)}
	}
	clock := &harness.Clock{At: harness.T0}
	e := harness.New(t, harness.WithProvider(cf), harness.WithClock(clock))
	p := e.Project("notes", strings.Replace(harness.NotesManifest, `"requires": ["no_cloud"],`, "", 1))
	id := e.Run(p, "collect")
	e.WaitState(id, "waiting_answer")
	cf.set(errors.New("broken"))
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	e.WaitHealth("provider:fake", "error")
	e.Answer(e.WaitAsk().ID, "deny:not now")
	e.WaitState(id, "waiting_provider")
	cf.set(nil)
	e.Must("deck.provider_check", map[string]any{"provider": "fake"}, nil)
	got := e.WaitState(id, "done", "error")
	calls := fake.Calls()
	if got.State != "done" || len(calls) != 2 || !calls[1].Resume || !strings.Contains(calls[1].Prompt, "not now") {
		t.Fatalf("the held turn resumes the session with the owner's answer: %+v %+v", got, calls)
	}
}

func TestSelfCheckCutShortIsRunAgain(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(1, "ratelimit_rejected.jsonl")
	c.Stream(2, "selfcheck.jsonl")
	c.Stream(3, "selfcheck_resume.jsonl")
	c.Approving()
	e := harness.ClockCLIEnv(t, c)
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	if h := e.WaitHealth("provider:claude-code", "paused"); h.Version != "" {
		t.Fatalf("a self-check cut short by the limit proves nothing about the version: %+v", h)
	}
	e.App.Providers.Checks(context.Background(), e.Clock.Add(31*time.Minute))
	if h := e.WaitHealth("provider:claude-code", "ok"); h.Version != "2.1.281" {
		t.Fatalf("the self-check runs again once the limit may be gone: %+v", h)
	}
}

func TestSelfCheckAllowsOnlyItsOwnCommand(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(1, "selfcheck.jsonl")
	c.Stream(2, "selfcheck_resume.jsonl")
	self := harness.Fakes
	c.Approving()
	c.Before(readFile(t, filepath.Join(c.Dir, "before")) + fmt.Sprintf(`
[ -n "$url" ] && DECK_FAKE_MCP=approve '%s' "$url" Bash '{"command":"cat ~/.ssh/id_rsa"}'
`, self))
	e := harness.ClockCLIEnv(t, c)
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	if h := e.WaitHealth("provider:claude-code", "error"); !strings.Contains(h.Error, "Bash instead of passing a write") {
		t.Fatalf("a question about any other command makes the self-check red: %+v", h)
	}
}

func TestSelfCheckIsRedWhenTheCLIStopsCountingTokens(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(1, "selfcheck_nousage.jsonl")
	c.Approving()
	e := harness.ClockCLIEnv(t, c)
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	if h := e.WaitHealth("provider:claude-code", "error"); !strings.Contains(h.Error, "token counts") {
		t.Fatalf("a turn without usage turns the self-check red: %+v", h)
	}
}

func TestSelfCheckIsRedWhenAResumeStopsCountingTokens(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(1, "selfcheck.jsonl")
	c.Stream(2, "selfcheck_resume_nousage.jsonl")
	c.Approving()
	e := harness.ClockCLIEnv(t, c)
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	if h := e.WaitHealth("provider:claude-code", "error"); !strings.Contains(h.Error, "resume: token counts") {
		t.Fatalf("a resume without usage turns the self-check red: %+v", h)
	}
}

func readFile(t *testing.T, path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// hungCheck is the fake provider whose self-check hangs until released.
type hungCheck struct {
	*harness.Fake
	release chan struct{}
}

func (h *hungCheck) SelfCheck(ctx context.Context) provider.Check {
	select {
	case <-h.release:
	case <-ctx.Done():
	}
	return provider.Check{Version: "1.0"}
}

// TestAHungSelfCheckDoesNotStopTheSchedule: the loop of deck only finds what is due; a provider whose self-check
// hangs does not hold the schedule.
func TestAHungSelfCheckDoesNotStopTheSchedule(t *testing.T) {
	t.Parallel()
	hv := &hungCheck{Fake: &harness.Fake{}, release: make(chan struct{})}
	clock := &harness.Clock{At: harness.T0}
	e := harness.New(t, harness.WithProvider(hv), harness.WithClock(clock))
	t.Cleanup(func() { close(hv.release) })
	e.Project("plain", strings.Replace(harness.PlainManifest, `"triggers": []`, `"triggers": [{"command": "work", "cron": "30 * * * *"}]`, 1))
	e.App.Run(t.Context(), 20*time.Millisecond)
	// The loop starts the self-check that hangs; its health need not show it.
	time.Sleep(200 * time.Millisecond)
	clock.Add(time.Hour)
	e.Until("the run that came due", func() bool { return len(runsOf(e, "plain", "work")) == 1 })
}

// slowCheck is the fake provider whose self-check finds a model too slow for its ceiling; it counts its checks.
type slowCheck struct {
	*harness.Fake
	checks atomic.Int32
}

func (s *slowCheck) SelfCheck(context.Context) provider.Check {
	s.checks.Add(1)
	return provider.Check{Version: "1.0", Slow: true, Err: errors.New("the model did not finish")}
}

// TestASlowModelIsAWarningThatHoldsNoTurn: a self-check that finds the model slow warns in the health of the
// provider, and its commands run on.
func TestASlowModelIsAWarningThatHoldsNoTurn(t *testing.T) {
	t.Parallel()
	clock := &harness.Clock{At: harness.T0}
	slow := &slowCheck{Fake: &harness.Fake{}}
	e := harness.New(t, harness.WithProvider(slow), harness.WithClock(clock))
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	if h := e.WaitHealth("provider:fake", "warning"); !strings.Contains(h.Error, "did not finish") {
		t.Fatalf("a slow model is a warning with the reason: %+v", h)
	}
	e.App.Providers.Checks(context.Background(), e.Clock.Add(31*time.Minute))
	e.Settle()
	if n := slow.checks.Load(); n != 1 {
		t.Fatalf("a warning is not checked again by the timer, which repeats only what holds turns: %d checks", n)
	}
	p := e.Project("plain", harness.PlainManifest)
	if got := e.WaitState(e.Run(p, "work"), "done", "error", "waiting_provider"); got.State != "done" {
		t.Fatalf("a slow model holds no turn: %+v", got)
	}
}

// TestATurnTheProviderReportsAsAnErrorChecksIt: a turn the CLI ends with an error result starts a self-check too.
func TestATurnTheProviderReportsAsAnErrorChecksIt(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(1, "selfcheck.jsonl")
	c.Stream(2, "selfcheck_resume.jsonl")
	c.Approving()
	e := harness.ClockCLIEnv(t, c)
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	e.WaitHealth("provider:claude-code", "ok")
	c.Stream(4, "error.jsonl")
	c.Stream(5, "ok.jsonl")
	e.Clock.Add(31 * time.Minute)
	if got := e.WaitState(e.Run(e.Project("plain", harness.PlainManifest), "work"), "done", "error"); got.State != "error" {
		t.Fatalf("the turn fails: %+v", got)
	}
	e.WaitHealth("provider:claude-code", "error")
}

func TestACommandDuringTheStopStartsNoBackgroundWork(t *testing.T) {
	t.Parallel()
	cf := &checkedFake{Fake: &harness.Fake{}}
	e := harness.New(t, harness.WithProvider(cf))
	e.App.Stop()
	code, out := e.Call("deck.provider_check", map[string]any{"provider": "fake"})
	// No check starts.
	time.Sleep(300 * time.Millisecond)
	cf.mu.Lock()
	n := cf.checks
	cf.mu.Unlock()
	if code == 200 || n != 0 || !strings.Contains(string(out), "stopping") {
		t.Fatalf("a self-check asked for while deck stops does not start: %d %s, %d checks", code, out, n)
	}
}

// TestAnAgentStartedBeforeDecksMCPWaitsAndRunsOnceItIsUp: a turn whose CLI did not get deck's tools does not fail
// the process: the provider is red, the process waits for it, and its self-check later runs the process again.
func TestAnAgentStartedBeforeDecksMCPWaitsAndRunsOnceItIsUp(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	c.Stream(1, "selfcheck.jsonl")
	c.Stream(2, "selfcheck_resume.jsonl")
	c.Approving()
	c.Before(readFile(t, filepath.Join(c.Dir, "before")) + `
if [ -f "$d/nomcp" ]; then
  echo "Error: MCP tool mcp__deck__approve (passed via --permission-prompt-tool) not found. Available MCP tools: none" >&2
  exit 1
fi`)
	e := harness.ClockCLIEnv(t, c)
	ctx := context.Background()
	e.App.Providers.Checks(ctx, e.Clock.Now())
	e.WaitHealth("provider:claude-code", "ok")
	nomcp := filepath.Join(c.Dir, "nomcp")
	if err := os.WriteFile(nomcp, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	id := e.Run(e.Project("plain", harness.PlainManifest), "work")
	e.WaitState(id, "waiting_provider")
	if h := e.WaitHealth("provider:claude-code", "error"); !strings.Contains(h.Error, "without deck's tools") || !strings.Contains(h.Error, "mcp__deck__approve") {
		t.Fatalf("the provider is red with why: %+v", h)
	}
	if runs := runsOf(e, "master", "on_error"); len(runs) != 0 {
		t.Fatalf("a turn deck holds to run again is no failure for the master: %+v", runs)
	}
	if err := os.Remove(nomcp); err != nil {
		t.Fatal(err)
	}
	c.Stream(5, "selfcheck.jsonl")
	c.Stream(6, "selfcheck_resume.jsonl")
	e.App.Providers.Checks(ctx, e.Clock.Add(10*time.Minute))
	e.Settle()
	if n := c.Calls(); n != 4 {
		t.Fatalf("the provider checked a moment ago is checked again after its time: %d CLI calls", n)
	}
	e.App.Providers.Checks(ctx, e.Clock.Add(21*time.Minute))
	e.WaitHealth("provider:claude-code", "ok")
	if got := e.WaitState(id, "done", "error"); got.State != "done" || c.Calls() != 8 {
		t.Fatalf("the held process runs again once the self-check is green: %+v, %d calls", got, c.Calls())
	}
}

// TestASelfCheckIsRedWhenAgentsDoNotReachDecksMCP: the self-check reaches deck's MCP server as a turn would, so a
// gateway agents cannot reach holds the turns instead of leaving the provider green.
func TestASelfCheckIsRedWhenAgentsDoNotReachDecksMCP(t *testing.T) {
	t.Parallel()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	closed := "http://" + ln.Addr().String()
	ln.Close()
	cf := &checkedFake{Fake: &harness.Fake{}}
	e := harness.New(t, harness.WithProvider(cf), harness.WithConfig(func(c *app.Config) { c.MCPBase = closed }))
	e.App.Providers.Checks(context.Background(), time.Now())
	if h := e.WaitHealth("provider:fake", "error"); !strings.Contains(h.Error, "do not reach deck's MCP server") {
		t.Fatalf("an unreachable gateway turns the provider red: %+v", h)
	}
	if got := e.WaitState(e.Run(e.Project("plain", harness.PlainManifest), "work"), "done", "error", "waiting_provider"); got.State != "waiting_provider" {
		t.Fatalf("turns wait while agents would get no tools: %+v", got)
	}
}

// TestAnAgentThatNeverGetsDecksToolsFails: a process whose turns keep starting without deck's tools, though the
// self-check passes, fails after a few tries as deck's failure, which the master does not get.
func TestAnAgentThatNeverGetsDecksToolsFails(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	for _, n := range []int{1, 5, 9} {
		c.Stream(n, "selfcheck.jsonl")
		c.Stream(n+1, "selfcheck_resume.jsonl")
	}
	c.Approving()
	c.Before(readFile(t, filepath.Join(c.Dir, "before")) + `
if [ -f "$d/nomcp" ] && ! grep -q 'deck-check-' "$c/stdin" && ! grep -q -- --resume "$c/args" && ! grep -q -- --tools "$c/args"; then
  echo "Error: MCP tool mcp__deck__approve (passed via --permission-prompt-tool) not found. Available MCP tools: none" >&2
  exit 1
fi`)
	e := harness.ClockCLIEnv(t, c)
	masterAwake(e)
	ctx := context.Background()
	e.App.Providers.Checks(ctx, e.Clock.Now())
	e.WaitHealth("provider:claude-code", "ok")
	if err := os.WriteFile(filepath.Join(c.Dir, "nomcp"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	id := e.Run(e.Project("plain", harness.PlainManifest), "work")
	for i := range 2 {
		e.WaitState(id, "waiting_provider")
		e.App.Providers.Checks(ctx, e.Clock.Add(31*time.Minute))
		if i == 0 {
			e.Until("the second try", func() bool { return c.Calls() >= 8 })
		}
	}
	if got := e.WaitState(id, "done", "error"); got.State != "error" || !strings.Contains(got.Error, "without deck's tools 3 times") {
		t.Fatalf("the process fails after its tries: %+v", got)
	}
	if got := e.Fault(id); got.ErrorType != "deck.tools" || got.Whose != "deck" || !strings.Contains(got.ErrorDetails, "mcp__deck__approve") {
		t.Fatalf("a turn without deck's tools is deck's failure: %+v", got)
	}
	e.App.Runner.Wait()
	if runs := runsOf(e, "master", "on_error"); len(runs) != 0 {
		t.Fatalf("deck's failure is in health, not a run of the master that would fail the same way: %+v", runs)
	}
	if rows := failedRuns(e); len(rows) != 0 {
		t.Fatalf("a failure without deck's tools is one row of health, not rows of the cockpit: %+v", rows)
	}
}

// lossyFake is the checked fake whose next turns start without deck's tools.
type lossyFake struct {
	*checkedFake
	lose atomic.Int64
}

func (l *lossyFake) Run(ctx context.Context, req provider.Request) (provider.Result, error) {
	if l.lose.Add(-1) >= 0 {
		return provider.Result{Outcome: provider.NoTools}, errors.New("the MCP tool of deck was not found")
	}
	l.lose.Store(0)
	return l.checkedFake.Run(ctx, req)
}

// TestToolsLostInEarlierTurnsDoNotCountAgainstALaterOne: the tries without deck's tools are of one stretch of a
// process; a turn that got its tools, or a process that closed, starts the count over.
func TestToolsLostInEarlierTurnsDoNotCountAgainstALaterOne(t *testing.T) {
	t.Parallel()
	lf := &lossyFake{checkedFake: &checkedFake{Fake: &harness.Fake{}}}
	clock := &harness.Clock{At: harness.T0}
	e := harness.New(t, harness.WithProvider(lf), harness.WithClock(clock))
	ctx := context.Background()
	e.App.Providers.Checks(ctx, clock.Now())
	e.WaitHealth("provider:fake", "ok")
	id := e.Run(e.Project("plain", harness.PlainManifest), "work")
	for i := range 3 {
		if i > 0 {
			e.WaitState(id, "done")
			lf.lose.Store(1)
			e.Must("deck.process_rework", map[string]any{"process": id, "remark": "once more"}, nil)
		} else {
			lf.lose.Store(1)
		}
		if got := e.WaitState(id, "waiting_provider", "error"); got.State != "waiting_provider" {
			t.Fatalf("round %d: a turn that lost its tools once waits for the self-check: %+v", i, got)
		}
		e.App.Providers.Checks(ctx, clock.Add(31*time.Minute))
		if got := e.WaitState(id, "done", "error"); got.State != "done" {
			t.Fatalf("round %d: the held turn runs once the self-check is green: %+v", i, got)
		}
	}
}

// gatedFake is the lossy fake whose self-checks, once held, each wait for the test to let them through.
type gatedFake struct {
	*lossyFake
	hold    atomic.Bool
	entered chan struct{}
	gate    chan struct{}
}

func (g *gatedFake) SelfCheck(ctx context.Context) provider.Check {
	if g.hold.Load() {
		g.entered <- struct{}{}
		<-g.gate
	}
	return g.checkedFake.SelfCheck(ctx)
}

// TestASelfCheckATurnBrokeDuringIsRunAgain: a turn that loses deck's tools while a self-check runs makes that check
// stale; it neither turns the provider green nor wakes the held turn, and a fresh check decides.
func TestASelfCheckATurnBrokeDuringIsRunAgain(t *testing.T) {
	t.Parallel()
	g := &gatedFake{lossyFake: &lossyFake{checkedFake: &checkedFake{Fake: &harness.Fake{}}}, entered: make(chan struct{}), gate: make(chan struct{})}
	g.Script = func(ctx context.Context, c *harness.Call) provider.Result {
		if c.N == 1 {
			return provider.Result{Outcome: provider.Failed, Text: "boom"}
		}
		return provider.Result{Text: "ok"}
	}
	clock := &harness.Clock{At: harness.T0}
	e := harness.New(t, harness.WithProvider(g), harness.WithClock(clock))
	e.App.Providers.Checks(context.Background(), clock.Now())
	e.WaitHealth("provider:fake", "ok")
	g.hold.Store(true)
	clock.Add(31 * time.Minute)
	p := e.Project("plain", harness.PlainManifest)
	e.WaitState(e.Run(p, "work"), "error")
	<-g.entered
	g.lose.Store(1)
	id := e.Run(p, "work")
	e.WaitState(id, "waiting_provider")
	e.WaitHealth("provider:fake", "error")
	g.gate <- struct{}{}
	harness.Receive(t, "the stale self-check to run again", g.entered)
	if got := e.WaitState(id, "waiting_provider", "done", "error"); got.State != "waiting_provider" {
		t.Fatalf("a stale self-check wakes no held turn: %+v", got)
	}
	if h := e.HealthRowOf("provider:fake"); h["status"] == "ok" {
		t.Fatalf("a stale self-check does not turn the provider green: %v", h)
	}
	g.hold.Store(false)
	g.gate <- struct{}{}
	if got := e.WaitState(id, "done", "error"); got.State != "done" {
		t.Fatalf("the fresh self-check wakes the held turn: %+v", got)
	}
}

// TestAPausedOrLoggedOutProviderIsCheckedAgainWithoutItsProcess: a limit or a lost login a turn met is checked by
// the self-check, at the reset or at once, so the row lifts though the owner stopped the process.
func TestAPausedOrLoggedOutProviderIsCheckedAgainWithoutItsProcess(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		status string
		res    provider.Result
		after  time.Duration
	}{
		{"paused", provider.Result{Outcome: provider.Limited, ResetsAt: harness.T0.Add(2 * time.Hour).Unix(), Text: "limit"}, 2*time.Hour + time.Minute},
		{"needs_login", provider.Result{Outcome: provider.Login, Text: "Not logged in"}, 0},
	} {
		t.Run(c.status, func(t *testing.T) {
			cf := &checkedFake{Fake: &harness.Fake{}}
			cf.Script = func(context.Context, *harness.Call) provider.Result { return c.res }
			clock := &harness.Clock{At: harness.T0}
			e := harness.New(t, harness.WithProvider(cf), harness.WithClock(clock))
			ctx := context.Background()
			e.App.Providers.Checks(ctx, clock.Now())
			e.WaitHealth("provider:fake", "ok")
			clock.Add(31 * time.Minute)
			id := e.Run(e.Project("plain", harness.PlainManifest), "work")
			e.WaitState(id, "rate_limited", "needs_login")
			e.Must("deck.process_stop", map[string]any{"process": id}, nil)
			e.App.Providers.Checks(ctx, clock.Add(c.after))
			if h := e.WaitHealth("provider:fake", "ok"); h.Status != "ok" {
				t.Fatalf("the self-check lifts the row: %+v", h)
			}
		})
	}
}

// TestASessionWaitsForItsPausedProviderWhileANewStartTakesTheNext: a turn at the limit goes on on the next model; a
// session of the paused provider waits until it is up again instead of running into the limit.
func TestASessionWaitsForItsPausedProviderWhileANewStartTakesTheNext(t *testing.T) {
	t.Parallel()
	var limited atomic.Bool
	main := &checkedFake{Fake: &harness.Fake{Script: func(context.Context, *harness.Call) provider.Result {
		if limited.Load() {
			return provider.Result{Outcome: provider.Limited, ResetsAt: harness.T0.Add(2 * time.Hour).Unix(), Text: "limit"}
		}
		return provider.Result{Text: "ok"}
	}}}
	other := &checkedFake{Fake: &harness.Fake{ProviderName: "other"}}
	clock := &harness.Clock{At: harness.T0}
	e := harness.New(t, harness.WithProvider(main, other), harness.WithClock(clock))
	ctx := context.Background()
	e.App.Providers.Checks(ctx, clock.Now())
	e.WaitHealth("provider:fake", "ok")
	e.WaitHealth("provider:other", "ok")
	p := e.Project("plain", harness.PlainManifest)
	e.UseModels("plain.work", "fake", "other")
	done := e.Run(p, "work")
	e.WaitState(done, "done")
	limited.Store(true)
	refused := e.Run(p, "work")
	e.WaitState(refused, "error")
	e.WaitState(following(e, refused), "done")
	e.WaitHealth("provider:fake", "paused")
	var v struct{ Provider string }
	fresh := e.Run(p, "work")
	e.WaitState(fresh, "done")
	if e.Must("deck.process_get", map[string]any{"process": fresh}, &v); v.Provider != "other" {
		t.Fatalf("a new start goes to the next model: %+v", v)
	}
	calls := len(main.Calls())
	e.Must("deck.process_rework", map[string]any{"process": done, "remark": "once more"}, nil)
	if got := e.WaitState(done, "waiting_provider", "rate_limited", "done"); got.State != "waiting_provider" || len(main.Calls()) != calls {
		t.Fatalf("the session waits for its paused provider and does not run into the limit: %+v, %d calls", got, len(main.Calls())-calls)
	}
	limited.Store(false)
	e.App.Providers.Checks(ctx, clock.Add(2*time.Hour+time.Minute))
	if got := e.WaitState(done, "done", "error"); got.State != "done" {
		t.Fatalf("the session goes on once its provider is up: %+v", got)
	}
}
