package scenarios

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"deck2/internal/provider"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// approving makes the fake CLI act as the real one does in auto mode on the self-check: the write in its folder
// runs without asking, only the question reaches deck's permission tool, and curl fails without network.
func (c *fakeCLI) approving() {
	self, _ := os.Executable()
	offline, _ := filepath.Abs("testdata/claude/selfcheck_offline.jsonl")
	c.before(`if grep -q -- --tools "$c/args" && grep -q 'self-check of deck' "$c/stdin"; then
  sed "s/{{session}}/$session/g" '` + offline + `'
  exit 0
fi
if [ -n "$(grep -o 'deck-check-' "$c/stdin")" ] && ! grep -q -- --resume "$c/args"; then
  echo "$word" > word.txt
  url=$(sed -n 's/.*"url":"\([^"]*\)".*/\1/p' "$c/mcpconfig")
  DECK_FAKE_MCP=approve '` + self + `' "$url" AskUserQuestion '{"questions":[{"question":"Which answer should the result carry?"}]}'
fi`)
}

func (c *fakeCLI) before(script string) {
	if err := os.WriteFile(filepath.Join(c.dir, "before"), []byte(script), 0o644); err != nil {
		c.t.Fatal(err)
	}
}

func (c *fakeCLI) version(v string) {
	if err := os.WriteFile(filepath.Join(c.dir, "version"), []byte(v), 0o644); err != nil {
		c.t.Fatal(err)
	}
}

func clockCLIEnv(t *testing.T, c *fakeCLI) *Env {
	home := t.TempDir()
	clock := &Clock{now: t0}
	e := newEnvWith(t, home, c.adapter(home), withClock(clock))
	e.Clock = clock
	return e
}

func TestFailedSelfCheckHoldsClaudeCodeProcesses(t *testing.T) {
	c := newCLI(t)
	c.stream(0, "ok.jsonl")
	e := clockCLIEnv(t, c)
	ctx := context.Background()
	e.App.Providers.Checks(ctx, e.Clock.Now())
	h := e.waitHealth("provider:claude-code", "error")
	if h.Version != "2.1.281" || !strings.Contains(h.Error, "self-check of 2.1.281 failed") || !strings.Contains(h.Error, "output by schema") {
		t.Fatalf("a failed self-check is red with the version and the reason: %+v", h)
	}
	p := e.createProject("plain", plainManifest)
	id := e.run(p, "work")
	e.waitState(id, "waiting_provider")
	if n := c.calls(); n != 1 {
		t.Fatalf("processes on Claude Code wait while the self-check is red: %d CLI calls", n)
	}
	c.stream(2, "selfcheck.jsonl")
	c.stream(3, "selfcheck_resume.jsonl")
	c.approving()
	e.App.Providers.Checks(ctx, e.Clock.Add(10*time.Minute))
	e.settle()
	if n := c.calls(); n != 1 {
		t.Fatalf("a red self-check is retried by its timer, not sooner: %d CLI calls", n)
	}
	e.App.Providers.Checks(ctx, e.Clock.Add(21*time.Minute))
	if h := e.waitHealth("provider:claude-code", "ok"); h.Version != "2.1.281" {
		t.Fatalf("the retried self-check is green: %+v", h)
	}
	if got := e.waitState(id, "done", "error"); got.State != "done" || c.calls() != 5 {
		t.Fatalf("the waiting process runs once the self-check is green: %+v, %d calls", got, c.calls())
	}
	if !strings.Contains(c.call(2).stdin, "deck-check-") || !slices.Contains(c.call(3).args, "--resume") {
		t.Fatalf("the self-check is a turn and a resume of its session: %v", c.call(3).args)
	}
	c.version("2.1.282")
	e.App.Providers.Checks(ctx, e.Clock.Add(30*time.Minute))
	e.settle()
	if n := c.calls(); n != 5 {
		t.Fatalf("a new version of the CLI alone runs no self-check: %d CLI calls", n)
	}
	c.stream(6, "two_results.jsonl")
	c.stream(7, "ok.jsonl")
	if got := e.waitState(e.run(p, "work"), "done", "error"); got.State != "error" {
		t.Fatalf("the broken turn fails: %+v", got)
	}
	if h := e.waitHealth("provider:claude-code", "error"); h.Version != "2.1.282" {
		t.Fatalf("a turn that breaks checks the CLI again: %+v", h)
	}
	c.stream(8, "selfcheck.jsonl")
	c.stream(9, "selfcheck_resume.jsonl")
	e.must("deck.provider_check", map[string]any{"provider": "claude-code"}, nil)
	e.waitHealth("provider:claude-code", "ok")
	if code, _ := e.call("deck.provider_check", map[string]any{"provider": "fake"}); code == 200 {
		t.Fatal("only a provider with a self-check can be checked")
	}
}

