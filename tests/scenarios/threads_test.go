package scenarios

import (
	"context"
	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// clipsModule keeps clips: new makes one, check follows it, tally follows check with no record, revise edits a clip,
// render (a script) follows revise.
const clipsModule = `{"version": 13,
  "collections": [{"name": "clips", "description": "A clip.", "key": "slug",
    "schema": {"type": "object", "properties": {"slug": {"type": "string"}}, "required": ["slug"]}}],
  "commands": [
    {"name": "new", "title": "New", "description": "Makes a clip.", "class": "internal",
     "input": {"type": "object", "properties": {"note": {"type": "string"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Make a clip."}},
    {"name": "check", "title": "Check", "description": "Checks a clip.", "class": "internal",
     "input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}, "record": {"type": "integer", "format": "record"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Check the clip."}},
    {"name": "tally", "title": "Tally", "description": "Counts checks.", "class": "internal",
     "input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Count."}},
    {"name": "revise", "title": "Revise", "description": "Edits a clip.", "class": "internal",
     "input": {"type": "object", "properties": {"record": {"type": "integer", "format": "record"}, "note": {"type": "string"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Edit the clip."}},
    {"name": "render", "title": "Render", "description": "Renders a clip.", "class": "internal",
     "input": {"type": "object", "properties": {"record": {"type": "integer", "format": "record"}, "process": {"type": "integer", "format": "process"},
       "n": {"type": "integer"}, "wait": {"type": "number"}}},
     "result": {"type": "object"}, "executor": {"type": "script", "script": "render"}}],
  "scripts": [{"name": "render", "file": "scripts/render.py", "runtime": "python"}],
  "triggers": [{"after": "new", "command": "check"}, {"after": "check", "command": "tally"}, {"after": "revise", "command": "render"}]}`

const clipsRender = `import json, sys, time
inp = json.load(sys.stdin)
time.sleep(inp.get("wait", 0))
print(json.dumps({"n": inp.get("n", 0), "record": inp.get("record")}))
`

// clipsEnv is deck with the clips module and the fake agent script.
func clipsEnv(t *testing.T, script harness.Script, opts ...harness.Option) *harness.Env {
	t.Helper()
	e := harness.New(t, append([]harness.Option{harness.WithAgent(script)}, opts...)...)
	e.Project("clips", clipsModule)
	e.SaveScript("clips", "scripts/render.py", clipsRender)
	return e
}

// clip writes a clip and gives its record.
func clip(e *harness.Env, slug string) int64 {
	e.T.Helper()
	e.Must("deck.records_upsert", map[string]any{"project": "clips", "collection": "clips", "records": harness.Jsonl(map[string]any{"slug": slug})}, nil)
	var all []struct {
		ID  int64
		Key string
	}
	e.Must("deck.records_list", map[string]any{"project": "clips", "collection": "clips"}, &all)
	for _, r := range all {
		if r.Key == slug {
			return r.ID
		}
	}
	e.T.Fatalf("no clip %s", slug)
	return 0
}

// runsOfRecord are the processes of a record, oldest first.
func runsOfRecord(e *harness.Env, record int64) []harness.Proc {
	e.T.Helper()
	var out []harness.Proc
	e.Must("deck.processes_list", map[string]any{"project": "clips", "record": record}, &out)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func runsOfCommand(e *harness.Env, command string) []harness.Proc {
	e.T.Helper()
	var out []harness.Proc
	e.Must("deck.processes_list", map[string]any{"project": "clips", "command": command}, &out)
	return out
}

// TestAStepAfterACommandGoesOnWithItsRecord: an after step that takes a record gets the record of the finished
// process, from its input or the one record it made; a step without the field gets the process only.
func TestAStepAfterACommandGoesOnWithItsRecord(t *testing.T) {
	t.Parallel()
	e := clipsEnv(t, func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "new" {
			if out, bad := c.Tool(ctx, "records_upsert", map[string]any{"project": "clips", "collection": "clips", "records": `{"slug": "made"}`}); bad {
				return provider.Result{Outcome: provider.Failed, Text: out}
			}
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	e.WaitState(e.Start("clips", "new", nil), "done")
	made := clip(e, "made")
	var check, tally int64
	e.Until("check and tally", func() bool {
		cs, ts := runsOfCommand(e, "check"), runsOfCommand(e, "tally")
		if len(cs) == 1 && len(ts) == 1 {
			check, tally = cs[0].ID, ts[0].ID
		}
		return tally != 0
	})
	if in := harness.ProcessInput(t, e, check); in["record"] != float64(made) {
		t.Fatalf("the step after new goes on with the record new made: %v", in)
	}
	if in := harness.ProcessInput(t, e, tally); in["record"] != nil || in["process"] != float64(check) {
		t.Fatalf("a step without a record field gets the process only: %v", in)
	}

	other := clip(e, "other")
	e.WaitState(e.Start("clips", "revise", map[string]any{"record": other, "note": "shorter"}), "done")
	var renders []harness.Proc
	e.Until("render after revise", func() bool { renders = runsOfCommand(e, "render"); return len(renders) == 1 })
	if got := e.WaitState(renders[0].ID, "done", "error"); got.State != "done" || harness.ProcessInput(t, e, got.ID)["record"] != float64(other) {
		t.Fatalf("the script after revise renders the same clip: %+v", got)
	}
	runs := runsOfRecord(e, other)
	if len(runs) != 2 || runs[0].Command != "revise" || runs[1].Command != "render" {
		t.Fatalf("the record lists its pipeline: %+v", runs)
	}
}

// TestAnAgentOfARecordReadsWhatItsThreadDid: an agent started for a record is told the earlier processes of that
// record in its project, with how they ended, and not those of another record.
func TestAnAgentOfARecordReadsWhatItsThreadDid(t *testing.T) {
	t.Parallel()
	e := clipsEnv(t, func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"done": "moved the crown"}`)}
	})
	a, b := clip(e, "a"), clip(e, "b")
	first := e.Start("clips", "revise", map[string]any{"record": a, "note": "the crown"})
	e.WaitState(first, "done")
	e.App.Runner.Wait()
	elsewhere := e.Start("clips", "revise", map[string]any{"record": b, "note": "the sky"})
	e.WaitState(elsewhere, "done")
	e.App.Runner.Wait()
	second := e.Start("clips", "revise", map[string]any{"record": a, "note": "the background"})
	e.WaitState(second, "done")
	var prompt string
	for _, c := range e.Fake.Calls() {
		if strings.Contains(c.Prompt, "the background") {
			prompt = c.Prompt
		}
	}
	if !strings.Contains(prompt, fmt.Sprintf("Earlier processes of record %d", a)) || !strings.Contains(prompt, fmt.Sprintf("#%d revise on fake, done", first)) ||
		!strings.Contains(prompt, "moved the crown") || strings.Contains(prompt, fmt.Sprintf("#%d ", elsewhere)) {
		t.Fatalf("the agent reads the thread of its record only:\n%s", prompt)
	}
	if c := e.Fake.Calls()[0]; strings.Contains(c.Prompt, "Earlier processes") {
		t.Fatalf("the first process of a record has no thread to read:\n%s", c.Prompt)
	}
}

// TestACallForARecordJoinsItsRunningAgent: a second edit of a clip while its revise runs goes into that run as a
// message instead of a second agent; an edit of another clip starts its own.
func TestACallForARecordJoinsItsRunningAgent(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var mu sync.Mutex
	var heardText string
	fake := &harness.Fake{Live: true, Script: func(ctx context.Context, c *harness.Call) provider.Result {
		if strings.Contains(c.Req.Prompt, "the crown") {
			started <- struct{}{}
			text := heard(ctx, c)
			mu.Lock()
			heardText = text
			mu.Unlock()
			<-release
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}}
	e := harness.New(t, harness.WithProvider(fake))
	e.Project("clips", clipsModule)
	e.SaveScript("clips", "scripts/render.py", clipsRender)
	a, b := clip(e, "a"), clip(e, "b")
	first := e.Start("clips", "revise", map[string]any{"record": a, "note": "the crown"})
	<-started
	if again := e.Start("clips", "revise", map[string]any{"record": a, "note": "the background at 5.2 s"}); again != first {
		t.Fatalf("a second edit of the clip joins the running revise: %d, %d", again, first)
	}
	e.Until("the running revise hears the second edit", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(heardText, "the background at 5.2 s") && strings.Contains(heardText, "called again for record")
	})
	other := e.Start("clips", "revise", map[string]any{"record": b, "note": "the sky"})
	if other == first {
		t.Fatal("an edit of another clip starts its own revise")
	}
	close(release)
	e.WaitState(first, "done")
	e.WaitState(other, "done")
	if runs := runsOfCommand(e, "revise"); len(runs) != 2 {
		t.Fatalf("two clips, two revises: %+v", runs)
	}
	var log struct {
		Lines []map[string]any
	}
	e.Must("deck.process_chat", map[string]any{"process": first}, &log)
	found := false
	for _, l := range log.Lines {
		if l["kind"] == "deck" && strings.Contains(fmt.Sprint(l["text"]), "the background at 5.2 s") {
			found = true
		}
	}
	if !found {
		t.Fatalf("the log of the run shows the call it took, as deck's: %+v", log.Lines)
	}
}

// TestAStepAlreadyGoingForARecordIsNotStartedTwice: a step after a second revise joins the check of the same clip
// that still runs after the first.
func TestAStepAlreadyGoingForARecordIsNotStartedTwice(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var mu sync.Mutex
	var told []string
	e := clipsEnv(t, func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "check" {
			started <- struct{}{}
			<-release
		}
		if strings.Contains(c.Req.Prompt, "called again for record") {
			mu.Lock()
			told = append(told, c.Req.Prompt)
			mu.Unlock()
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	e.Must("deck.manifest_save", map[string]any{"project": "clips", "manifest": strings.Replace(clipsModule,
		`{"after": "new", "command": "check"}`, `{"after": "revise", "command": "check"}`, 1)}, nil)
	a := clip(e, "a")
	first := e.Start("clips", "revise", map[string]any{"record": a, "note": "the crown"})
	e.WaitState(first, "done")
	<-started
	second := e.Start("clips", "revise", map[string]any{"record": a, "note": "the background"})
	if second == first {
		t.Fatal("a revise after the first one finished is a run of its own")
	}
	e.WaitState(second, "done")
	e.Until("the second call of check", func() bool {
		var log struct{ Lines []map[string]any }
		checks := runsOfCommand(e, "check")
		if len(checks) != 1 {
			return false
		}
		e.Must("deck.process_chat", map[string]any{"process": checks[0].ID}, &log)
		for _, l := range log.Lines {
			if l["kind"] == "deck" && strings.Contains(fmt.Sprint(l["text"]), fmt.Sprintf(`"process":%d`, second)) {
				return true
			}
		}
		return false
	})
	close(release)
	checks := runsOfCommand(e, "check")
	e.WaitState(checks[0].ID, "done")
	e.App.Runner.Wait()
	if checks = runsOfCommand(e, "check"); len(checks) != 1 {
		t.Fatalf("one check of the clip for both revises: %+v", checks)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(told) != 1 {
		t.Fatalf("the check hears the second revise after its turn: %d", len(told))
	}
}

// TestAScriptCalledForItsRecordWhileItRunsRunsOnceMoreAfter: calls of a script for a clip while it renders it return
// that run; once it ends, one more run takes the last of them.
func TestAScriptCalledForItsRecordWhileItRunsRunsOnceMoreAfter(t *testing.T) {
	t.Parallel()
	e := clipsEnv(t, nil)
	a := clip(e, "a")
	first := e.Start("clips", "render", map[string]any{"record": a, "n": 1, "wait": 3})
	if got := e.Proc(first); got.State != "running" {
		t.Fatalf("the first render runs: %+v", got)
	}
	for _, n := range []int{2, 3} {
		if id := e.Start("clips", "render", map[string]any{"record": a, "n": n}); id != first {
			t.Fatalf("a render of the clip while one runs returns it: %d, %d", id, first)
		}
	}
	e.WaitState(first, "done")
	next := following(e, first)
	if got := e.WaitState(next, "done", "error"); got.State != "done" || !strings.Contains(string(got.Result), `"n":3`) {
		t.Fatalf("one more render with the last call: %+v", got)
	}
	e.App.Runner.Wait()
	if runs := runsOfCommand(e, "render"); len(runs) != 2 {
		t.Fatalf("two renders for three calls: %+v", runs)
	}

	stopped := e.Start("clips", "render", map[string]any{"record": a, "n": 4, "wait": 30})
	if id := e.Start("clips", "render", map[string]any{"record": a, "n": 5}); id != stopped {
		t.Fatal("the call joins the running render")
	}
	e.Must("deck.process_stop", map[string]any{"process": stopped}, nil)
	e.WaitState(stopped, "stopped")
	e.App.Runner.Wait()
	if runs := runsOfCommand(e, "render"); len(runs) != 3 {
		t.Fatalf("a stopped render runs nothing more: %+v", runs)
	}
}

// TestDeckStopsAreDecks: a process deck stops because its record is gone is deck's stop, not the owner's.
func TestDeckStopsAreDecks(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 2)
	e := clipsEnv(t, func(ctx context.Context, c *harness.Call) provider.Result {
		started <- struct{}{}
		<-ctx.Done()
		return provider.Result{}
	})
	a, b := clip(e, "a"), clip(e, "b")
	gone := e.Start("clips", "revise", map[string]any{"record": a, "note": "x"})
	<-started
	e.Must("deck.record_delete", map[string]any{"record": a}, nil)
	e.WaitState(gone, "stopped")
	if got := e.Fault(gone); got.ErrorType != "deck.stopped" || got.Whose != "deck" {
		t.Fatalf("a process of a deleted record is stopped by deck: %+v", got)
	}
	owners := e.Start("clips", "revise", map[string]any{"record": b, "note": "y"})
	<-started
	e.Must("deck.process_stop", map[string]any{"process": owners}, nil)
	e.WaitState(owners, "stopped")
	if got := e.Fault(owners); got.ErrorType != "stopped" || got.Whose != "owner" {
		t.Fatalf("the owner's stop stays the owner's: %+v", got)
	}
}

// TestTheNextModelHearsWhatTheOwnerWroteAndKeepsTheRetryKey: a message of the owner to a refused process goes on with
// its work, and a call with its retry key finds the process that took it.
func TestTheNextModelHearsWhatTheOwnerWroteAndKeepsTheRetryKey(t *testing.T) {
	t.Parallel()
	told := make(chan struct{})
	release := make(chan struct{})
	e, backup, _ := twoModels(t, func(ctx context.Context, c *harness.Call) provider.Result {
		<-told
		return limited(harness.T0)
	}, func(ctx context.Context, c *harness.Call) provider.Result {
		<-release
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	var r struct{ Process int64 }
	e.Must("plain.work", map[string]any{"n": 1, "retry_key": "k1"}, &r)
	id := r.Process
	e.WaitState(id, "running")
	e.Must("deck.process_tell", map[string]any{"process": id, "text": "use the blue folder"}, nil)
	close(told)
	next := following(e, id)
	e.Must("plain.work", map[string]any{"n": 1, "retry_key": "k1"}, &r)
	if r.Process != next {
		t.Fatalf("a call with the retry key finds the process that took the work: %d, %d", r.Process, next)
	}
	e.Until("the next model starts", func() bool { return len(backup.Calls()) == 1 })
	if p := backup.Calls()[0].Prompt; !strings.Contains(p, "use the blue folder") {
		t.Fatalf("the next model hears what the owner wrote:\n%s", p)
	}
	close(release)
	e.WaitState(next, "done")
	var log struct{ Lines []map[string]any }
	e.Must("deck.process_chat", map[string]any{"process": id}, &log)
	for _, l := range log.Lines {
		if l["kind"] == "owner" && l["status"] != "taken" {
			t.Fatalf("the message the next model took is not left waiting on the refused one: %+v", l)
		}
	}
}

// TestTheMasterSeesTheProcessesOfItsTopic: what the replies of a topic started is listed by topic, and a session on
// another model is told of it.
func TestTheMasterSeesTheProcessesOfItsTopic(t *testing.T) {
	t.Parallel()
	script := func(ctx context.Context, c *harness.Call) provider.Result {
		if strings.Contains(c.Req.Prompt, "make a clip") && !strings.Contains(c.Req.Prompt, "did not see the earlier part") {
			if out, bad := c.Tool(ctx, "clips__revise", map[string]any{"record": 1, "note": "from the chat"}); bad {
				return provider.Result{Outcome: provider.Failed, Text: out}
			}
		}
		if c.Command() == "revise" {
			return provider.Result{Structured: json.RawMessage(`{"done": "CLIP-MARK"}`)}
		}
		return provider.Result{Text: "ok"}
	}
	main := &harness.Fake{Script: script}
	other := &harness.Fake{Script: script, ProviderName: "other"}
	e := harness.New(t, harness.WithProvider(main, other))
	e.Project("clips", clipsModule)
	e.SaveScript("clips", "scripts/render.py", clipsRender)
	clip(e, "a")
	topic := e.Say(0, "make a clip")
	e.MasterSaid(topic, 1)
	var runs []harness.Proc
	e.Until("the revise the reply started", func() bool {
		e.Must("deck.processes_list", map[string]any{"topic": topic}, &runs)
		for _, r := range runs {
			if r.Command == "revise" && r.State == "done" {
				return true
			}
		}
		return false
	})
	e.App.Runner.Wait()
	elsewhere := e.Say(0, "something else")
	e.MasterSaid(elsewhere, 1)
	var mine []harness.Proc
	e.Must("deck.processes_list", map[string]any{"topic": elsewhere}, &mine)
	for _, r := range mine {
		if r.Command == "revise" {
			t.Fatalf("another topic has its own processes: %+v", mine)
		}
	}

	e.UseModels("master.reply", "other")
	e.Say(topic, "how did it go")
	e.MasterSaid(topic, 2)
	var prompt string
	e.Until("the reply on the other model", func() bool {
		for _, c := range other.Calls() {
			if strings.Contains(c.Prompt, "how did it go") {
				prompt = c.Prompt
			}
		}
		return prompt != ""
	})
	if !strings.Contains(prompt, "Processes of the topic") || !strings.Contains(prompt, "clips.revise on fake, done") || !strings.Contains(prompt, "CLIP-MARK") {
		t.Fatalf("a session on another model is told what the topic started:\n%s", prompt)
	}
}

// TestAScheduleOfARecordJoinsItsRunOncePerSlot: a schedule with a record in its params that comes due while a run of
// that record goes on joins it once and moves on to its next slot.
func TestAScheduleOfARecordJoinsItsRunOncePerSlot(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "revise" {
			<-release
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}), harness.WithClock())

	e.Project("clips", strings.Replace(clipsModule, `"triggers": [`, `"triggers": [{"command": "revise", "cron": "0 9 * * *", "params": {"record": 1, "note": "nightly"}}, `, 1))
	e.SaveScript("clips", "scripts/render.py", clipsRender)
	if a := clip(e, "a"); a != 1 {
		t.Fatalf("the clip is record 1: %d", a)
	}
	first := e.Start("clips", "revise", map[string]any{"record": 1, "note": "the crown"})
	e.WaitState(first, "running")
	next := ""
	for _, tr := range e.Triggers() {
		if tr.Command == "revise" {
			next = tr.NextRunAt
		}
	}
	e.Clock.Add(24 * time.Hour)
	for range 3 {
		e.Tick()
		// The later ticks join no more.
		time.Sleep(300 * time.Millisecond)
	}
	var log struct{ Lines []map[string]any }
	e.Must("deck.process_chat", map[string]any{"process": first}, &log)
	joined := 0
	for _, l := range log.Lines {
		if l["kind"] == "deck" && strings.Contains(fmt.Sprint(l["text"]), "nightly") {
			joined++
		}
	}
	moved := ""
	for _, tr := range e.Triggers() {
		if tr.Command == "revise" {
			moved = tr.NextRunAt
		}
	}
	if joined != 1 || moved == next || e.CountOf("revise") != 1 {
		t.Fatalf("the schedule joins the run once and moves on: %d joins, next %s → %s, %d runs", joined, next, moved, e.CountOf("revise"))
	}
	close(release)
	e.WaitState(first, "done")
}

// TestARunAfterThatDidNotStartLetsItsRecordGo: a run after a render that cannot start tells the owner and leaves the
// clip free, so the next call renders it.
func TestARunAfterThatDidNotStartLetsItsRecordGo(t *testing.T) {
	t.Parallel()
	e := clipsEnv(t, nil)
	a := clip(e, "a")
	first := e.Start("clips", "render", map[string]any{"record": a, "n": 1, "wait": 3})
	if id := e.Start("clips", "render", map[string]any{"record": a, "n": 2}); id != first {
		t.Fatal("the call joins the running render")
	}
	start := strings.Index(clipsModule, `,
    {"name": "render"`)
	end := strings.Index(clipsModule, `"script": "render"}}]`) + len(`"script": "render"}}`)
	gone := strings.Replace(clipsModule[:start]+clipsModule[end:], `, {"after": "revise", "command": "render"}`, ``, 1)
	e.Must("deck.manifest_save", map[string]any{"project": "clips", "manifest": gone}, nil)
	e.WaitState(first, "done")
	if note := e.WaitNote(fmt.Sprintf("did not run render again for the calls that came while process #%d ran", first)); !strings.Contains(note.Text, `no command "render"`) {
		t.Fatalf("the note says why: %s", note.Text)
	}
	e.App.Runner.Wait()
	e.Must("deck.manifest_save", map[string]any{"project": "clips", "manifest": clipsModule}, nil)
	next := e.Start("clips", "render", map[string]any{"record": a, "n": 3})
	if next == first {
		t.Fatal("a run after that did not start does not hold the clip")
	}
	if got := e.WaitState(next, "done", "error"); got.State != "done" || !strings.Contains(string(got.Result), `"n":3`) {
		t.Fatalf("the next call renders the clip: %+v", got)
	}
}

// TestACallAFailedAgentDidNotHearRunsOnceAfter: an edit joined to a revise that fails before hearing it runs once
// more with its input; a revise that heard its edits runs nothing more.
func TestACallAFailedAgentDidNotHearRunsOnceAfter(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 4)
	release := make(chan struct{})
	var mu sync.Mutex
	var prompts []string
	e := clipsEnv(t, func(ctx context.Context, c *harness.Call) provider.Result {
		mu.Lock()
		prompts = append(prompts, c.Req.Prompt)
		mu.Unlock()
		if strings.Contains(c.Req.Prompt, "the crown") {
			started <- struct{}{}
			<-release
			return provider.Result{Outcome: provider.Failed, Text: "the job broke"}
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	e.Must("deck.manifest_save", map[string]any{"project": "clips", "manifest": strings.Replace(clipsModule,
		`, {"after": "revise", "command": "render"}`, ``, 1)}, nil)
	a := clip(e, "a")
	first := e.Start("clips", "revise", map[string]any{"record": a, "note": "the crown"})
	<-started
	for _, note := range []string{"the sky", "the background"} {
		if id := e.Start("clips", "revise", map[string]any{"record": a, "note": note}); id != first {
			t.Fatalf("an edit joins the running revise: %d, %d", id, first)
		}
	}
	close(release)
	e.WaitState(first, "error")
	next := following(e, first)
	e.WaitState(next, "done")
	e.App.Runner.Wait()
	mu.Lock()
	after := strings.Join(prompts[1:], "\n")
	mu.Unlock()
	if m := harness.InputOf.FindStringSubmatch(after); m == nil || !strings.Contains(m[1], "the background") {
		t.Fatalf("the run after takes the last edit as its input: %s", after)
	}
	if !strings.Contains(after, "the sky") {
		t.Fatal("the run after hears the other edit the failed revise missed")
	}
	if runs := runsOfCommand(e, "revise"); len(runs) != 2 {
		t.Fatalf("one run after for the missed edits: %+v", runs)
	}
}

// TestAStepGetsOnlyAnIntegerRecordAndAFailedStepIsSeen: a step whose record field is not an integer gets no record,
// and a step that does not start is a note of deck in the feed at the time of deck's clock.
func TestAStepGetsOnlyAnIntegerRecordAndAFailedStepIsSeen(t *testing.T) {
	t.Parallel()
	clock := &harness.Clock{At: harness.T0}
	e := clipsEnv(t, func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}, harness.WithClock(clock))
	m := strings.Replace(clipsModule, `"input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Count."}}`,
		`"input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}, "record": {"type": "string"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Count."}},
    {"name": "label", "title": "Label", "description": "Labels a clip.", "class": "internal",
     "input": {"type": "object", "properties": {"record": {"type": "integer"}, "process": {"type": "integer", "format": "process"}}},
     "result": {"type": "object"}, "executor": {"type": "script", "script": "label"}}`, 1)
	m = strings.Replace(m, `{"after": "check", "command": "tally"}`, `{"after": "check", "command": "tally"}, {"after": "check", "command": "label"}`, 1)
	m = strings.Replace(m, `"scripts": [`, `"scripts": [{"name": "label", "file": "scripts/label.py", "runtime": "python"}, `, 1)
	e.Must("deck.manifest_save", map[string]any{"project": "clips", "manifest": m}, nil)
	a := clip(e, "a")
	check := e.Start("clips", "check", map[string]any{"record": a})
	e.WaitState(check, "done")
	var tally []harness.Proc
	e.Until("tally", func() bool { tally = runsOfCommand(e, "tally"); return len(tally) == 1 })
	if in := harness.ProcessInput(t, e, tally[0].ID); in["record"] != nil {
		t.Fatalf("a record field that is not an integer is not filled: %v", in)
	}
	if n := e.WaitNote(fmt.Sprintf("deck did not start label after process #%d of check", check)); n.CreatedAt != store.Time(harness.T0) {
		t.Fatalf("the note is at the time of deck's clock: %s", n.CreatedAt)
	}
}

// TestAHelperCallingBackForTheRecordDoesNotJoinItsCaller: a revise that started a helper waits for it, so the helper's
// call of revise for the same clip is a run of its own, not a message to the revise that waits.
func TestAHelperCallingBackForTheRecordDoesNotJoinItsCaller(t *testing.T) {
	t.Parallel()
	helped := make(chan string, 1)
	var a int64
	e := clipsEnv(t, func(ctx context.Context, c *harness.Call) provider.Result {
		switch {
		case c.Command() == "revise" && strings.Contains(c.Req.Prompt, "the crown"):
			if out, bad := c.Tool(ctx, "clips__tally", map[string]any{}); bad {
				return provider.Result{Outcome: provider.Failed, Text: out}
			}
			<-helped
		case c.Command() == "tally":
			out, _ := c.Tool(ctx, "clips__revise", map[string]any{"record": a, "note": "by the helper"})
			helped <- out
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	a = clip(e, "a")
	first := e.Start("clips", "revise", map[string]any{"record": a, "note": "the crown"})
	e.WaitState(first, "done")
	e.Until("the helper's revise", func() bool { return len(runsOfCommand(e, "revise")) == 2 })
	for _, r := range runsOfCommand(e, "revise") {
		e.WaitState(r.ID, "done")
	}
}
