package scenarios

import (
	"context"
	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestAgentCallsReturnAtOnceAndRetryKeyReusesTheProcess(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	ids := make(chan []string, 1)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "work" {
			select {
			case <-release:
			case <-ctx.Done():
			}
			return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
		}
		var got []string
		for _, key := range []string{"a", "a", "b"} {
			out, _ := c.Tool(ctx, "jobs__work", map[string]any{"item": key, "retry_key": key})
			got = append(got, out)
		}
		ids <- got
		return provider.Result{Structured: json.RawMessage(`{"n": 3}`)}
	}))

	p := e.Project("jobs", harness.TestManifest([]string{harness.AgentCommand("fan", ""),
		harness.AgentCommand("work", `{"type": "object", "properties": {"item": {"type": "string"}}, "additionalProperties": false}`)}, nil))
	fan := e.Run(p, "fan")
	got := harness.Receive(t, "the process ids of the commands the agent called, without waiting for them", ids)
	e.WaitState(fan, "done")
	var first, same, other struct {
		Process int64
		Running bool
	}
	_ = json.Unmarshal([]byte(got[0]), &first)
	_ = json.Unmarshal([]byte(got[1]), &same)
	_ = json.Unmarshal([]byte(got[2]), &other)
	if first.Process == 0 || first.Process != same.Process || !same.Running || first.Running || other.Process == first.Process {
		t.Fatalf("the same key returns the same process, said to be running already, another key another one: %v", got)
	}
	if pr := e.Proc(first.Process); pr.Parent != fan || pr.Depth != 2 || pr.State != "running" {
		t.Fatalf("the called process belongs to its caller and is still running: %+v", pr)
	}
	var again struct{ Process int64 }
	e.Must("jobs.work", map[string]any{"item": "a", "retry_key": "a"}, &again)
	if again.Process != first.Process {
		t.Fatalf("the owner gets the same process for the same key: %d vs %d", again.Process, first.Process)
	}
	close(release)
	e.WaitState(first.Process, "done")
	e.Must("jobs.work", map[string]any{"item": "a", "retry_key": "a"}, &again)
	if again.Process == first.Process {
		t.Fatal("a finished process does not hold its key")
	}
	e.WaitState(again.Process, "done")
	if in := harness.ProcessInput(t, e, again.Process); in["retry_key"] != nil || in["item"] != "a" {
		t.Fatalf("the retry key is not part of the command input: %v", in)
	}
}

func TestReworkContinuesTheSessionAndRevisesTheArtifact(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		text := "report v1"
		if c.Req.Resume {
			text = "report v2"
		}
		c.Tool(ctx, "document_save", map[string]any{"name": "report.md", "kind": "artifact", "text": text})
		return provider.Result{Structured: json.RawMessage(fmt.Sprintf(`{"n": %d}`, c.N))}
	}))

	p := e.Project("docs", harness.TestManifest([]string{harness.AgentCommand("write", "")}, nil))
	id := e.Run(p, "write")
	e.WaitState(id, "done")
	var started struct{ Process int64 }
	e.Must("deck.process_rework", map[string]any{"process": id, "remark": "make it shorter"}, &started)
	e.Until("the rework to finish", func() bool { return e.Proc(id).State == "done" && len(e.Fake.Calls()) == 2 })
	calls := e.Fake.Calls()
	if started.Process != id || !calls[1].Resume || calls[1].SessionID != calls[0].SessionID || !strings.Contains(calls[1].Prompt, "make it shorter") {
		t.Fatalf("rework resumes the same session with the remark: %+v", calls[1])
	}
	if got := e.Proc(id); string(got.Result) != `{"n":2}` {
		t.Fatalf("the rework gives the new result: %+v", got)
	}
	var d struct{ ID int64 }
	e.Must("deck.document_get", map[string]any{"project": "docs", "name": "report.md"}, &d)
	var h struct{ Revisions []struct{ Author string } }
	e.Must("deck.document_get", map[string]any{"document": d.ID, "history": true}, &h)
	if revs := h.Revisions; len(revs) != 2 {
		t.Fatalf("the rework makes a new revision of the artifact, got %v", revs)
	}
	e.Refused("deck.process_rework", map[string]any{"process": id, "remark": " "}, 422, "remark")
}

