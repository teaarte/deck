package scenarios

import (
	"context"
	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/internal/store/db"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAgentCallsReturnAtOnceAndRetryKeyReusesTheProcess(t *testing.T) {
	release := make(chan struct{})
	ids := make(chan []string, 1)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "work" {
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
	})
	p := e.createProject("jobs", testManifest([]string{agentCommand("fan", ""),
		agentCommand("work", `{"type": "object", "properties": {"item": {"type": "string"}}, "additionalProperties": false}`)}, nil))
	fan := e.run(p, "fan")
	var got []string
	select {
	case got = <-ids:
	case <-time.After(10 * time.Second):
		t.Fatal("a command called by an agent must return its process id without waiting for it")
	}
	e.waitState(fan, "done")
	if got[0] != got[1] || got[0] == got[2] || !strings.Contains(got[0], `"process"`) {
		t.Fatalf("the same key returns the same process, another key another one: %v", got)
	}
	var first struct{ Process int64 }
	_ = json.Unmarshal([]byte(got[0]), &first)
	if pr := e.full(first.Process); pr.Parent != fan || pr.Depth != 2 || pr.State != "running" {
		t.Fatalf("the called process belongs to its caller and is still running: %+v", pr)
	}
	var again struct{ Process int64 }
	e.must("jobs.work", map[string]any{"item": "a", "retry_key": "a"}, &again)
	if again.Process != first.Process {
		t.Fatalf("the owner gets the same process for the same key: %d vs %d", again.Process, first.Process)
	}
	close(release)
	e.waitState(first.Process, "done")
	e.must("jobs.work", map[string]any{"item": "a", "retry_key": "a"}, &again)
	if again.Process == first.Process {
		t.Fatal("a finished process does not hold its key")
	}
	e.waitState(again.Process, "done")
	if in := processInput(t, e, again.Process); in["retry_key"] != nil || in["item"] != "a" {
		t.Fatalf("the retry key is not part of the command input: %v", in)
	}
}

func TestReworkContinuesTheSessionAndRevisesTheArtifact(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		text := "report v1"
		if c.Req.Resume {
			text = "report v2"
		}
		c.Tool(ctx, "document_save", map[string]any{"name": "report.md", "kind": "artifact", "text": text})
		return provider.Result{Structured: json.RawMessage(fmt.Sprintf(`{"n": %d}`, c.N))}
	})
	p := e.createProject("docs", testManifest([]string{agentCommand("write", "")}, nil))
	id := e.run(p, "write")
	e.waitState(id, "done")
	var started struct{ Process int64 }
	e.must("deck.process_rework", map[string]any{"process": id, "remark": "make it shorter"}, &started)
	e.waitFor("the rework to finish", func() bool { return e.full(id).State == "done" && len(e.Fake.Calls()) == 2 })
	calls := e.Fake.Calls()
	if started.Process != id || !calls[1].Resume || calls[1].SessionID != calls[0].SessionID || !strings.Contains(calls[1].Prompt, "make it shorter") {
		t.Fatalf("rework resumes the same session with the remark: %+v", calls[1])
	}
	if got := e.full(id); string(got.Result) != `{"n":2}` {
		t.Fatalf("the rework gives the new result: %+v", got)
	}
	var d struct{ ID int64 }
	e.must("deck.document_get", map[string]any{"project": "docs", "name": "report.md"}, &d)
	var revs []struct{ Author string }
	e.must("deck.document_revisions", map[string]any{"document": d.ID}, &revs)
	if len(revs) != 2 {
		t.Fatalf("the rework makes a new revision of the artifact, got %v", revs)
	}
	e.refused("deck.process_rework", map[string]any{"process": id, "remark": " "}, 422, "remark")
}