func TestSelfCheckHittingTheLimitIsAPauseNotABrokenAdapter(t *testing.T) {
	for _, c := range []struct{ fixture, status string }{{"ratelimit_rejected.jsonl", "paused"}, {"nologin.jsonl", "needs_login"}} {
		t.Run(c.status, func(t *testing.T) {
			cli := newCLI(t)
			cli.stream(1, c.fixture)
			cli.stream(0, "ok.jsonl")
			e := clockCLIEnv(t, cli)
			e.App.Providers.Checks(context.Background(), e.Clock.Now())
			e.waitHealth("provider:claude-code", c.status)
			p := e.createProject("plain", plainManifest)
			if got := e.waitState(e.run(p, "work"), "done", "error", "waiting_provider"); got.State != "done" {
				t.Fatalf("a limit or a login is not a broken adapter, so processes do not wait for it: %+v", got)
			}
		})
	}
}

// checkedFake is the fake provider with a self-check the test turns red or green.
type checkedFake struct {
	*Fake
	mu  sync.Mutex
	err error
}


func (c *checkedFake) SelfCheck(context.Context) provider.Check {
	c.mu.Lock()
	defer c.mu.Unlock()
	return provider.Check{Version: "1.0", Err: c.err}
}

func (c *checkedFake) set(err error) {
	c.mu.Lock()
	c.err = err
	c.mu.Unlock()
}

func TestAnswerWhileTheProviderIsRedWaitsAndThenResumes(t *testing.T) {
	fake := &Fake{}
	cf := &checkedFake{Fake: fake}
	fake.script = func(ctx context.Context, c *Call) provider.Result {
		if c.N == 1 {
			c.Tool(ctx, "notes__post", map[string]any{"text": "hi"})
		}
		return provider.Result{Structured: json.RawMessage(`{"written": 1}`)}
	}
	clock := &Clock{now: t0}
	e := newEnvWith(t, t.TempDir(), cf, withClock(clock))
	e.Clock = clock
	p := e.createProject("notes", strings.Replace(notesManifest, `"requires": ["no_cloud"],`, "", 1))
	id := e.run(p, "collect")
	e.waitState(id, "waiting_answer")
	cf.set(errors.New("broken"))
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	e.waitHealth("provider:fake", "error")
	e.answer(e.waitAsk().ID, "deny:not now")
	e.waitState(id, "waiting_provider")
	cf.set(nil)
	e.must("deck.provider_check", map[string]any{"provider": "fake"}, nil)
	got := e.waitState(id, "done", "error")
	calls := fake.Calls()
	if got.State != "done" || len(calls) != 2 || !calls[1].Resume || !strings.Contains(calls[1].Prompt, "not now") {
		t.Fatalf("the held turn resumes the session with the owner's answer: %+v %+v", got, calls)
	}
}

func TestSelfCheckCutShortIsRunAgain(t *testing.T) {
	c := newCLI(t)
	c.stream(1, "ratelimit_rejected.jsonl")
	c.stream(2, "selfcheck.jsonl")
	c.stream(3, "selfcheck_resume.jsonl")
	c.approving()
	e := clockCLIEnv(t, c)
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	if h := e.waitHealth("provider:claude-code", "paused"); h.Version != "" {
		t.Fatalf("a self-check cut short by the limit proves nothing about the version: %+v", h)
	}
	e.App.Providers.Checks(context.Background(), e.Clock.Add(31*time.Minute))
	if h := e.waitHealth("provider:claude-code", "ok"); h.Version != "2.1.281" {
		t.Fatalf("the self-check runs again once the limit may be gone: %+v", h)
	}
}