func TestCeilingStopsTheAgentAndManifestOverridesIt(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		c.Req.Emit(provider.Event{Kind: provider.EventThought, Text: "working"})
		<-ctx.Done()
		return provider.Result{}
	}), harness.WithClock())

	p := e.Project("long", harness.TestManifest([]string{harness.AgentCommand("short", ""),
		strings.Replace(harness.AgentCommand("long", ""), `"class"`, `"timeout_minutes": 120, "class"`, 1)}, nil))
	a, b := e.Run(p, "short"), e.Run(p, "long")
	e.Until("both ceilings to run and both models to give a sign", func() bool {
		return e.Proc(a).Deadline != "" && e.Proc(b).Deadline != "" && len(e.Log(a)) == 1 && len(e.Log(b)) == 1
	})
	e.Clock.Add(59 * time.Minute)
	e.Sweep()
	if got := e.Proc(a); got.State != "running" {
		t.Fatalf("the ceiling of a command agent is an hour: %+v", got)
	}
	e.Clock.Add(2 * time.Minute)
	e.Sweep()
	got := e.WaitState(a, "error")
	if !strings.Contains(got.Error, "ceiling") || e.Proc(b).State != "running" {
		t.Fatalf("the ceiling stops the agent; a manifest ceiling of 2 h holds: %+v %+v", got, e.Proc(b))
	}
	e.Clock.Add(time.Hour)
	e.Sweep()
	e.WaitState(b, "error")
}

func TestCeilingAndSilenceWaitForABlockingQuestion(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		askQuestion(ctx, c, "Which one?")
		<-ctx.Done()
		return provider.Result{}
	}), harness.WithClock(), harness.WithConfig(

		func(c *app.Config) { c.QuestionWait = 3 * time.Hour }))

	p := e.Project("ask", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	id := e.Run(p, "job")
	a := e.WaitAsk()
	e.Until("the ceiling to pause", func() bool { return e.Proc(id).Deadline == "" })
	e.Clock.Add(2 * time.Hour)
	e.Sweep()
	if got := e.Proc(id); got.State != "running" || got.SilentSince != "" {
		t.Fatalf("while the owner owes an answer the ceiling does not tick and the process is not silent: %+v", got)
	}
	e.Answer(a.ID, "the first")
	e.Until("the ceiling to run again", func() bool { return e.Proc(id).Deadline != "" })
	e.Clock.Add(50 * time.Minute)
	e.Sweep()
	if e.Proc(id).State != "running" {
		t.Fatal("the time of the question does not count against the ceiling")
	}
	e.Clock.Add(20 * time.Minute)
	e.Sweep()
	e.WaitState(id, "error")
}

func TestSilentProcessShowsSinceItsLastOutput(t *testing.T) {
	t.Parallel()
	more := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		c.Req.OnEvent(provider.Event{Kind: "text", Text: "working"})
		<-more
		c.Req.OnEvent(provider.Event{Kind: "tool", Text: "Read"})
		<-ctx.Done()
		return provider.Result{}
	}), harness.WithClock())

	p := e.Project("quiet", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	id := e.Run(p, "job")
	e.Until("the first output", func() bool { return len(e.Log(id)) == 1 })
	e.Clock.Add(29 * time.Minute)
	e.Sweep()
	if e.Proc(id).SilentSince != "" {
		t.Fatal("29 minutes without output is not silent yet")
	}
	e.Clock.Add(2 * time.Minute)
	e.Sweep()
	if got := e.Proc(id).SilentSince; got != harness.T0.UTC().Format("2006-01-02T15:04:05.000Z") {
		t.Fatalf("silent since the last output at 08:00, got %q", got)
	}
	close(more)
	e.Until("new output to clear silence", func() bool { return e.Proc(id).SilentSince == "" })
}

