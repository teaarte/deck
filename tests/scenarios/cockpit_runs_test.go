package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/internal/store"
)

// jobsModule renders an item of its collection by an agent command whose input names the record.
const jobsModule = `{"version": 11,
  "collections": [{"name": "videos", "description": "A video.", "key": "slug", "display": "title",
    "schema": {"type": "object", "properties": {"slug": {"type": "string"}, "title": {"type": "string"}}, "required": ["slug"]}}],
  "commands": [{"name": "voice", "title": "Voice", "description": "Voices a video.", "class": "internal",
    "input": {"type": "object", "properties": {"record": {"type": "integer"}}},
    "executor": {"type": "agent", "prompt": "Voice the video."}}]}`

type cockpitRun struct {
	ID          int64
	Command     string
	Error       string
	Record      int64
	RecordTitle string `json:"record_title"`
	Since       string
	Long        bool
	Retry       bool
	Word        string
	Tone        string
	Waits       bool
}

// TestTheCockpitShowsWhatDidNotWorkUntilARunMakesItGood: a failed run, with the record its input names, stays in the
// cockpit until a later run of it goes well or the owner closes it, and a retry runs it again with its input.
func TestTheCockpitShowsWhatDidNotWorkUntilARunMakesItGood(t *testing.T) {
	t.Parallel()
	var fail atomic.Bool
	fail.Store(true)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if fail.Load() {
			return provider.Result{IsError: true, Text: "the voice server is down"}
		}
		return provider.Result{Text: "voiced"}
	})
	e.createProject("shorts", jobsModule)
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(map[string]any{"slug": "knights", "title": "Knights"})}, nil)
	var recs []struct{ ID int64 }
	e.must("deck.records_list", map[string]any{"project": "shorts", "collection": "videos"}, &recs)
	failed := func() []cockpitRun {
		var out []cockpitRun
		e.must("deck.processes_list", map[string]any{"failed": true}, &out)
		return out
	}
	id := e.start("shorts", "voice", map[string]any{"record": recs[0].ID})
	e.waitState(id, "done", "error")
	if got := failed(); len(got) != 1 || got[0].ID != id || got[0].RecordTitle != "Knights" || got[0].Error == "" {
		t.Fatalf("the failed run is in the cockpit with its record and why: %+v", got)
	}
	fail.Store(false)
	var again struct{ Process int64 }
	e.must("deck.process_retry", map[string]any{"process": id}, &again)
	if got := e.waitState(again.Process, "done", "error"); got.State != "done" || len(failed()) != 0 {
		t.Fatalf("a retry runs it again with its input, and a run that went well takes it off: %+v %+v", got, failed())
	}
	fail.Store(true)
	id = e.start("shorts", "voice", map[string]any{"record": recs[0].ID})
	e.waitState(id, "done", "error")
	e.must("deck.process_dismiss", map[string]any{"process": id}, nil)
	if got := failed(); len(got) != 0 {
		t.Fatalf("a run the owner closed leaves the cockpit: %+v", got)
	}
}

// TestARunThatTakesLongerThanUsualIsMarked: an open run says since when it works and, past twice the usual time of
// its command over its last five runs at least, that it takes longer than usual; within twice, or with fewer runs
// to know the usual by, it is not marked.
func TestARunThatTakesLongerThanUsualIsMarked(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		runs int
		at   time.Duration
		long bool
	}{
		{"past twice the usual", usualRunsForTest, 3 * time.Minute, true},
		{"within twice the usual", usualRunsForTest, 90 * time.Second, false},
		{"too few runs to know the usual", usualRunsForTest - 1, 3 * time.Minute, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			release := make(chan struct{})
			t.Cleanup(func() { close(release) })
			var n atomic.Int32
			clock := &Clock{now: t0}
			e := newEnvWith(t, t.TempDir(), &Fake{script: func(ctx context.Context, c *Call) provider.Result {
				if int(n.Add(1)) > tc.runs {
					select {
					case <-release:
					case <-ctx.Done():
					}
				} else {
					clock.Add(time.Minute)
				}
				return provider.Result{Text: "voiced"}
			}}, withClock(clock))
			e.createProject("shorts", jobsModule)
			for range tc.runs {
				e.waitState(e.start("shorts", "voice", nil), "done", "error")
			}
			id := e.start("shorts", "voice", nil)
			e.waitState(id, "running")
			clock.Add(tc.at)
			var open []cockpitRun
			e.must("deck.processes_list", map[string]any{"open": true}, &open)
			if len(open) != 1 || open[0].ID != id || open[0].Since == "" || open[0].Long != tc.long ||
				tc.long && (open[0].Tone != "warn" || !strings.HasPrefix(open[0].Word, "longer than usual: runs on")) ||
				!tc.long && (open[0].Tone != "" || !strings.HasPrefix(open[0].Word, "runs on")) {
				t.Fatalf("long %v: %+v", tc.long, open)
			}
		})
	}
}

