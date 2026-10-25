package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/internal/store"
)

// jobsModule renders an item of its collection by an agent command whose input names the record.
const jobsModule = `{"version": 13,
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
			return provider.Result{Outcome: provider.Failed, Text: "the voice server is down"}
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

// TestARunThatTakesLongerThanUsualIsMarked: an open run past twice the usual of five runs of its command is marked
// longer than usual; within twice, or with fewer runs, it is not.
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
		return provider.Result{Outcome: provider.Failed, Text: "the voice server is down"}
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
			return provider.Result{Outcome: provider.Limited, ResetsAt: t0.Add(time.Hour).Unix()}
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
const cutModule = `{"version": 13,
  "commands": [
    {"name": "cut", "title": "Cut", "description": "Cuts a clip.", "class": "internal",
      "input": {"type": "object", "properties": {"clip": {"type": "string"}}},
      "executor": {"type": "agent", "prompt": "Cut the clip."}},
    {"name": "trim", "title": "Trim", "description": "Trims a clip.", "class": "internal", "callers": ["master"],
      "input": {"type": "object", "properties": {"clip": {"type": "string"}}},
      "executor": {"type": "agent", "prompt": "Trim the clip."}}]}`

// TestAFailureWithoutARecordLeavesOnlyWhenTheSameInputWorks: a failure of a command without a record outlives other
// inputs and trials, goes once its own input works; one the owner may not call has no retry.
func TestAFailureWithoutARecordLeavesOnlyWhenTheSameInputWorks(t *testing.T) {
	t.Parallel()
	var fail atomic.Bool
	fail.Store(true)
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if fail.Load() {
			return provider.Result{Outcome: provider.Failed, Text: "the cutter is down"}
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

// TestTheCockpitSaysHowManyFailuresItLeftOut: past its limit the list of what did not work, of failures that differ,
// says how many more there are.
func TestTheCockpitSaysHowManyFailuresItLeftOut(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: fmt.Sprintf("down %d", c.N)}
	})
	e.createProject("clips", cutModule)
	for i := range 22 {
		e.waitState(e.start("clips", "cut", map[string]any{"clip": fmt.Sprint(i)}), "error")
	}
	var more int
	_ = json.Unmarshal(e.screenData("", "cockpit", nil)["b2"]["more"], &more)
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
			return provider.Result{Outcome: provider.Failed, Text: "down"}
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
	_ = json.Unmarshal(e.screenData("", "cockpit", map[string]any{"lang": "ru"})["b1"]["items"], &running)
	if len(running) != 1 || running[0].Text != "" || !strings.Contains(running[0].Details, "работает на") || running[0].Since == "" {
		t.Fatalf("a running run says since when, and what it runs on in the owner's words under its details: %+v", running)
	}
	page, _ := json.Marshal(e.screenData("", "process", map[string]any{"record": failed, "lang": "ru"}))
	if !strings.Contains(string(page), `"value":"не получилось"`) || !strings.Contains(string(page), `"tone":"error"`) {
		t.Fatalf("the page of a failed run says so in red: %s", page)
	}
}

// TestALineOfTheCockpitSaysWhenItHappened: a failure, a note of the feed and what waits for the owner each carry the
// time they happened, which the line shows and gives in full on hover.
func TestALineOfTheCockpitSaysWhenItHappened(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: "down"}
	})
	e.createProject("clips", cutModule)
	e.waitState(e.start("clips", "cut", map[string]any{"clip": "a"}), "error")
	var failed []listItem
	_ = json.Unmarshal(e.screenData("", "cockpit", nil)["b2"]["items"], &failed)
	if len(failed) != 1 || failed[0].Since == "" {
		t.Fatalf("a failure says when it happened: %+v", failed)
	}
}

// TestFailedRunsAreAListOfTheirOwnInTheSchedule: the schedule has the failed runs apart from the history, with why;
// a run closed on the cockpit stays there.
func TestFailedRunsAreAListOfTheirOwnInTheSchedule(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: "the voice server is down"}
	})
	e.createProject("shorts", jobsModule)
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(map[string]any{"slug": "knights", "title": "Knights"})}, nil)
	var recs []struct{ ID int64 }
	e.must("deck.records_list", map[string]any{"project": "shorts", "collection": "videos"}, &recs)
	id := e.start("shorts", "voice", map[string]any{"record": recs[0].ID})
	e.waitState(id, "done", "error")
	e.must("deck.process_dismiss", map[string]any{"process": id}, nil)
	failed := blockData[[]screenRow](t, e.screenData("", "schedule", nil)["b0-1-0"]["rows"])
	if len(failed) != 1 || failed[0].Cells[0] != "voice" || failed[0].Cells[4] != float64(id) || !strings.Contains(fmt.Sprint(failed[0].Cells[2]), "voice server is down") {
		t.Fatalf("the failed run is in its own list with why, closed on the cockpit or not: %+v", failed)
	}
}

type todayPulse struct {
	State, Line, Mark string
	Failed, Notes     int64
}

func (e *Env) today(lang string) todayPulse {
	e.T.Helper()
	var m struct{ Pulse todayPulse }
	e.must("deck.menu", map[string]any{"lang": lang}, &m)
	return m.Pulse
}

// TestTodaySaysWhatIsNotSeenBelow: the first line of Today says only what the blocks under it do not: what to do in
// an empty deck, that all is well, the failures in the feed, the rest beside the decisions, in the owner's language.
func TestTodaySaysWhatIsNotSeenBelow(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: "the voice server is down"}
	})
	if h := e.today(""); h.State != "calm" || !strings.HasPrefix(h.Line, "Tell Deki what to do") {
		t.Fatalf("a quiet empty deck says what to do: %+v", h)
	}
	e.createProject("shorts", jobsModule)
	if h := e.today(""); h.Line != "All is well: nothing to decide, no failures." || h.Mark != "check" {
		t.Fatalf("a quiet deck with a module says all is well: %+v", h)
	}
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(map[string]any{"slug": "knights", "title": "Knights"})}, nil)
	var recs []struct{ ID int64 }
	e.must("deck.records_list", map[string]any{"project": "shorts", "collection": "videos"}, &recs)
	e.waitState(e.start("shorts", "voice", map[string]any{"record": recs[0].ID}), "error")
	e.must("deck.notify", map[string]any{"project": "shorts", "text": "a new video is ready"}, nil)
	if h := e.today(""); h.State != "errors" || h.Failed != 1 || h.Notes != 1 || h.Line != "Nothing to decide. One failure is in the feed below." || h.Mark != "alert-circle" {
		t.Fatalf("a failure alone is said to be in the feed: %+v", h)
	}
	if ru := e.today("ru"); ru.Line != "Решать нечего. Один сбой — в ленте ниже." {
		t.Fatalf("the line speaks the language of the owner: %+v", ru)
	}
	_, tok := e.token("asker", "deck.model_set")
	if code, out := e.callWith(tok, "deck.model_set", map[string]any{"connection": "none", "model": "x"}); code != 200 || !strings.Contains(string(out), `"waiting"`) {
		t.Fatalf("a token's call waits for the owner: %d %s", code, out)
	}
	if h := e.today("ru"); h.Line != "Остальное идёт само, кроме одного сбоя в ленте." || h.Mark != "hand-finger" {
		t.Fatalf("beside a decision the line says the rest, but for the failure: %+v", h)
	}
	var asks []struct{ Allow string }
	e.must("deck.asks_list", map[string]any{"lang": "ru"}, &asks)
	if len(asks) != 1 || asks[0].Allow != "Разрешить: Модель" {
		t.Fatalf("the yes of a decision says what it lets happen: %+v", asks)
	}
}

// TestTheFeedOfTodayHoldsWhatHappenedAndFailed: one feed holds notes and open failures by time, a repeated failure one
// line; read all and close clear it, while the schedule and the history keep them.
func TestTheFeedOfTodayHoldsWhatHappenedAndFailed(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: "the voice server is down"}
	})
	e.createProject("shorts", jobsModule)
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(
		map[string]any{"slug": "knights", "title": "Knights"}, map[string]any{"slug": "dragons", "title": "Dragons"})}, nil)
	var recs []struct{ ID int64 }
	e.must("deck.records_list", map[string]any{"project": "shorts", "collection": "videos"}, &recs)
	for _, r := range recs {
		e.waitState(e.start("shorts", "voice", map[string]any{"record": r.ID}), "error")
	}
	for _, text := range []string{"one", "two", "three"} {
		e.must("deck.notify", map[string]any{"project": "shorts", "text": text}, nil)
	}
	feed := e.screenData("", "cockpit", nil)["b2"]
	items := blockData[[]listItem](t, feed["items"])
	head := blockData[[]map[string]any](t, feed["head"])
	var fails []listItem
	for _, it := range items {
		if strings.Contains(it.Meta, "voice server is down") {
			fails = append(fails, it)
		}
	}
	if len(items) != 4 || len(fails) != 1 || items[0].Title != "three" || len(head) != 1 || head[0]["label"] != "Read all (3)" {
		t.Fatalf("three notes and the failure of two runs as one line, newest first, read all counting the notes: %+v %+v", items, head)
	}
	verbs := []string{}
	for _, a := range fails[0].Actions {
		verbs = append(verbs, fmt.Sprint(a["verb"]))
	}
	if !slices.Equal(verbs, []string{"retry", "open", "close"}) {
		t.Fatalf("a failure is retried, opened or closed: %v", verbs)
	}
	e.must("deck.feed_read", map[string]any{"all": true}, nil)
	var failed []cockpitRun
	e.must("deck.processes_list", map[string]any{"failed": true}, &failed)
	for _, f := range failed {
		e.must("deck.process_dismiss", map[string]any{"process": f.ID}, nil)
	}
	if h := e.today(""); h.Failed != 0 || h.Notes != 0 || h.State != "calm" {
		t.Fatalf("the feed is read and its failure closed: %+v", h)
	}
	if feed := e.screenData("", "cockpit", nil)["b2"]; feed["head"] != nil || len(blockData[[]listItem](t, feed["items"])) != 3 {
		t.Fatalf("the notes stay in the feed, read, without the failure: %s %s", feed["head"], feed["items"])
	}
	if runs := blockData[[]screenRow](t, e.screenData("", "schedule", nil)["b0-1-0"]["rows"]); len(runs) != 2 {
		t.Fatalf("the failed runs of the schedule keep both: %+v", runs)
	}
	notes := blockData[[]screenRow](t, e.screenData("", "cockpit", map[string]any{"dialog": "changes"})["b3-1"]["rows"])
	if len(notes) != 3 || notes[0].Cells[2] != "read" {
		t.Fatalf("the history keeps the notes, read: %+v", notes)
	}
	e.refused("deck.feed_read", map[string]any{}, 422, "give the entry of one note, or all")
	e.refused("deck.process_dismiss", map[string]any{"all": true, "process": 1}, 422, "give one process, or all")
}

// TestTheOpenFailuresAreThoseNotClosedNorMadeGood: the Failed tab of the Schedule lists every failed run, and
// processes_list with failed only those still open: not closed, not made good.
func TestTheOpenFailuresAreThoseNotClosedNorMadeGood(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if n := calls.Add(1); c.command() != "voice" || n <= 3 {
			return provider.Result{Outcome: provider.Failed, Text: fmt.Sprintf("the voice server is down (%d)", n)}
		}
		return provider.Result{Text: "voiced"}
	})
	e.createProject("shorts", jobsModule)
	e.must("deck.records_upsert", map[string]any{"project": "shorts", "collection": "videos", "records": jsonl(
		map[string]any{"slug": "knights"}, map[string]any{"slug": "dragons"}, map[string]any{"slug": "castles"})}, nil)
	var recs []struct{ ID int64 }
	e.must("deck.records_list", map[string]any{"project": "shorts", "collection": "videos"}, &recs)
	var ids []int64
	for _, r := range recs {
		id := e.start("shorts", "voice", map[string]any{"record": r.ID})
		e.waitState(id, "error")
		ids = append(ids, id)
	}
	e.must("deck.process_dismiss", map[string]any{"process": ids[1]}, nil)
	e.waitState(e.start("shorts", "voice", map[string]any{"record": recs[2].ID}), "done")

	var open []struct{ ID int64 }
	e.must("deck.processes_list", map[string]any{"failed": true}, &open)
	failed := blockData[[]screenRow](t, e.screenData("", "schedule", nil)["b0-1-0"]["rows"])
	if len(open) != 1 || open[0].ID != ids[0] || len(failed) != 3 {
		t.Fatalf("3 failed as the Failed tab lists them, 1 open as processes_list gives it: %+v, %d rows", open, len(failed))
	}
}