func TestQuestionWaitEndsByTheClock(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Req.Resume {
			return provider.Result{Structured: json.RawMessage(`{"n": 2}`)}
		}
		if d := askQuestion(ctx, c, "Go on?"); d["behavior"] == "allow" {
			return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
		}
		return provider.Result{Text: "waiting"}
	}), harness.WithClock(), harness.WithConfig(

		func(c *app.Config) { c.QuestionWait = 30 * time.Minute }))

	p := e.Project("ask", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	live := e.Run(p, "job")
	a := e.WaitAsk()
	e.Clock.Add(20 * time.Minute)
	// The wait of the question does not end at 20 minutes.
	time.Sleep(100 * time.Millisecond)
	e.Answer(a.ID, "yes")
	if got := e.WaitState(live, "done"); string(got.Result) != `{"n":1}` {
		t.Fatalf("an answer within 30 minutes continues the same turn: %+v", got)
	}
	late := e.Run(p, "job")
	a = e.WaitAsk()
	e.Clock.Add(31 * time.Minute)
	e.WaitState(late, "waiting_answer")
	e.Answer(a.ID, "yes")
	if got := e.WaitState(late, "done"); string(got.Result) != `{"n":2}` || !e.Fake.Calls()[len(e.Fake.Calls())-1].Resume {
		t.Fatalf("after 30 minutes by the clock the session parks and the answer resumes it: %+v", got)
	}
}

func TestStopEndsRunningAndParkedProcesses(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if strings.Contains(c.Req.Prompt, "Do ask.") {
			askQuestion(ctx, c, "Which?")
			return provider.Result{Text: "waiting"}
		}
		<-ctx.Done()
		return provider.Result{}
	}), harness.WithClock())

	p := e.Project("stop", harness.TestManifest([]string{harness.AgentCommand("job", ""), harness.AgentCommand("ask", "")}, nil))
	running := e.Run(p, "job")
	e.Until("the turn to start", func() bool { return len(e.Fake.Calls()) == 1 })
	e.Must("deck.process_stop", map[string]any{"process": running}, nil)
	if got := e.WaitState(running, "stopped"); !strings.Contains(got.Error, "stopped by owner") {
		t.Fatalf("stop ends a running agent: %+v", got)
	}
	parked := e.Run(p, "ask")
	e.PassState(parked, "waiting_answer")
	e.Must("deck.process_stop", map[string]any{"process": parked}, nil)
	e.WaitState(parked, "stopped")
	if _, out := e.Call("deck.asks_list", nil); strings.Contains(string(out), "Which?") {
		t.Fatalf("a stopped process has no open asks: %s", out)
	}
	e.Refused("deck.process_stop", map[string]any{"process": parked}, 422, "already finished")
}

func TestLogSurvivesARestartAndTheClose(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	started := make(chan struct{})
	fake := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		c.Req.OnEvent(provider.Event{Kind: "text", Text: "first"})
		close(started)
		<-ctx.Done()
		return provider.Result{}
	}}
	e1 := harness.New(t, harness.WithHome(home), harness.WithProvider(fake))
	p := e1.Project("logs", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	id := e1.Run(p, "job")
	<-started
	if l := e1.Log(id); len(l) != 1 || l[0]["text"] != "first" {
		t.Fatalf("the log has the events of the process: %v", l)
	}
	_ = e1.App.Close()
	stream := e1.App.Runner.LogPath(id)
	if _, err := os.Stat(stream); err != nil {
		t.Fatalf("the log of an open process survives a restart: %v", err)
	}
	fake2 := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		c.Req.OnEvent(provider.Event{Kind: "text", Text: "second"})
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}}
	e2 := harness.New(t, harness.WithHome(home), harness.WithProvider(fake2))
	e2.Resume()
	e2.WaitState(id, "done")
	if l := e2.Log(id); len(l) != 2 || l[1]["text"] != "second" {
		t.Fatalf("a closed process keeps its log, both turns of it: %v", l)
	}
}