const usualRunsForTest = 5

// TestARunSentBackCountsFromThenAndFailsAnew: a closed failed run sent back for rework is a fresh try: it works since
// then, carries no old result, and failing again brings it back to the cockpit though it first started long ago.
func TestARunSentBackCountsFromThenAndFailsAnew(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	clock := &Clock{now: t0}
	e := newEnvWith(t, t.TempDir(), &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		if c.N > 1 {
			<-release
		}
		return provider.Result{IsError: true, Text: "the voice server is down"}
	}}, withClock(clock))
	e.createProject("shorts", jobsModule)
	id := e.start("shorts", "voice", nil)
	e.waitState(id, "error")
	e.must("deck.process_dismiss", map[string]any{"process": id}, nil)
	clock.Add(8 * 24 * time.Hour)
	e.must("deck.process_rework", map[string]any{"process": id, "remark": "once more"}, nil)
	clock.Add(time.Minute)
	var open []struct {
		cockpitRun
		Result any
	}
	e.must("deck.processes_list", map[string]any{"open": true}, &open)
	if len(open) != 1 || open[0].Since != store.Time(clock.Now().Add(-time.Minute)) || open[0].Result != nil {
		t.Fatalf("the run works since it was sent back and has no result yet: %+v", open)
	}
	close(release)
	e.waitState(id, "error")
	var failed []cockpitRun
	e.must("deck.processes_list", map[string]any{"failed": true}, &failed)
	if len(failed) != 1 || failed[0].ID != id {
		t.Fatalf("the run failed again, so it is back in the cockpit: %+v", failed)
	}
}

// TestARunThatWaitsIsNotLongerThanUsual: a run that waits, here on a usage limit, is not slow work.
func TestARunThatWaitsIsNotLongerThanUsual(t *testing.T) {
	t.Parallel()
	clock := &Clock{now: t0}
	e := newEnvWith(t, t.TempDir(), &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		if c.N > usualRunsForTest {
			return provider.Result{RateLimited: true, ResetsAt: t0.Add(time.Hour).Unix()}
		}
		clock.Add(time.Second)
		return provider.Result{Text: "voiced"}
	}}, withClock(clock))
	e.createProject("shorts", jobsModule)
	for range usualRunsForTest {
		e.waitState(e.start("shorts", "voice", nil), "done", "error")
	}
	id := e.start("shorts", "voice", nil)
	e.waitState(id, "rate_limited")
	clock.Add(3 * time.Minute)
	var open []cockpitRun
	e.must("deck.processes_list", map[string]any{"open": true}, &open)
	if len(open) != 1 || open[0].ID != id || open[0].Long || !open[0].Waits || open[0].Tone != "warn" || open[0].Word != "paused on a usage limit" {
		t.Fatalf("a run that waits is not marked longer than usual, and says it waits: %+v", open)
	}
}

// cutModule cuts a clip by name; trim is a step the master calls, not the owner.
const cutModule = `{"version": 11,
  "commands": [
    {"name": "cut", "title": "Cut", "description": "Cuts a clip.", "class": "internal",
      "input": {"type": "object", "properties": {"clip": {"type": "string"}}},
      "executor": {"type": "agent", "prompt": "Cut the clip."}},
    {"name": "trim", "title": "Trim", "description": "Trims a clip.", "class": "internal", "callers": ["master"],
      "input": {"type": "object", "properties": {"clip": {"type": "string"}}},
      "executor": {"type": "agent", "prompt": "Trim the clip."}}]}`