func TestCeilingStopsTheAgentAndManifestOverridesIt(t *testing.T) {
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		<-ctx.Done()
		return provider.Result{}
	})
	p := e.createProject("long", testManifest([]string{agentCommand("short", ""),
		strings.Replace(agentCommand("long", ""), `"class"`, `"timeout_minutes": 120, "class"`, 1)}, nil))
	a, b := e.run(p, "short"), e.run(p, "long")
	e.waitFor("both ceilings to run", func() bool { return e.full(a).Deadline != "" && e.full(b).Deadline != "" })
	e.Clock.Add(59 * time.Minute)
	e.sweep()
	if e.full(a).State != "running" {
		t.Fatal("the ceiling of a command agent is an hour")
	}
	e.Clock.Add(2 * time.Minute)
	e.sweep()
	got := e.waitState(a, "stopped")
	if !strings.Contains(got.Error, "ceiling") || e.full(b).State != "running" {
		t.Fatalf("the ceiling stops the agent; a manifest ceiling of 2 h holds: %+v %+v", got, e.full(b))
	}
	e.Clock.Add(time.Hour)
	e.sweep()
	e.waitState(b, "stopped")
}

func TestCeilingAndSilenceWaitForABlockingQuestion(t *testing.T) {
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		askQuestion(ctx, c, "Which one?")
		<-ctx.Done()
		return provider.Result{}
	}, func(c *app.Config) { c.QuestionWait = 3 * time.Hour })
	p := e.createProject("ask", testManifest([]string{agentCommand("job", "")}, nil))
	id := e.run(p, "job")
	a := e.waitAsk()
	e.waitFor("the ceiling to pause", func() bool { return e.full(id).Deadline == "" })
	e.Clock.Add(2 * time.Hour)
	e.sweep()
	if got := e.full(id); got.State != "running" || got.SilentSince != "" {
		t.Fatalf("while the owner owes an answer the ceiling does not tick and the process is not silent: %+v", got)
	}
	e.answer(a.ID, "the first")
	e.waitFor("the ceiling to run again", func() bool { return e.full(id).Deadline != "" })
	e.Clock.Add(50 * time.Minute)
	e.sweep()
	if e.full(id).State != "running" {
		t.Fatal("the time of the question does not count against the ceiling")
	}
	e.Clock.Add(20 * time.Minute)
	e.sweep()
	e.waitState(id, "stopped")
}

func TestSilentProcessShowsSinceItsLastOutput(t *testing.T) {
	more := make(chan struct{})
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		c.Req.OnEvent(provider.Event{Kind: "text", Text: "working"})
		<-more
		c.Req.OnEvent(provider.Event{Kind: "tool", Text: "Read"})
		<-ctx.Done()
		return provider.Result{}
	})
	p := e.createProject("quiet", testManifest([]string{agentCommand("job", "")}, nil))
	id := e.run(p, "job")
	e.waitFor("the first output", func() bool { return len(e.log(id)) == 1 })
	e.Clock.Add(29 * time.Minute)
	e.sweep()
	if e.full(id).SilentSince != "" {
		t.Fatal("29 minutes without output is not silent yet")
	}
	e.Clock.Add(2 * time.Minute)
	e.sweep()
	if got := e.full(id).SilentSince; got != t0.UTC().Format("2006-01-02T15:04:05.000Z") {
		t.Fatalf("silent since the last output at 08:00, got %q", got)
	}
	close(more)
	e.waitFor("new output to clear silence", func() bool { return e.full(id).SilentSince == "" })
}

func (e *Env) log(id int64) []map[string]any {
	var l []map[string]any
	e.must("deck.process_log", map[string]any{"process": id}, &l)
	return l
}

func TestQuestionWaitEndsByTheClock(t *testing.T) {
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.Req.Resume {
			return provider.Result{Structured: json.RawMessage(`{"n": 2}`)}
		}
		if d := askQuestion(ctx, c, "Go on?"); d["behavior"] == "allow" {
			return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
		}
		return provider.Result{Text: "waiting"}
	}, func(c *app.Config) { c.QuestionWait = 30 * time.Minute })
	p := e.createProject("ask", testManifest([]string{agentCommand("job", "")}, nil))
	live := e.run(p, "job")
	a := e.waitAsk()
	e.Clock.Add(20 * time.Minute)
	time.Sleep(100 * time.Millisecond)
	e.answer(a.ID, "yes")
	if got := e.waitState(live, "done"); string(got.Result) != `{"n":1}` {
		t.Fatalf("an answer within 30 minutes continues the same turn: %+v", got)
	}
	late := e.run(p, "job")
	a = e.waitAsk()
	e.Clock.Add(31 * time.Minute)
	e.waitState(late, "waiting_answer")
	e.answer(a.ID, "yes")
	if got := e.waitState(late, "done"); string(got.Result) != `{"n":2}` || !e.Fake.Calls()[len(e.Fake.Calls())-1].Resume {
		t.Fatalf("after 30 minutes by the clock the session parks and the answer resumes it: %+v", got)
	}
}