func TestLeftoverCLIGroupIsKilledBeforeResume(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "ok.jsonl")
	home := t.TempDir()
	e1 := harness.New(t, harness.WithHome(home), harness.WithProvider(c.Adapter(home)))
	p := e1.Project("plain", harness.PlainManifest)
	var pgids []int
	group := func(args ...string) chan struct{} {
		cmd := exec.Command("sh", append([]string{"-c", "sleep 300; true"}, args...)...)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		pgids = append(pgids, cmd.Process.Pid)
		gone := make(chan struct{})
		go func() { _ = cmd.Wait(); close(gone) }()
		t.Cleanup(func() { _ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) })
		return gone
	}
	ours := group("claude", "--session-id", "11111111-1111-1111-1111-111111111111")
	foreign := group("someone-else")
	removed := group("goose", "deck session 44444444-4444-4444-4444-444444444444")
	orphan := exec.Command("sh", "-c", "sleep 300 & exit 0")
	orphan.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := orphan.Run(); err != nil {
		t.Fatal(err)
	}
	pgids = append(pgids, orphan.Process.Pid)
	t.Cleanup(func() { _ = syscall.Kill(-orphan.Process.Pid, syscall.SIGKILL) })
	var id int64
	err := e1.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		pr, err := q.CreateProcess(ctx, db.CreateProcessParams{StartedAt: store.Time(time.Now()), ProjectID: p, Command: "work", State: "running", Provider: "claude-code",
			Input: "{}", SessionID: "11111111-1111-1111-1111-111111111111", Depth: 1, CeilingMs: time.Hour.Milliseconds()})
		if err != nil {
			return err
		}
		id = pr.ID
		if err := q.SetProcessGroup(ctx, db.SetProcessGroupParams{Pgid: int64(pgids[0]), ID: pr.ID}); err != nil {
			return err
		}
		done, err := q.CreateProcess(ctx, db.CreateProcessParams{StartedAt: store.Time(time.Now()), ProjectID: p, Command: "work", State: "done", Input: "{}",
			SessionID: "22222222-2222-2222-2222-222222222222", Depth: 1})
		if err != nil {
			return err
		}
		if err := q.SetProcessGroup(ctx, db.SetProcessGroupParams{Pgid: int64(pgids[1]), ID: done.ID}); err != nil {
			return err
		}
		other, err := q.CreateProcess(ctx, db.CreateProcessParams{StartedAt: store.Time(time.Now()), ProjectID: p, Command: "work", State: "done", Input: "{}",
			SessionID: "33333333-3333-3333-3333-333333333333", Depth: 1})
		if err != nil {
			return err
		}
		if err := q.SetProcessGroup(ctx, db.SetProcessGroupParams{Pgid: int64(pgids[3]), ID: other.ID}); err != nil {
			return err
		}
		gone, err := q.CreateProcess(ctx, db.CreateProcessParams{StartedAt: store.Time(time.Now()), ProjectID: p, Command: "work", State: "done", Input: "{}", Provider: "removed",
			SessionID: "44444444-4444-4444-4444-444444444444", Depth: 1})
		if err != nil {
			return err
		}
		return q.SetProcessGroup(ctx, db.SetProcessGroupParams{Pgid: int64(pgids[2]), ID: gone.ID})
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = e1.App.Close()

	e2 := harness.New(t, harness.WithHome(home), harness.WithProvider(c.Adapter(home)))
	e2.Resume()
	harness.Receive(t, "the kill of the CLI group a crashed deck left, before the session resumes", ours)
	harness.Receive(t, "the kill of the group of a provider the owner removed since", removed)
	select {
	case <-foreign:
		t.Fatal("a group that does not run the session is not ours and stays")
	default:
	}
	if syscall.Kill(-pgids[3], 0) != nil {
		t.Fatal("a group whose leader is gone and whose members do not run the session stays")
	}
	e2.WaitState(id, "done")
	if sid, ok := harness.Flag(c.Call(1).Args, "--resume"); !ok || sid != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("then the session resumes: %v", c.Call(1).Args)
	}
	if pr, _ := e2.App.Store.Q.GetProcess(context.Background(), id); pr.Pgid != 0 {
		t.Fatalf("a finished turn leaves no group behind: %d", pr.Pgid)
	}
}

