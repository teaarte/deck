package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
	"deck2/internal/store"
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
func clipsEnv(t *testing.T, script Script, cfg ...func(*app.Config)) *Env {
	t.Helper()
	e := newEnv(t, script, cfg...)
	e.createProject("clips", clipsModule)
	e.saveScript("clips", "scripts/render.py", clipsRender)
	return e
}

// clip writes a clip and gives its record.
func (e *Env) clip(slug string) int64 {
	e.T.Helper()
	e.must("deck.records_upsert", map[string]any{"project": "clips", "collection": "clips", "records": jsonl(map[string]any{"slug": slug})}, nil)
	var all []struct {
		ID  int64
		Key string
	}
	e.must("deck.records_list", map[string]any{"project": "clips", "collection": "clips"}, &all)
	for _, r := range all {
		if r.Key == slug {
			return r.ID
		}
	}
	e.T.Fatalf("no clip %s", slug)
	return 0
}

// runsOfRecord are the processes of a record, oldest first.
func (e *Env) runsOfRecord(record int64) []procFull {
	e.T.Helper()
	var out []procFull
	e.must("deck.processes_list", map[string]any{"project": "clips", "record": record}, &out)
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

func (e *Env) runsOfCommand(command string) []procFull {
	e.T.Helper()
	var out []procFull
	e.must("deck.processes_list", map[string]any{"project": "clips", "command": command}, &out)
	return out
}

// TestAStepAfterACommandGoesOnWithItsRecord: an after step that takes a record gets the record of the finished
// process, from its input or the one record it made; a step without the field gets the process only.
func TestAStepAfterACommandGoesOnWithItsRecord(t *testing.T) {
	t.Parallel()
	e := clipsEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "new" {
			if out, bad := c.Tool(ctx, "records_upsert", map[string]any{"project": "clips", "collection": "clips", "records": `{"slug": "made"}`}); bad {
				return provider.Result{IsError: true, Text: out}
			}
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	e.waitState(e.start("clips", "new", nil), "done")
	made := e.clip("made")
	var check, tally int64
	e.waitFor("check and tally", func() bool {
		cs, ts := e.runsOfCommand("check"), e.runsOfCommand("tally")
		if len(cs) == 1 && len(ts) == 1 {
			check, tally = cs[0].ID, ts[0].ID
		}
		return tally != 0
	})
	if in := processInput(t, e, check); in["record"] != float64(made) {
		t.Fatalf("the step after new goes on with the record new made: %v", in)
	}
	if in := processInput(t, e, tally); in["record"] != nil || in["process"] != float64(check) {
		t.Fatalf("a step without a record field gets the process only: %v", in)
	}

	other := e.clip("other")
	e.waitState(e.start("clips", "revise", map[string]any{"record": other, "note": "shorter"}), "done")
	var renders []procFull
	e.waitFor("render after revise", func() bool { renders = e.runsOfCommand("render"); return len(renders) == 1 })
	if got := e.waitScript(renders[0].ID, "done", "error"); got.State != "done" || processInput(t, e, got.ID)["record"] != float64(other) {
		t.Fatalf("the script after revise renders the same clip: %+v", got)
	}
	runs := e.runsOfRecord(other)
	if len(runs) != 2 || runs[0].Command != "revise" || runs[1].Command != "render" {
		t.Fatalf("the record lists its pipeline: %+v", runs)
	}
}

// TestAnAgentOfARecordReadsWhatItsThreadDid: an agent started for a record is told the earlier processes of that
// record in its project, with how they ended, and not those of another record.
func TestAnAgentOfARecordReadsWhatItsThreadDid(t *testing.T) {
	t.Parallel()
	e := clipsEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{"done": "moved the crown"}`)}
	})
	a, b := e.clip("a"), e.clip("b")
	first := e.start("clips", "revise", map[string]any{"record": a, "note": "the crown"})
	e.waitState(first, "done")
	e.App.Runner.Wait()
	elsewhere := e.start("clips", "revise", map[string]any{"record": b, "note": "the sky"})
	e.waitState(elsewhere, "done")
	e.App.Runner.Wait()
	second := e.start("clips", "revise", map[string]any{"record": a, "note": "the background"})
	e.waitState(second, "done")
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
	fake := &Fake{live: true, script: func(ctx context.Context, c *Call) provider.Result {
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
	e := newEnvWith(t, t.TempDir(), fake)
	e.createProject("clips", clipsModule)
	e.saveScript("clips", "scripts/render.py", clipsRender)
	a, b := e.clip("a"), e.clip("b")
	first := e.start("clips", "revise", map[string]any{"record": a, "note": "the crown"})
	<-started
	if again := e.start("clips", "revise", map[string]any{"record": a, "note": "the background at 5.2 s"}); again != first {
		t.Fatalf("a second edit of the clip joins the running revise: %d, %d", again, first)
	}
	e.waitFor("the running revise hears the second edit", func() bool {
		mu.Lock()
		defer mu.Unlock()
		return strings.Contains(heardText, "the background at 5.2 s") && strings.Contains(heardText, "called again for record")
	})
	other := e.start("clips", "revise", map[string]any{"record": b, "note": "the sky"})
	if other == first {
		t.Fatal("an edit of another clip starts its own revise")
	}
	close(release)
	e.waitState(first, "done")
	e.waitState(other, "done")
	if runs := e.runsOfCommand("revise"); len(runs) != 2 {
		t.Fatalf("two clips, two revises: %+v", runs)
	}
	var log struct {
		Lines []map[string]any
	}
	e.must("deck.process_chat", map[string]any{"process": first}, &log)
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
	e := clipsEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "check" {
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
	e.must("deck.manifest_save", map[string]any{"project": "clips", "manifest": strings.Replace(clipsModule,
		`{"after": "new", "command": "check"}`, `{"after": "revise", "command": "check"}`, 1)}, nil)
	a := e.clip("a")
	first := e.start("clips", "revise", map[string]any{"record": a, "note": "the crown"})
	e.waitState(first, "done")
	<-started
	second := e.start("clips", "revise", map[string]any{"record": a, "note": "the background"})
	if second == first {
		t.Fatal("a revise after the first one finished is a run of its own")
	}
	e.waitState(second, "done")
	e.waitFor("the second call of check", func() bool {
		var log struct{ Lines []map[string]any }
		checks := e.runsOfCommand("check")
		if len(checks) != 1 {
			return false
		}
		e.must("deck.process_chat", map[string]any{"process": checks[0].ID}, &log)
		for _, l := range log.Lines {
			if l["kind"] == "deck" && strings.Contains(fmt.Sprint(l["text"]), fmt.Sprintf(`"process":%d`, second)) {
				return true
			}
		}
		return false
	})
	close(release)
	checks := e.runsOfCommand("check")
	e.waitState(checks[0].ID, "done")
	e.App.Runner.Wait()
	if checks = e.runsOfCommand("check"); len(checks) != 1 {
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
	a := e.clip("a")
	first := e.start("clips", "render", map[string]any{"record": a, "n": 1, "wait": 3})
	if got := e.process(first); got.State != "running" {
		t.Fatalf("the first render runs: %+v", got)
	}
	for _, n := range []int{2, 3} {
		if id := e.start("clips", "render", map[string]any{"record": a, "n": n}); id != first {
			t.Fatalf("a render of the clip while one runs returns it: %d, %d", id, first)
		}
	}
	e.waitScript(first, "done")
	next := e.following(first)
	if got := e.waitScript(next, "done", "error"); got.State != "done" || !strings.Contains(string(got.Result), `"n":3`) {
		t.Fatalf("one more render with the last call: %+v", got)
	}
	e.App.Runner.Wait()
	if runs := e.runsOfCommand("render"); len(runs) != 2 {
		t.Fatalf("two renders for three calls: %+v", runs)
	}

	stopped := e.start("clips", "render", map[string]any{"record": a, "n": 4, "wait": 30})
	if id := e.start("clips", "render", map[string]any{"record": a, "n": 5}); id != stopped {
		t.Fatal("the call joins the running render")
	}
	e.must("deck.process_stop", map[string]any{"process": stopped}, nil)
	e.waitScript(stopped, "stopped")
	e.App.Runner.Wait()
	if runs := e.runsOfCommand("render"); len(runs) != 3 {
		t.Fatalf("a stopped render runs nothing more: %+v", runs)
	}
}

// TestDeckStopsAreDecks: a process deck stops because its record is gone is deck's stop, not the owner's.
func TestDeckStopsAreDecks(t *testing.T) {
	t.Parallel()
	started := make(chan struct{}, 2)
	e := clipsEnv(t, func(ctx context.Context, c *Call) provider.Result {
		started <- struct{}{}
		<-ctx.Done()
		return provider.Result{}
	})
	a, b := e.clip("a"), e.clip("b")
	gone := e.start("clips", "revise", map[string]any{"record": a, "note": "x"})
	<-started
	e.must("deck.record_delete", map[string]any{"record": a}, nil)
	e.waitState(gone, "stopped")
	if got := e.fault(gone); got.ErrorType != "deck.stopped" || got.Whose != "deck" {
		t.Fatalf("a process of a deleted record is stopped by deck: %+v", got)
	}
	owners := e.start("clips", "revise", map[string]any{"record": b, "note": "y"})
	<-started
	e.must("deck.process_stop", map[string]any{"process": owners}, nil)
	e.waitState(owners, "stopped")
	if got := e.fault(owners); got.ErrorType != "stopped" || got.Whose != "owner" {
		t.Fatalf("the owner's stop stays the owner's: %+v", got)
	}
}

// TestTheNextModelHearsWhatTheOwnerWroteAndKeepsTheRetryKey: a message of the owner to a refused process goes on with
// its work, and a call with its retry key finds the process that took it.
func TestTheNextModelHearsWhatTheOwnerWroteAndKeepsTheRetryKey(t *testing.T) {
	t.Parallel()
	told := make(chan struct{})
	release := make(chan struct{})
	e, backup, _ := twoModels(t, func(ctx context.Context, c *Call) provider.Result {
		<-told
		return limited(t0)
	}, func(ctx context.Context, c *Call) provider.Result {
		<-release
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	var r struct{ Process int64 }
	e.must("plain.work", map[string]any{"n": 1, "retry_key": "k1"}, &r)
	id := r.Process
	e.waitState(id, "running")
	e.must("deck.process_tell", map[string]any{"process": id, "text": "use the blue folder"}, nil)
	close(told)
	next := e.following(id)
	e.must("plain.work", map[string]any{"n": 1, "retry_key": "k1"}, &r)
	if r.Process != next {
		t.Fatalf("a call with the retry key finds the process that took the work: %d, %d", r.Process, next)
	}
	e.waitFor("the next model starts", func() bool { return len(backup.Calls()) == 1 })
	if p := backup.Calls()[0].Prompt; !strings.Contains(p, "use the blue folder") {
		t.Fatalf("the next model hears what the owner wrote:\n%s", p)
	}
	close(release)
	e.waitState(next, "done")
	var log struct{ Lines []map[string]any }
	e.must("deck.process_chat", map[string]any{"process": id}, &log)
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
	script := func(ctx context.Context, c *Call) provider.Result {
		if strings.Contains(c.Req.Prompt, "make a clip") && !strings.Contains(c.Req.Prompt, "did not see the earlier part") {
			if out, bad := c.Tool(ctx, "clips__revise", map[string]any{"record": 1, "note": "from the chat"}); bad {
				return provider.Result{IsError: true, Text: out}
			}
		}
		if c.command() == "revise" {
			return provider.Result{Structured: json.RawMessage(`{"done": "CLIP-MARK"}`)}
		}
		return provider.Result{Text: "ok"}
	}
	main := &Fake{script: script}
	other := &Fake{script: script, name: "other"}
	e := newEnvBuiltin(t, t.TempDir(), []provider.Provider{main, other})
	e.Fake = main
	e.createProject("clips", clipsModule)
	e.saveScript("clips", "scripts/render.py", clipsRender)
	e.clip("a")
	topic := e.say(0, "make a clip")
	e.masterSaid(topic, 1)
	var runs []procFull
	e.waitFor("the revise the reply started", func() bool {
		e.must("deck.processes_list", map[string]any{"topic": topic}, &runs)
		for _, r := range runs {
			if r.Command == "revise" && r.State == "done" {
				return true
			}
		}
		return false
	})
	e.App.Runner.Wait()
	elsewhere := e.say(0, "something else")
	e.masterSaid(elsewhere, 1)
	var mine []procFull
	e.must("deck.processes_list", map[string]any{"topic": elsewhere}, &mine)
	for _, r := range mine {
		if r.Command == "revise" {
			t.Fatalf("another topic has its own processes: %+v", mine)
		}
	}

	e.useModels("master.reply", "other")
	e.say(topic, "how did it go")
	e.masterSaid(topic, 2)
	var prompt string
	e.waitWithin("the reply on the other model", time.Minute, func() bool {
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
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "revise" {
			<-release
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	e.createProject("clips", strings.Replace(clipsModule, `"triggers": [`, `"triggers": [{"command": "revise", "cron": "0 9 * * *", "params": {"record": 1, "note": "nightly"}}, `, 1))
	e.saveScript("clips", "scripts/render.py", clipsRender)
	if a := e.clip("a"); a != 1 {
		t.Fatalf("the clip is record 1: %d", a)
	}
	first := e.start("clips", "revise", map[string]any{"record": 1, "note": "the crown"})
	e.waitState(first, "running")
	next := ""
	for _, tr := range e.triggers() {
		if tr.Command == "revise" {
			next = tr.NextRunAt
		}
	}
	e.Clock.Add(24 * time.Hour)
	for range 3 {
		e.tick()
		time.Sleep(300 * time.Millisecond)
	}
	var log struct{ Lines []map[string]any }
	e.must("deck.process_chat", map[string]any{"process": first}, &log)
	joined := 0
	for _, l := range log.Lines {
		if l["kind"] == "deck" && strings.Contains(fmt.Sprint(l["text"]), "nightly") {
			joined++
		}
	}
	moved := ""
	for _, tr := range e.triggers() {
		if tr.Command == "revise" {
			moved = tr.NextRunAt
		}
	}
	if joined != 1 || moved == next || e.countOf("revise") != 1 {
		t.Fatalf("the schedule joins the run once and moves on: %d joins, next %s → %s, %d runs", joined, next, moved, e.countOf("revise"))
	}
	close(release)
	e.waitState(first, "done")
}

// TestARunAfterThatDidNotStartLetsItsRecordGo: a run after a render that cannot start tells the owner and leaves the
// clip free, so the next call renders it.
func TestARunAfterThatDidNotStartLetsItsRecordGo(t *testing.T) {
	t.Parallel()
	e := clipsEnv(t, nil)
	a := e.clip("a")
	first := e.start("clips", "render", map[string]any{"record": a, "n": 1, "wait": 3})
	if id := e.start("clips", "render", map[string]any{"record": a, "n": 2}); id != first {
		t.Fatal("the call joins the running render")
	}
	start := strings.Index(clipsModule, `,
    {"name": "render"`)
	end := strings.Index(clipsModule, `"script": "render"}}]`) + len(`"script": "render"}}`)
	gone := strings.Replace(clipsModule[:start]+clipsModule[end:], `, {"after": "revise", "command": "render"}`, ``, 1)
	e.must("deck.manifest_save", map[string]any{"project": "clips", "manifest": gone}, nil)
	e.waitScript(first, "done")
	if note := e.waitNote(fmt.Sprintf("did not run render again for the calls that came while process #%d ran", first)); !strings.Contains(note.Text, `no command "render"`) {
		t.Fatalf("the note says why: %s", note.Text)
	}
	e.App.Runner.Wait()
	e.must("deck.manifest_save", map[string]any{"project": "clips", "manifest": clipsModule}, nil)
	next := e.start("clips", "render", map[string]any{"record": a, "n": 3})
	if next == first {
		t.Fatal("a run after that did not start does not hold the clip")
	}
	if got := e.waitScript(next, "done", "error"); got.State != "done" || !strings.Contains(string(got.Result), `"n":3`) {
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
	e := clipsEnv(t, func(ctx context.Context, c *Call) provider.Result {
		mu.Lock()
		prompts = append(prompts, c.Req.Prompt)
		mu.Unlock()
		if strings.Contains(c.Req.Prompt, "the crown") {
			started <- struct{}{}
			<-release
			return provider.Result{IsError: true, Text: "the job broke"}
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	e.must("deck.manifest_save", map[string]any{"project": "clips", "manifest": strings.Replace(clipsModule,
		`, {"after": "revise", "command": "render"}`, ``, 1)}, nil)
	a := e.clip("a")
	first := e.start("clips", "revise", map[string]any{"record": a, "note": "the crown"})
	<-started
	for _, note := range []string{"the sky", "the background"} {
		if id := e.start("clips", "revise", map[string]any{"record": a, "note": note}); id != first {
			t.Fatalf("an edit joins the running revise: %d, %d", id, first)
		}
	}
	close(release)
	e.waitState(first, "error")
	next := e.following(first)
	e.waitState(next, "done")
	e.App.Runner.Wait()
	mu.Lock()
	after := strings.Join(prompts[1:], "\n")
	mu.Unlock()
	if m := inputOf.FindStringSubmatch(after); m == nil || !strings.Contains(m[1], "the background") {
		t.Fatalf("the run after takes the last edit as its input: %s", after)
	}
	if !strings.Contains(after, "the sky") {
		t.Fatal("the run after hears the other edit the failed revise missed")
	}
	if runs := e.runsOfCommand("revise"); len(runs) != 2 {
		t.Fatalf("one run after for the missed edits: %+v", runs)
	}
}

// TestAStepGetsOnlyAnIntegerRecordAndAFailedStepIsSeen: a step whose record field is not an integer gets no record,
// and a step that does not start is a note of deck in the feed at the time of deck's clock.
func TestAStepGetsOnlyAnIntegerRecordAndAFailedStepIsSeen(t *testing.T) {
	t.Parallel()
	clock := &Clock{now: t0}
	e := clipsEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}, withClock(clock))
	m := strings.Replace(clipsModule, `"input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Count."}}`,
		`"input": {"type": "object", "properties": {"process": {"type": "integer", "format": "process"}, "record": {"type": "string"}}},
     "result": {"type": "object"}, "executor": {"type": "agent", "prompt": "Count."}},
    {"name": "label", "title": "Label", "description": "Labels a clip.", "class": "internal",
     "input": {"type": "object", "properties": {"record": {"type": "integer"}, "process": {"type": "integer", "format": "process"}}},
     "result": {"type": "object"}, "executor": {"type": "script", "script": "label"}}`, 1)
	m = strings.Replace(m, `{"after": "check", "command": "tally"}`, `{"after": "check", "command": "tally"}, {"after": "check", "command": "label"}`, 1)
	m = strings.Replace(m, `"scripts": [`, `"scripts": [{"name": "label", "file": "scripts/label.py", "runtime": "python"}, `, 1)
	e.must("deck.manifest_save", map[string]any{"project": "clips", "manifest": m}, nil)
	a := e.clip("a")
	check := e.start("clips", "check", map[string]any{"record": a})
	e.waitState(check, "done")
	var tally []procFull
	e.waitFor("tally", func() bool { tally = e.runsOfCommand("tally"); return len(tally) == 1 })
	if in := processInput(t, e, tally[0].ID); in["record"] != nil {
		t.Fatalf("a record field that is not an integer is not filled: %v", in)
	}
	if n := e.waitNote(fmt.Sprintf("deck did not start label after process #%d of check", check)); n.CreatedAt != store.Time(t0) {
		t.Fatalf("the note is at the time of deck's clock: %s", n.CreatedAt)
	}
}

// TestAHelperCallingBackForTheRecordDoesNotJoinItsCaller: a revise that started a helper waits for it, so the helper's
// call of revise for the same clip is a run of its own, not a message to the revise that waits.
func TestAHelperCallingBackForTheRecordDoesNotJoinItsCaller(t *testing.T) {
	t.Parallel()
	helped := make(chan string, 1)
	var a int64
	e := clipsEnv(t, func(ctx context.Context, c *Call) provider.Result {
		switch {
		case c.command() == "revise" && strings.Contains(c.Req.Prompt, "the crown"):
			if out, bad := c.Tool(ctx, "clips__tally", map[string]any{}); bad {
				return provider.Result{IsError: true, Text: out}
			}
			<-helped
		case c.command() == "tally":
			out, _ := c.Tool(ctx, "clips__revise", map[string]any{"record": a, "note": "by the helper"})
			helped <- out
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	a = e.clip("a")
	first := e.start("clips", "revise", map[string]any{"record": a, "note": "the crown"})
	e.waitState(first, "done")
	e.waitFor("the helper's revise", func() bool { return len(e.runsOfCommand("revise")) == 2 })
	for _, r := range e.runsOfCommand("revise") {
		e.waitState(r.ID, "done")
	}
}