func TestStopEndsRunningAndParkedProcesses(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if strings.Contains(c.Req.Prompt, "Do ask.") {
			askQuestion(ctx, c, "Which?")
			return provider.Result{Text: "waiting"}
		}
		<-ctx.Done()
		return provider.Result{}
	}, short)
	p := e.createProject("stop", testManifest([]string{agentCommand("job", ""), agentCommand("ask", "")}, nil))
	running := e.run(p, "job")
	e.waitFor("the turn to start", func() bool { return len(e.Fake.Calls()) == 1 })
	e.must("deck.process_stop", map[string]any{"process": running}, nil)
	if got := e.waitState(running, "stopped"); !strings.Contains(got.Error, "stopped by owner") {
		t.Fatalf("stop ends a running agent: %+v", got)
	}
	parked := e.run(p, "ask")
	e.waitState(parked, "waiting_answer")
	e.must("deck.process_stop", map[string]any{"process": parked}, nil)
	e.waitState(parked, "stopped")
	if _, out := e.call("deck.asks_list", nil); strings.Contains(string(out), "Which?") {
		t.Fatalf("a stopped process has no open asks: %s", out)
	}
	e.refused("deck.process_stop", map[string]any{"process": parked}, 422, "already finished")
}

func TestLogSurvivesARestartAndTheClose(t *testing.T) {
	home := t.TempDir()
	started := make(chan struct{})
	fake := &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		c.Req.OnEvent(provider.Event{Kind: "text", Text: "first"})
		close(started)
		<-ctx.Done()
		return provider.Result{}
	}}
	e1 := newEnvWith(t, home, fake)
	e1.Fake = fake
	p := e1.createProject("logs", testManifest([]string{agentCommand("job", "")}, nil))
	id := e1.run(p, "job")
	<-started
	if l := e1.log(id); len(l) != 1 || l[0]["text"] != "first" {
		t.Fatalf("the log has the events of the process: %v", l)
	}
	_ = e1.App.Close()
	stream := e1.App.Runner.LogPath(id)
	if _, err := os.Stat(stream); err != nil {
		t.Fatalf("the log of an open process survives a restart: %v", err)
	}
	fake2 := &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		c.Req.OnEvent(provider.Event{Kind: "text", Text: "second"})
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}}
	e2 := newEnvWith(t, home, fake2)
	if err := e2.App.Runner.ResumeInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	e2.waitState(id, "done")
	if l := e2.log(id); len(l) != 2 || l[1]["text"] != "second" {
		t.Fatalf("a closed process keeps its log, both turns of it: %v", l)
	}
}