func TestClaudeCodeEmptyResultIsNull(t *testing.T) {
	t.Parallel()
	c := harness.NewCLI(t)
	c.Stream(0, "empty_result.jsonl")
	e := harness.New(t, harness.WithCLI(c))
	p := e.Project("quiet", harness.Nullable(harness.TestManifest([]string{harness.AgentCommand("check", ""), harness.AgentCommand("notify", harness.ProcessField)},
		[]string{`{"command": "notify", "after": "check"}`})))
	id := e.Run(p, "check")
	got := e.WaitState(id, "done", "error")
	e.Settle()
	if got.State != "done" || string(got.Result) != "null" || e.CountOf("notify") != 0 {
		t.Fatalf("a null result of the CLI is an empty result: %+v", got)
	}
	in := c.Call(1).Stdin
	if !strings.Contains(in, `"type":["object","null"]`) || !strings.Contains(in, `"required":["result"]`) || !strings.Contains(in, "nothing to do, the result is null") {
		t.Fatalf("the CLI gets an object schema with a nullable result: %s", in)
	}
}

func TestCeilingDoesNotTickWhileParked(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if !c.Req.Resume {
			c.Approve(ctx, "Bash", map[string]any{"command": "make"})
			return provider.Result{Text: "waiting"}
		}
		<-ctx.Done()
		return provider.Result{}
	}), harness.WithClock(), harness.WithConfig(

		func(c *app.Config) { c.ConfirmWait = time.Minute }))

	p := e.Project("park", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	id := e.Run(p, "job")
	a := e.WaitAsk()
	e.Clock.Add(2 * time.Minute)
	e.WaitState(id, "waiting_answer")
	e.Clock.Add(3 * time.Hour)
	e.Answer(a.ID, "allow")
	e.Until("the resumed turn", func() bool { return len(e.Fake.Calls()) == 2 && e.Proc(id).Deadline != "" })
	e.Sweep()
	if got := e.Proc(id); got.State != "running" {
		t.Fatalf("hours parked for an answer do not count against the ceiling: %+v", got)
	}
}

func TestCeilingDoesNotCountTheTimeDeckWasDown(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	clock := &harness.Clock{At: harness.T0}
	blocked := func(ctx context.Context, c *harness.Call) provider.Result {
		if c.N == 1 {
			c.Req.OnEvent(provider.Event{Kind: "text", Text: "working"})
		}
		<-ctx.Done()
		return provider.Result{}
	}
	e1 := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		clock.Add(10 * time.Minute)
		return blocked(ctx, c)
	}}), harness.WithClock(clock))

	p := e1.Project("long", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	id := e1.Run(p, "job")
	e1.Until("the first output", func() bool { return len(e1.Log(id)) == 1 })
	_ = e1.App.Close()
	clock.Add(2 * time.Hour)
	e2 := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{Script: blocked}), harness.WithClock(clock))
	e2.Resume()
	e2.Until("the resumed ceiling and a sign of the model", func() bool { return e2.Proc(id).Deadline != "" && len(e2.Log(id)) == 2 })
	clock.Add(49 * time.Minute)
	e2.Sweep()
	if got := e2.Proc(id); got.State != "running" {
		t.Fatalf("the two hours deck was down do not count against the ceiling: %+v", got)
	}
	clock.Add(2 * time.Minute)
	e2.Sweep()
	e2.WaitState(id, "error")
}

func TestCeilingRunsAgainWhenAQuestionGoesUnanswered(t *testing.T) {
	t.Parallel()
	asked := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		askQuestion(ctx, c, "Which?")
		close(asked)
		<-ctx.Done()
		return provider.Result{}
	}), harness.WithClock(), harness.WithConfig(

		func(c *app.Config) { c.QuestionWait = 30 * time.Minute }))

	p := e.Project("ask", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	id := e.Run(p, "job")
	e.WaitAsk()
	e.Until("the ceiling to pause", func() bool { return e.Proc(id).Deadline == "" })
	e.Clock.Add(31 * time.Minute)
	e.Until("the ceiling to run again once the wait ends and the agent hears it", func() bool {
		select {
		case <-asked:
			return e.Proc(id).Deadline != ""
		default:
			return false
		}
	})
	e.Clock.Add(61 * time.Minute)
	e.Sweep()
	e.WaitState(id, "error")
}