// TestAFailureWithoutARecordLeavesOnlyWhenTheSameInputWorks: a failed run of a command without a record stays in
// the cockpit through a good run with another input and a good trial, and leaves once a real run with its input
// goes well; one the owner may not call has no retry.
func TestAFailureWithoutARecordLeavesOnlyWhenTheSameInputWorks(t *testing.T) {
	t.Parallel()
	var fail atomic.Bool
	fail.Store(true)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if fail.Load() {
			return provider.Result{IsError: true, Text: "the cutter is down"}
		}
		return provider.Result{Text: "cut"}
	})
	e.createProject("clips", cutModule)
	failed := func() []cockpitRun {
		var out []cockpitRun
		e.must("deck.processes_list", map[string]any{"failed": true}, &out)
		return out
	}
	id := e.start("clips", "cut", map[string]any{"clip": "a"})
	e.waitState(id, "error")
	fail.Store(false)
	e.waitState(e.start("clips", "cut", map[string]any{"clip": "b"}), "done")
	var tried struct{ Process int64 }
	e.must("deck.command_try", map[string]any{"live": true, "command": "clips.cut", "input": map[string]any{"clip": "a"}}, &tried)
	e.waitState(tried.Process, "done")
	if got := failed(); len(got) != 1 || got[0].ID != id || !got[0].Retry {
		t.Fatalf("another input and a trial make nothing good; the owner may retry it: %+v", got)
	}
	e.waitState(e.start("clips", "cut", map[string]any{"clip": "a"}), "done")
	if got := failed(); len(got) != 0 {
		t.Fatalf("a good run with the same input takes it off: %+v", got)
	}
	fail.Store(true)
	var started struct{ Process int64 }
	e.must("deck.command_try", map[string]any{"live": true, "command": "clips.cut", "input": map[string]any{"clip": "c"}}, &started)
	e.waitState(started.Process, "error")
	if got := failed(); len(got) != 0 {
		t.Fatalf("a failed trial is not in the cockpit: %+v", got)
	}
	_, tok := e.token("terminal", "clips.*")
	code, out := e.callWith(tok, "clips.trim", map[string]any{"clip": "a"})
	if code != 200 || json.Unmarshal(out, &started) != nil {
		t.Fatalf("the master starts trim: %d %s", code, out)
	}
	e.waitState(started.Process, "error")
	if got := failed(); len(got) != 1 || got[0].ID != started.Process || got[0].Retry {
		t.Fatalf("a failed run the owner may not call has no retry: %+v", got)
	}
	e.refused("deck.process_retry", map[string]any{"process": started.Process}, 403, "cannot be called by owner")
}

// TestARunCutByARestartIsInTheCockpit: a call deck lost to a restart did not work, so it waits in the cockpit.
func TestARunCutByARestartIsInTheCockpit(t *testing.T) {
	t.Parallel()
	svc, release := hangingService(t, 1)
	defer release(0)
	home := t.TempDir()
	clock := &Clock{now: t0}
	e := newEnvWith(t, home, &Fake{}, withClock(clock))
	e.Clock = clock
	e.createProject("clips", generateManifest(svc.URL))
	id := e.start("clips", "make", map[string]any{"topic": "cats"})
	e.waitFor("the call reaches the service", func() bool { return len(svc.requests()) == 1 })
	e2 := restart(t, e, home, &Fake{})
	var failed []cockpitRun
	e2.must("deck.processes_list", map[string]any{"failed": true}, &failed)
	if len(failed) != 1 || failed[0].ID != id {
		t.Fatalf("the interrupted run is among those that did not work: %+v", failed)
	}
}

// TestTheCockpitSaysHowManyFailuresItLeftOut: past its limit the list of what did not work says how many more there are.
func TestTheCockpitSaysHowManyFailuresItLeftOut(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{IsError: true, Text: "down"}
	})
	e.createProject("clips", cutModule)
	for i := range 22 {
		e.waitState(e.start("clips", "cut", map[string]any{"clip": fmt.Sprint(i)}), "error")
	}
	var more int
	_ = json.Unmarshal(e.screenData("", "cockpit", nil)["b4-1-0"]["more"], &more)
	if more != 2 {
		t.Fatalf("the cockpit shows 20 and says 2 more: %d", more)
	}
}

// TestTheStateOfARunIsSaidInTheLanguageOfTheOwner: the cockpit, the page of a process and the schedule say the
// state of a run in words deck gives, translated, with its tone and since when an open one works.
func TestTheStateOfARunIsSaidInTheLanguageOfTheOwner(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.N == 1 {
			return provider.Result{IsError: true, Text: "down"}
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.Result{Text: "cut"}
	})
	e.createProject("clips", cutModule)
	failed := e.start("clips", "cut", map[string]any{"clip": "a"})
	e.waitState(failed, "error")
	id := e.start("clips", "cut", map[string]any{"clip": "b"})
	e.waitState(id, "running")
	var running []listItem
	_ = json.Unmarshal(e.screenData("", "cockpit", map[string]any{"lang": "ru"})["b4-1-1"]["items"], &running)
	if len(running) != 1 || !strings.HasPrefix(running[0].Text, "работает на") || running[0].Since == "" {
		t.Fatalf("a running run says what it does in the owner's words and since when: %+v", running)
	}
	page, _ := json.Marshal(e.screenData("", "process", map[string]any{"record": failed, "lang": "ru"}))
	if !strings.Contains(string(page), `"value":"не получилось"`) || !strings.Contains(string(page), `"tone":"error"`) {
		t.Fatalf("the page of a failed run says so in red: %s", page)
	}
}