func TestLeftoverCLIGroupIsKilledBeforeResume(t *testing.T) {
	c := newCLI(t)
	c.stream(0, "ok.jsonl")
	home := t.TempDir()
	e1 := newEnvWith(t, home, c.adapter(home))
	p := e1.createProject("plain", plainManifest)
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
		pr, err := q.CreateProcess(ctx, db.CreateProcessParams{ProjectID: p, Command: "work", State: "running", Provider: "claude-code",
			Input: "{}", SessionID: "11111111-1111-1111-1111-111111111111", Depth: 1, CeilingMs: time.Hour.Milliseconds()})
		if err != nil {
			return err
		}
		id = pr.ID
		if err := q.SetProcessGroup(ctx, db.SetProcessGroupParams{Pgid: int64(pgids[0]), ID: pr.ID}); err != nil {
			return err
		}
		done, err := q.CreateProcess(ctx, db.CreateProcessParams{ProjectID: p, Command: "work", State: "done", Input: "{}",
			SessionID: "22222222-2222-2222-2222-222222222222", Depth: 1})
		if err != nil {
			return err
		}
		if err := q.SetProcessGroup(ctx, db.SetProcessGroupParams{Pgid: int64(pgids[1]), ID: done.ID}); err != nil {
			return err
		}
		other, err := q.CreateProcess(ctx, db.CreateProcessParams{ProjectID: p, Command: "work", State: "done", Input: "{}",
			SessionID: "33333333-3333-3333-3333-333333333333", Depth: 1})
		if err != nil {
			return err
		}
		if err := q.SetProcessGroup(ctx, db.SetProcessGroupParams{Pgid: int64(pgids[3]), ID: other.ID}); err != nil {
			return err
		}
		gone, err := q.CreateProcess(ctx, db.CreateProcessParams{ProjectID: p, Command: "work", State: "done", Input: "{}", Provider: "removed",
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

	e2 := newEnvWith(t, home, c.adapter(home))
	if err := e2.App.Runner.ResumeInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	select {
	case <-ours:
	case <-time.After(3 * time.Second):
		t.Fatal("the CLI group a crashed deck left must be killed before the session resumes")
	}
	select {
	case <-removed:
	case <-time.After(3 * time.Second):
		t.Fatal("the group of a provider the owner removed since is killed too")
	}
	select {
	case <-foreign:
		t.Fatal("a group that does not run the session is not ours and stays")
	default:
	}
	if syscall.Kill(-pgids[3], 0) != nil {
		t.Fatal("a group whose leader is gone and whose members do not run the session stays")
	}
	e2.waitState(id, "done")
	if sid, ok := flag(c.call(1).args, "--resume"); !ok || sid != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("then the session resumes: %v", c.call(1).args)
	}
	if pr, _ := e2.App.Store.Q.GetProcess(context.Background(), id); pr.Pgid != 0 {
		t.Fatalf("a finished turn leaves no group behind: %d", pr.Pgid)
	}
}

func TestClaudeCodeEmptyResultIsNull(t *testing.T) {
	c := newCLI(t)
	c.stream(0, "empty_result.jsonl")
	e, _ := cliEnv(t, c)
	p := e.createProject("quiet", nullable(testManifest([]string{agentCommand("check", ""), agentCommand("notify", processField)},
		[]string{`{"command": "notify", "after": "check"}`})))
	id := e.run(p, "check")
	got := e.waitState(id, "done", "error")
	e.settle()
	if got.State != "done" || string(got.Result) != "null" || e.countOf("notify") != 0 {
		t.Fatalf("a null result of the CLI is an empty result: %+v", got)
	}
	in := c.call(1).stdin
	if !strings.Contains(in, `"type":["object","null"]`) || !strings.Contains(in, `"required":["result"]`) || !strings.Contains(in, "nothing to do, the result is null") {
		t.Fatalf("the CLI gets an object schema with a nullable result: %s", in)
	}
}

func TestCeilingDoesNotTickWhileParked(t *testing.T) {
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if !c.Req.Resume {
			c.Approve(ctx, "Bash", map[string]any{"command": "make"})
			return provider.Result{Text: "waiting"}
		}
		<-ctx.Done()
		return provider.Result{}
	}, func(c *app.Config) { c.ConfirmWait = time.Minute })
	p := e.createProject("park", testManifest([]string{agentCommand("job", "")}, nil))
	id := e.run(p, "job")
	a := e.waitAsk()
	e.Clock.Add(2 * time.Minute)
	e.waitState(id, "waiting_answer")
	e.Clock.Add(3 * time.Hour)
	e.answer(a.ID, "allow")
	e.waitFor("the resumed turn", func() bool { return len(e.Fake.Calls()) == 2 && e.full(id).Deadline != "" })
	e.sweep()
	if got := e.full(id); got.State != "running" {
		t.Fatalf("hours parked for an answer do not count against the ceiling: %+v", got)
	}
}