func TestSelfCheckAllowsOnlyItsOwnCommand(t *testing.T) {
	c := newCLI(t)
	c.stream(1, "selfcheck.jsonl")
	c.stream(2, "selfcheck_resume.jsonl")
	self, _ := os.Executable()
	c.approving()
	c.before(readFile(t, filepath.Join(c.dir, "before")) + fmt.Sprintf(`
[ -n "$url" ] && DECK_FAKE_MCP=approve '%s' "$url" "cat ~/.ssh/id_rsa; echo $word > word.txt"
`, self))
	e := clockCLIEnv(t, c)
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	e.waitHealth("provider:claude-code", "ok")
}

func TestSelfCheckIsRedWhenTheCLIStopsCountingTokens(t *testing.T) {
	c := newCLI(t)
	c.stream(1, "selfcheck_nousage.jsonl")
	c.approving()
	e := clockCLIEnv(t, c)
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	if h := e.waitHealth("provider:claude-code", "error"); !strings.Contains(h.Error, "token counts") {
		t.Fatalf("a turn without usage turns the self-check red: %+v", h)
	}
}

func TestSelfCheckIsRedWhenAResumeStopsCountingTokens(t *testing.T) {
	c := newCLI(t)
	c.stream(1, "selfcheck.jsonl")
	c.stream(2, "selfcheck_resume_nousage.jsonl")
	c.approving()
	e := clockCLIEnv(t, c)
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	if h := e.waitHealth("provider:claude-code", "error"); !strings.Contains(h.Error, "resume: token counts") {
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
	*Fake
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
	hv := &hungCheck{Fake: &Fake{}, release: make(chan struct{})}
	clock := &Clock{now: t0}
	e := newEnvWith(t, t.TempDir(), hv, withClock(clock))
	t.Cleanup(func() { close(hv.release) })
	e.Clock = clock
	e.createProject("plain", strings.Replace(plainManifest, `"triggers": []`, `"triggers": [{"command": "work", "cron": "30 * * * *"}]`, 1))
	e.App.Start(20 * time.Millisecond)
	time.Sleep(200 * time.Millisecond)
	clock.Add(time.Hour)
	e.waitFor("the run that came due", func() bool { return len(e.runsOf("plain", "work")) == 1 })
}

// slowCheck is the fake provider whose self-check finds a model too slow for its ceiling; it counts its checks.
type slowCheck struct {
	*Fake
	checks atomic.Int32
}

func (s *slowCheck) SelfCheck(context.Context) provider.Check {
	s.checks.Add(1)
	return provider.Check{Version: "1.0", Slow: true, Err: errors.New("the model did not finish")}
}

// TestASlowModelIsAWarningThatHoldsNoTurn: a self-check that finds the model slow warns in the health of the
// provider, and its commands run on.
func TestASlowModelIsAWarningThatHoldsNoTurn(t *testing.T) {
	clock := &Clock{now: t0}
	slow := &slowCheck{Fake: &Fake{}}
	e := newEnvWith(t, t.TempDir(), slow, withClock(clock))
	e.Clock = clock
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	if h := e.waitHealth("provider:fake", "warning"); !strings.Contains(h.Error, "did not finish") {
		t.Fatalf("a slow model is a warning with the reason: %+v", h)
	}
	e.App.Providers.Checks(context.Background(), e.Clock.Add(31*time.Minute))
	e.settle()
	if n := slow.checks.Load(); n != 1 {
		t.Fatalf("a warning is not checked again by the timer, which repeats only what holds turns: %d checks", n)
	}
	p := e.createProject("plain", plainManifest)
	if got := e.waitState(e.run(p, "work"), "done", "error", "waiting_provider"); got.State != "done" {
		t.Fatalf("a slow model holds no turn: %+v", got)
	}
}

// TestATurnTheProviderReportsAsAnErrorChecksIt: a turn the CLI ends with an error result starts a self-check too.
func TestATurnTheProviderReportsAsAnErrorChecksIt(t *testing.T) {
	c := newCLI(t)
	c.stream(1, "selfcheck.jsonl")
	c.stream(2, "selfcheck_resume.jsonl")
	c.approving()
	e := clockCLIEnv(t, c)
	e.App.Providers.Checks(context.Background(), e.Clock.Now())
	e.waitHealth("provider:claude-code", "ok")
	c.stream(4, "error.jsonl")
	c.stream(5, "ok.jsonl")
	e.Clock.Add(31 * time.Minute)
	if got := e.waitState(e.run(e.createProject("plain", plainManifest), "work"), "done", "error"); got.State != "error" {
		t.Fatalf("the turn fails: %+v", got)
	}
	e.waitHealth("provider:claude-code", "error")
}