func TestReworkKeepsTheRetryKeyUnique(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.N == 2 {
			<-release
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))

	defer close(release)
	e.Project("jobs", harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil))
	var first, second struct{ Process int64 }
	e.Must("jobs.work", map[string]any{"retry_key": "a"}, &first)
	e.WaitState(first.Process, "done")
	e.Must("jobs.work", map[string]any{"retry_key": "a"}, &second)
	e.Refused("deck.process_rework", map[string]any{"process": first.Process, "remark": "again"}, 422, "same retry key")
}

// TestProcessGetWaitsForTheEnd: with wait the status comes once the process finishes, so a caller does not poll.
func TestProcessGetWaitsForTheEnd(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		<-release
		return provider.Result{Structured: json.RawMessage(`{"written": 1}`)}
	}))

	id := e.Run(e.Project("ideas", ideasManifest), "collect")
	time.AfterFunc(time.Second, func() { close(release) })
	var p struct{ State string }
	began := time.Now()
	e.Must("deck.process_get", map[string]any{"process": id, "wait": 30}, &p)
	if p.State != "done" || time.Since(began) < time.Second || time.Since(began) > 10*time.Second {
		t.Fatalf("the status comes when the process is done: %+v after %s", p, time.Since(began))
	}
}

// TestHistoryFindsAnOldRunOfAQuietProject: the filter of the history narrows the query, so an old run behind
// hundreds of newer ones of another project is still found.
func TestHistoryFindsAnOldRunOfAQuietProject(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	quiet := e.Project("quiet", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	busy := e.Project("busy", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	old := e.Run(quiet, "job")
	e.WaitState(old, "done")
	for i := 0; i < 205; i++ {
		e.Run(busy, "job")
	}
	e.Until("the busy runs", func() bool {
		for _, p := range e.Procs() {
			if p.State != "done" {
				return false
			}
		}
		return true
	})
	history := e.ScreenData("", "schedule", map[string]any{"filter": `{"b0-1-0": {"project": "quiet", "state": "done"}}`})["b0-1-0"]
	rows := harness.BlockData[[]harness.ScreenRow](t, history["rows"])
	if len(rows) != 1 || rows[0].ID != float64(old) || !strings.Contains(string(history["filters"]), `"options":["busy","quiet"]`) {
		t.Fatalf("the history finds the run of the quiet project, and the picked project stays a choice: %+v %s", rows, history["filters"])
	}
}

func TestFinishedProcessSessionIsClosed(t *testing.T) {
	t.Parallel()
	var url, sid string
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		url, sid = c.Req.MCPURL, c.Session.ID()
		return provider.Result{Text: "ok"}
	}))

	p := e.Project("nums", harness.CalcManifest)
	e.WaitState(e.Run(p, "calc"), "done")
	body := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"records_upsert","arguments":{"collection":"nums","records":"{\"n\": 5}"}}}`
	req, _ := http.NewRequest("POST", url, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Mcp-Session-Id", sid)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	recs, _ := e.App.Store.Q.ListRecords(context.Background(), db.ListRecordsParams{ProjectID: p, Collection: "nums"})
	if resp.StatusCode != http.StatusNotFound || len(recs) != 0 {
		t.Fatalf("a finished process's session takes no calls: %d, %d records", resp.StatusCode, len(recs))
	}
}

// TestCeilingWaitsForAConfirmationToo: while the owner decides on a call the agent waits for, its ceiling does
// not tick, as for a question.
func TestCeilingWaitsForAConfirmationToo(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		c.Approve(ctx, "Bash", map[string]any{"command": "curl https://example.com"})
		<-ctx.Done()
		return provider.Result{}
	}), harness.WithClock(), harness.WithConfig(

		func(c *app.Config) { c.ConfirmWait = 3 * time.Hour }))

	p := e.Project("ask", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	id := e.Run(p, "job")
	a := e.WaitAsk()
	e.Until("the ceiling to pause", func() bool { return e.Proc(id).Deadline == "" })
	e.Clock.Add(2 * time.Hour)
	e.Sweep()
	if got := e.Proc(id); got.State != "running" {
		t.Fatalf("while the owner decides on a call the ceiling does not tick: %+v", got)
	}
	e.Answer(a.ID, "deny")
	e.Until("the ceiling to run again", func() bool { return e.Proc(id).Deadline != "" })
}

// TestNullIsAResultOnlyWhereTheSchemaAllowsIt: an agent's null is nothing to do only when the result schema takes
// null; against a schema of an object it is a result not by the schema.
func TestNullIsAResultOnlyWhereTheSchemaAllowsIt(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`null`)}
	}))

	p := e.Project("strict", harness.TestManifest([]string{harness.AgentCommand("check", "")}, nil))
	if got := e.Proc(e.WaitState(e.Run(p, "check"), "done", "error").ID); !got.NotBySchema || got.Empty {
		t.Fatalf("null against a schema of an object is not by the schema: %+v", got)
	}
	m := harness.TestManifest([]string{harness.AgentCommand("check", "")}, nil)
	nullable := strings.Replace(m, `"result": {"type": "object",`, `"result": {"type": ["object", "null"],`, 1)
	if nullable == m {
		t.Fatal("fixture replacement did not apply")
	}
	open := e.Project("open", nullable)
	if got := e.Proc(e.WaitState(e.Run(open, "check"), "done", "error").ID); got.NotBySchema || !got.Empty {
		t.Fatalf("null against a schema that takes null is nothing to do: %+v", got)
	}
}

// TestACallThatOutlivesItsTurnLeavesAParkedProcessWithoutACeiling: an approval that is decided after the turn
// ended and the process parked does not start the ceiling of the parked process, which would count its wait.
func TestACallThatOutlivesItsTurnLeavesAParkedProcessWithoutACeiling(t *testing.T) {
	t.Parallel()
	returned := make(chan struct{})
	var asked atomic.Bool
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.N > 1 {
			return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
		}
		client := mcp.NewClient(&mcp.Implementation{Name: "late", Version: "0"}, nil)
		sess, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: c.Req.MCPURL}, nil)
		if err != nil {
			c.Fake.Broke(err.Error())
			return provider.Result{}
		}
		go func() {
			defer close(returned)
			defer sess.Close()
			_, _ = sess.CallTool(context.Background(), &mcp.CallToolParams{Name: "approve",
				Arguments: map[string]any{"tool_name": "Bash", "input": map[string]any{"command": "curl https://example.com"}, "tool_use_id": "t1"}})
		}()
		harness.Until(t, "the owner to be asked", asked.Load)
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}), harness.WithClock(), harness.WithConfig(

		func(c *app.Config) { c.ConfirmWait = time.Hour }))

	p := e.Project("ask", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
	id := e.Run(p, "job")
	a := e.WaitAsk()
	asked.Store(true)
	e.WaitState(id, "waiting_answer")
	e.Answer(a.ID, "deny")
	<-returned
	e.Settle()
	if got := e.Proc(id); got.State == "waiting_answer" && got.Deadline != "" {
		t.Fatalf("a parked process has no ceiling running: %+v", got)
	}
}

// TestTheQuestionToolIsTheProvidersOwn: the permission prompt of a call of the provider's own question tool, by
// whatever name, is a question to the owner, not a call to confirm.
func TestTheQuestionToolIsTheProvidersOwn(t *testing.T) {
	t.Parallel()
	got := make(chan map[string]any, 1)
	fake := &harness.Fake{Question: "AskOwner", Script: func(ctx context.Context, c *harness.Call) provider.Result {
		got <- c.Approve(ctx, "AskOwner", map[string]any{"questions": []any{map[string]any{"question": "Which color?"}}})
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}}
	e := harness.New(t, harness.WithProvider(fake))
	e.Run(e.Project("ask", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil)), "job")
	a := e.WaitAsk()
	if a.Kind != "question" || a.Text != "Which color?" {
		t.Fatalf("the question tool of the provider asks the owner: %+v", a)
	}
	e.Answer(a.ID, "blue")
	if d := <-got; d["behavior"] != "allow" || !strings.Contains(fmt.Sprint(d["updatedInput"]), "blue") {
		t.Fatalf("the answer goes back to the question tool: %v", d)
	}
}

// TestFilesATurnLeftPastTheLimitsAreToldOf: of the files a turn leaves, deck keeps visible ones up to its limits and
// tells the owner how many of how many it kept.
func TestFilesATurnLeftPastTheLimitsAreToldOf(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		results := filepath.Join(c.Req.Cwd, "..", "results")
		for i := range 52 {
			_ = os.WriteFile(filepath.Join(results, fmt.Sprintf("f%02d.txt", i)), []byte("file"), 0o600)
		}
		_ = os.WriteFile(filepath.Join(results, ".secret"), []byte("x"), 0o600)
		_ = os.MkdirAll(filepath.Join(results, ".cache"), 0o700)
		_ = os.WriteFile(filepath.Join(results, ".cache", "x"), []byte("x"), 0o600)
		return provider.Result{Text: "done"}
	}))

	e.WaitState(e.Run(e.Project("plain", harness.PlainManifest), "work"), "done")
	if n := e.WaitNote("deck kept 50 of 54 files"); !strings.Contains(n.Text, "hidden") {
		t.Fatalf("the note says why: %+v", n)
	}
}

// TestAnAgentHearsItsCeilingNearAndTheErrorNamesWhatStillRuns: an agent is told its ceiling nears, a wait ends
// before it, and the error at the ceiling names the processes the agent started that still run.
func TestAnAgentHearsItsCeilingNearAndTheErrorNamesWhatStillRuns(t *testing.T) {
	t.Parallel()
	told, proceed, waited := make(chan string, 1), make(chan struct{}), make(chan time.Duration, 1)
	child := make(chan int64, 1)
	fake := &harness.Fake{Live: true, Script: func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "child" {
			c.Req.Emit(provider.Event{Kind: provider.EventThought, Text: "working"})
			<-ctx.Done()
			return provider.Result{}
		}
		out, _ := c.Tool(ctx, "long__child", map[string]any{})
		var started struct{ Process int64 }
		_ = json.Unmarshal([]byte(out), &started)
		child <- started.Process
		told <- heard(ctx, c)
		<-proceed
		at := time.Now()
		c.Tool(ctx, "process_get", map[string]any{"process": started.Process, "wait": 1800})
		waited <- time.Since(at)
		<-ctx.Done()
		return provider.Result{}
	}}
	clock := &harness.Clock{At: harness.T0}
	e := harness.New(t, harness.WithProvider(fake), harness.WithClock(clock))
	p := e.Project("long", harness.TestManifest([]string{harness.AgentCommand("parent", ""), harness.AgentCommand("child", "")}, nil))
	id := e.Run(p, "parent")
	kid := <-child
	e.Until("the ceiling to run and the child to give a sign", func() bool { return e.Proc(id).Deadline != "" && len(e.Log(kid)) == 1 })
	e.Clock.Add(51 * time.Minute)
	e.Sweep()
	if got := <-told; !strings.Contains(got, "time ceiling in 9 minutes") {
		t.Fatalf("the agent hears its ceiling near: %q", got)
	}
	e.Clock.Add(8 * time.Minute)
	close(proceed)
	if d := <-waited; d > 5*time.Second {
		t.Fatalf("a wait ends before the ceiling of the process that waits, not after %s", d)
	}
	e.Clock.Add(2 * time.Minute)
	e.Sweep()
	got := e.WaitState(id, "error")
	if want := fmt.Sprintf("the processes it started still run: #%d child", kid); !strings.Contains(got.Error, want) {
		t.Fatalf("the error at the ceiling names what still runs: %q", got.Error)
	}
}