func TestCeilingDoesNotCountTheTimeDeckWasDown(t *testing.T) {
	home := t.TempDir()
	clock := &Clock{now: t0}
	blocked := func(ctx context.Context, c *Call) provider.Result {
		if c.N == 1 {
			c.Req.OnEvent(provider.Event{Kind: "text", Text: "working"})
		}
		<-ctx.Done()
		return provider.Result{}
	}
	e1 := newEnvWith(t, home, &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		clock.Add(10 * time.Minute)
		return blocked(ctx, c)
	}}, withClock(clock))
	p := e1.createProject("long", testManifest([]string{agentCommand("job", "")}, nil))
	id := e1.run(p, "job")
	e1.waitFor("the first output", func() bool { return len(e1.log(id)) == 1 })
	_ = e1.App.Close()
	clock.Add(2 * time.Hour)
	e2 := newEnvWith(t, home, &Fake{script: blocked}, withClock(clock))
	e2.Clock = clock
	if err := e2.App.Runner.ResumeInterrupted(context.Background()); err != nil {
		t.Fatal(err)
	}
	e2.waitFor("the resumed ceiling", func() bool { return e2.full(id).Deadline != "" })
	clock.Add(49 * time.Minute)
	e2.sweep()
	if got := e2.full(id); got.State != "running" {
		t.Fatalf("the two hours deck was down do not count against the ceiling: %+v", got)
	}
	clock.Add(2 * time.Minute)
	e2.sweep()
	e2.waitState(id, "stopped")
}

func TestCeilingRunsAgainWhenAQuestionGoesUnanswered(t *testing.T) {
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		askQuestion(ctx, c, "Which?")
		<-ctx.Done()
		return provider.Result{}
	}, func(c *app.Config) { c.QuestionWait = 30 * time.Minute })
	p := e.createProject("ask", testManifest([]string{agentCommand("job", "")}, nil))
	id := e.run(p, "job")
	e.waitAsk()
	e.waitFor("the ceiling to pause", func() bool { return e.full(id).Deadline == "" })
	e.Clock.Add(31 * time.Minute)
	e.waitFor("the ceiling to run again once the wait ends", func() bool { return e.full(id).Deadline != "" })
	e.Clock.Add(61 * time.Minute)
	e.sweep()
	e.waitState(id, "stopped")
}

func TestReworkKeepsTheRetryKeyUnique(t *testing.T) {
	release := make(chan struct{})
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.N == 2 {
			<-release
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	defer close(release)
	e.createProject("jobs", testManifest([]string{agentCommand("work", "")}, nil))
	var first, second struct{ Process int64 }
	e.must("jobs.work", map[string]any{"retry_key": "a"}, &first)
	e.waitState(first.Process, "done")
	e.must("jobs.work", map[string]any{"retry_key": "a"}, &second)
	e.refused("deck.process_rework", map[string]any{"process": first.Process, "remark": "again"}, 422, "same retry key")
}

// TestProcessGetWaitsForTheEnd: with wait the status comes once the process finishes, so a caller does not poll.
func TestProcessGetWaitsForTheEnd(t *testing.T) {
	release := make(chan struct{})
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		<-release
		return provider.Result{Structured: json.RawMessage(`{"written": 1}`)}
	})
	id := e.run(e.createProject("ideas", ideasManifest), "collect")
	time.AfterFunc(time.Second, func() { close(release) })
	var p struct{ State string }
	began := time.Now()
	e.must("deck.process_get", map[string]any{"process": id, "wait": 30}, &p)
	if p.State != "done" || time.Since(began) < time.Second || time.Since(began) > 10*time.Second {
		t.Fatalf("the status comes when the process is done: %+v after %s", p, time.Since(began))
	}
}

// TestHistoryFindsAnOldRunOfAQuietProject: the filter of the history narrows the query, so an old run behind
// hundreds of newer ones of another project is still found.
func TestHistoryFindsAnOldRunOfAQuietProject(t *testing.T) {
	e := newEnv(t, ok)
	quiet := e.createProject("quiet", testManifest([]string{agentCommand("job", "")}, nil))
	busy := e.createProject("busy", testManifest([]string{agentCommand("job", "")}, nil))
	old := e.run(quiet, "job")
	e.waitState(old, "done")
	for i := 0; i < 205; i++ {
		e.run(busy, "job")
	}
	e.waitFor("the busy runs", func() bool {
		for _, p := range e.processes() {
			if p.State != "done" {
				return false
			}
		}
		return true
	})
	history := e.screenData("", "schedule", map[string]any{"filter": `{"b0-1-0": {"project": "quiet", "state": "done"}}`})["b0-1-0"]
	rows := blockData[[]screenRow](t, history["rows"])
	if len(rows) != 1 || rows[0].Cells[0] != float64(old) || !strings.Contains(string(history["filters"]), `"options":["busy","quiet"]`) {
		t.Fatalf("the history finds the run of the quiet project, and the picked project stays a choice: %+v %s", rows, history["filters"])
	}
}

func TestFinishedProcessSessionIsClosed(t *testing.T) {
	var url, sid string
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		url, sid = c.Req.MCPURL, c.Session.ID()
		return provider.Result{Text: "ok"}
	})
	p := e.createProject("nums", calcManifest)
	e.waitState(e.run(p, "calc"), "done")
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
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		c.Approve(ctx, "Bash", map[string]any{"command": "curl https://example.com"})
		<-ctx.Done()
		return provider.Result{}
	}, func(c *app.Config) { c.ConfirmWait = 3 * time.Hour })
	p := e.createProject("ask", testManifest([]string{agentCommand("job", "")}, nil))
	id := e.run(p, "job")
	a := e.waitAsk()
	e.waitFor("the ceiling to pause", func() bool { return e.full(id).Deadline == "" })
	e.Clock.Add(2 * time.Hour)
	e.sweep()
	if got := e.full(id); got.State != "running" {
		t.Fatalf("while the owner decides on a call the ceiling does not tick: %+v", got)
	}
	e.answer(a.ID, "deny")
	e.waitFor("the ceiling to run again", func() bool { return e.full(id).Deadline != "" })
}

// TestNullIsAResultOnlyWhereTheSchemaAllowsIt: in format 7 an agent's null is nothing to do only when the result
// schema takes null; a module of format 6 is raised so that it still does.
func TestNullIsAResultOnlyWhereTheSchemaAllowsIt(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`null`)}
	})
	p := e.createProject("strict", testManifest([]string{agentCommand("check", "")}, nil))
	if got := e.full(e.waitState(e.run(p, "check"), "done", "error").ID); !got.NotBySchema || got.Empty {
		t.Fatalf("null against a schema of an object is not by the schema: %+v", got)
	}
	old := e.createProject("old", strings.Replace(testManifest([]string{agentCommand("check", "")}, nil), `"version": 9`, `"version": 6`, 1))
	if got := e.full(e.waitState(e.run(old, "check"), "done", "error").ID); got.NotBySchema || !got.Empty {
		t.Fatalf("a module of format 6 still takes null as nothing to do: %+v", got)
	}
	var m struct{ Manifest string }
	e.must("deck.manifest_get", map[string]any{"project": "old"}, &m)
	if !strings.Contains(m.Manifest, `"version": 9`) || !strings.Contains(m.Manifest, `"null"`) {
		t.Fatalf("format 6 is raised to 7 with null in its result schemas:\n%s", m.Manifest)
	}
}

// TestAResultSchemaOfFormatSixWithoutATypeStillLoads: a result schema of format 6 with no type of its own, such as
// one of enum or properties only, is raised to take null too and the module still loads.
func TestAResultSchemaOfFormatSixWithoutATypeStillLoads(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`null`)}
	})
	for i, result := range []string{`{"enum": [1, 2]}`, `{"properties": {"n": {"type": "integer"}}}`} {
		m := strings.Replace(testManifest([]string{agentCommand("check", "")}, nil), `"version": 9`, `"version": 6`, 1)
		m = strings.Replace(m, `"result": {"type": "object", "properties": {"n": {"type": "integer"}}}`, `"result": `+result, 1)
		p := e.createProject(fmt.Sprintf("old%d", i), m)
		if got := e.full(e.waitState(e.run(p, "check"), "done", "error").ID); got.NotBySchema || !got.Empty {
			t.Fatalf("%s: a module of format 6 takes null as nothing to do: %+v", result, got)
		}
	}
}
