package owner

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/tests/harness"
)

// jobsModule renders an item of its collection by an agent command whose input names the record.
var jobsModule = harness.Manifest(`{"collections": [{"name": "videos", "description": "A video.", "key": "slug", "display": "title",
    "schema": {"type": "object", "properties": {"slug": {"type": "string"}, "title": {"type": "string"}}, "required": ["slug"]}}],
  "commands": [{"name": "voice", "title": "Voice", "description": "Voices a video.", "class": "internal",
    "input": {"type": "object", "properties": {"record": {"type": "integer"}}},
    "executor": {"type": "agent", "prompt": "Voice the video."}}]}`)

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
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if fail.Load() {
			return provider.Result{Outcome: provider.Failed, Text: "the voice server is down"}
		}
		return provider.Result{Text: "voiced"}
	}))

	e.Project("films", jobsModule)
	e.Must("deck.records_upsert", map[string]any{"project": "films", "collection": "videos", "records": harness.Jsonl(map[string]any{"slug": "knights", "title": "Knights"})}, nil)
	var recs []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "films", "collection": "videos"}, &recs)
	failed := func() []cockpitRun {
		var out []cockpitRun
		e.Must("deck.processes_list", map[string]any{"failed": true}, &out)
		return out
	}
	id := e.Start("films", "voice", map[string]any{"record": recs[0].ID})
	e.WaitState(id, "done", "error")
	if got := failed(); len(got) != 1 || got[0].ID != id || got[0].RecordTitle != "Knights" || got[0].Error == "" {
		t.Fatalf("the failed run is in the cockpit with its record and why: %+v", got)
	}
	fail.Store(false)
	var again struct{ Process int64 }
	e.Must("deck.process_retry", map[string]any{"process": id}, &again)
	if got := e.WaitState(again.Process, "done", "error"); got.State != "done" || len(failed()) != 0 {
		t.Fatalf("a retry runs it again with its input, and a run that went well takes it off: %+v %+v", got, failed())
	}
	fail.Store(true)
	id = e.Start("films", "voice", map[string]any{"record": recs[0].ID})
	e.WaitState(id, "done", "error")
	e.Must("deck.process_dismiss", map[string]any{"process": id}, nil)
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
			clock := &harness.Clock{At: harness.T0}
			e := harness.New(t, harness.WithProvider(&harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
				if int(n.Add(1)) > tc.runs {
					select {
					case <-release:
					case <-ctx.Done():
					}
				} else {
					clock.Add(time.Minute)
				}
				return provider.Result{Text: "voiced"}
			}}), harness.WithClock(clock))

			e.Project("films", jobsModule)
			for range tc.runs {
				e.WaitState(e.Start("films", "voice", nil), "done", "error")
			}
			id := e.Start("films", "voice", nil)
			e.WaitState(id, "running")
			clock.Add(tc.at)
			var open []cockpitRun
			e.Must("deck.processes_list", map[string]any{"open": true}, &open)
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
	clock := &harness.Clock{At: harness.T0}
	e := harness.New(t, harness.WithProvider(&harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		if c.N > 1 {
			<-release
		}
		return provider.Result{Outcome: provider.Failed, Text: "the voice server is down"}
	}}), harness.WithClock(clock))

	e.Project("films", jobsModule)
	id := e.Start("films", "voice", nil)
	e.WaitState(id, "error")
	e.Must("deck.process_dismiss", map[string]any{"process": id}, nil)
	clock.Add(8 * 24 * time.Hour)
	e.Must("deck.process_rework", map[string]any{"process": id, "remark": "once more"}, nil)
	clock.Add(time.Minute)
	var open []struct {
		cockpitRun
		Result any
	}
	e.Must("deck.processes_list", map[string]any{"open": true}, &open)
	if len(open) != 1 || open[0].Since != store.Time(clock.Now().Add(-time.Minute)) || open[0].Result != nil {
		t.Fatalf("the run works since it was sent back and has no result yet: %+v", open)
	}
	close(release)
	e.WaitState(id, "error")
	var failed []cockpitRun
	e.Must("deck.processes_list", map[string]any{"failed": true}, &failed)
	if len(failed) != 1 || failed[0].ID != id {
		t.Fatalf("the run failed again, so it is back in the cockpit: %+v", failed)
	}
}

// TestARunThatWaitsIsNotLongerThanUsual: a run that waits, here on a usage limit, is not slow work.
func TestARunThatWaitsIsNotLongerThanUsual(t *testing.T) {
	t.Parallel()
	clock := &harness.Clock{At: harness.T0}
	e := harness.New(t, harness.WithProvider(&harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		if c.N > usualRunsForTest {
			return provider.Result{Outcome: provider.Limited, ResetsAt: harness.T0.Add(time.Hour).Unix()}
		}
		clock.Add(time.Second)
		return provider.Result{Text: "voiced"}
	}}), harness.WithClock(clock))

	e.Project("films", jobsModule)
	for range usualRunsForTest {
		e.WaitState(e.Start("films", "voice", nil), "done", "error")
	}
	id := e.Start("films", "voice", nil)
	e.WaitState(id, "rate_limited")
	clock.Add(3 * time.Minute)
	var open []cockpitRun
	e.Must("deck.processes_list", map[string]any{"open": true}, &open)
	if len(open) != 1 || open[0].ID != id || open[0].Long || !open[0].Waits || open[0].Tone != "warn" || open[0].Word != "paused on a usage limit" {
		t.Fatalf("a run that waits is not marked longer than usual, and says it waits: %+v", open)
	}
}

// cutModule cuts a clip by name; trim is a step the master calls, not the owner.
var cutModule = harness.Manifest(`{"commands": [
    {"name": "cut", "title": "Cut", "description": "Cuts a clip.", "class": "internal",
      "input": {"type": "object", "properties": {"clip": {"type": "string"}}},
      "executor": {"type": "agent", "prompt": "Cut the clip."}},
    {"name": "trim", "title": "Trim", "description": "Trims a clip.", "class": "internal", "callers": ["master"],
      "input": {"type": "object", "properties": {"clip": {"type": "string"}}},
      "executor": {"type": "agent", "prompt": "Trim the clip."}}]}`)

// TestAFailureWithoutARecordLeavesOnlyWhenTheSameInputWorks: a failure of a command without a record outlives other
// inputs and trials, goes once its own input works; one the owner may not call has no retry.
func TestAFailureWithoutARecordLeavesOnlyWhenTheSameInputWorks(t *testing.T) {
	t.Parallel()
	var fail atomic.Bool
	fail.Store(true)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if fail.Load() {
			return provider.Result{Outcome: provider.Failed, Text: "the cutter is down"}
		}
		return provider.Result{Text: "cut"}
	}))

	e.Project("clips", cutModule)
	failed := func() []cockpitRun {
		var out []cockpitRun
		e.Must("deck.processes_list", map[string]any{"failed": true}, &out)
		return out
	}
	id := e.Start("clips", "cut", map[string]any{"clip": "a"})
	e.WaitState(id, "error")
	fail.Store(false)
	e.WaitState(e.Start("clips", "cut", map[string]any{"clip": "b"}), "done")
	var tried struct{ Process int64 }
	e.Must("deck.command_try", map[string]any{"live": true, "command": "clips.cut", "input": map[string]any{"clip": "a"}}, &tried)
	e.WaitState(tried.Process, "done")
	if got := failed(); len(got) != 1 || got[0].ID != id || !got[0].Retry {
		t.Fatalf("another input and a trial make nothing good; the owner may retry it: %+v", got)
	}
	e.WaitState(e.Start("clips", "cut", map[string]any{"clip": "a"}), "done")
	if got := failed(); len(got) != 0 {
		t.Fatalf("a good run with the same input takes it off: %+v", got)
	}
	fail.Store(true)
	var started struct{ Process int64 }
	e.Must("deck.command_try", map[string]any{"live": true, "command": "clips.cut", "input": map[string]any{"clip": "c"}}, &started)
	e.WaitState(started.Process, "error")
	if got := failed(); len(got) != 0 {
		t.Fatalf("a failed trial is not in the cockpit: %+v", got)
	}
	_, tok := e.Token("terminal", "clips.*")
	code, out := e.As(tok).Call("clips.trim", map[string]any{"clip": "a"})
	if code != 200 || json.Unmarshal(out, &started) != nil {
		t.Fatalf("the master starts trim: %d %s", code, out)
	}
	e.WaitState(started.Process, "error")
	if got := failed(); len(got) != 1 || got[0].ID != started.Process || got[0].Retry {
		t.Fatalf("a failed run the owner may not call has no retry: %+v", got)
	}
	e.Refused("deck.process_retry", map[string]any{"process": started.Process}, 403, "cannot be called by owner")
}

// TestARunCutByARestartIsInTheCockpit: a call deck lost to a restart did not work, so it waits in the cockpit.
func TestARunCutByARestartIsInTheCockpit(t *testing.T) {
	t.Parallel()
	svc, release := harness.HangingService(t, 1)
	defer release(0)
	home := t.TempDir()
	clock := &harness.Clock{At: harness.T0}
	e := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}), harness.WithClock(clock))
	e.Project("clips", harness.GenerateManifest(svc.URL))
	id := e.Start("clips", "make", map[string]any{"topic": "cats"})
	e.Until("the call reaches the service", func() bool { return len(svc.Requests()) == 1 })
	e2 := e.Restart(harness.WithProvider(&harness.Fake{}))
	e2.Resume()
	var failed []cockpitRun
	e2.Must("deck.processes_list", map[string]any{"failed": true}, &failed)
	if len(failed) != 1 || failed[0].ID != id {
		t.Fatalf("the interrupted run is among those that did not work: %+v", failed)
	}
}

// TestTheCockpitSaysHowManyFailuresItLeftOut: past its limit the list of what did not work, of failures that differ,
// says how many more there are.
func TestTheCockpitSaysHowManyFailuresItLeftOut(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: fmt.Sprintf("down %d", c.N)}
	}))

	e.Project("clips", cutModule)
	for i := range 22 {
		e.WaitState(e.Start("clips", "cut", map[string]any{"clip": fmt.Sprint(i)}), "error")
	}
	var more int
	_ = json.Unmarshal(e.Screen("", "cockpit", nil)["b2"]["more"], &more)
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
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.N == 1 {
			return provider.Result{Outcome: provider.Failed, Text: "down"}
		}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.Result{Text: "cut"}
	}))

	e.Project("clips", cutModule)
	failed := e.Start("clips", "cut", map[string]any{"clip": "a"})
	e.WaitState(failed, "error")
	id := e.Start("clips", "cut", map[string]any{"clip": "b"})
	e.WaitState(id, "running")
	var running []harness.ListItem
	_ = json.Unmarshal(e.Screen("", "cockpit", map[string]any{"lang": "ru"})["b1"]["items"], &running)
	if len(running) != 1 || running[0].Text != "" || !strings.Contains(running[0].Details, "работает на") || running[0].Since == "" {
		t.Fatalf("a running run says since when, and what it runs on in the owner's words under its details: %+v", running)
	}
	page, _ := json.Marshal(e.Screen("", "process", map[string]any{"record": failed, "lang": "ru"}))
	if !strings.Contains(string(page), `"value":"не получилось"`) || !strings.Contains(string(page), `"tone":"error"`) {
		t.Fatalf("the page of a failed run says so in red: %s", page)
	}
}

// TestALineOfTheCockpitSaysWhenItHappened: a failure, a note of the feed and what waits for the owner each carry the
// time they happened, which the line shows and gives in full on hover.
func TestALineOfTheCockpitSaysWhenItHappened(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: "down"}
	}))

	e.Project("clips", cutModule)
	e.WaitState(e.Start("clips", "cut", map[string]any{"clip": "a"}), "error")
	var failed []harness.ListItem
	_ = json.Unmarshal(e.Screen("", "cockpit", nil)["b2"]["items"], &failed)
	if len(failed) != 1 || failed[0].Since == "" {
		t.Fatalf("a failure says when it happened: %+v", failed)
	}
}

// TestFailedRunsStayInTheHistoryOfTheSchedule: the history of the schedule finds a failed run by its state, in red;
// a run closed on the cockpit stays there.
func TestFailedRunsStayInTheHistoryOfTheSchedule(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: "the voice server is down"}
	}))

	e.Project("films", jobsModule)
	e.Must("deck.records_upsert", map[string]any{"project": "films", "collection": "videos", "records": harness.Jsonl(map[string]any{"slug": "knights", "title": "Knights"})}, nil)
	var recs []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "films", "collection": "videos"}, &recs)
	id := e.Start("films", "voice", map[string]any{"record": recs[0].ID})
	e.WaitState(id, "done", "error")
	e.Must("deck.process_dismiss", map[string]any{"process": id}, nil)
	failed := harness.BlockData[[]harness.ScreenRow](t, e.Screen("", "schedule", map[string]any{"filter": `{"b0-1-0": {"state": "error"}}`})["b0-1-0"]["rows"])
	if len(failed) != 1 || failed[0].Cells[0] != "Voice" || failed[0].ID != float64(id) || failed[0].Tones[2] != "error" || failed[0].Cells[4] != "by a button" {
		t.Fatalf("the failed run is in the history by its state, closed on the cockpit or not: %+v", failed)
	}
	if page, _ := json.Marshal(e.Screen("", "process", map[string]any{"record": id})); !strings.Contains(string(page), "voice server is down") {
		t.Fatalf("the page of the failed run says why: %s", page)
	}
}

func today(e *harness.Env, lang string) harness.TodayPulse {
	e.T.Helper()
	var m struct{ Pulse harness.TodayPulse }
	e.Must("deck.menu", map[string]any{"lang": lang}, &m)
	return m.Pulse
}

// TestTodaySaysWhatIsNotSeenBelow: the first line of Today says only what the blocks under it do not: what to do in
// an empty deck, that all is well, the failures in the feed, the rest beside the decisions, in the owner's language.
func TestTodaySaysWhatIsNotSeenBelow(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: "the voice server is down"}
	}))

	if h := today(e, ""); h.State != "calm" || !strings.HasPrefix(h.Line, "Tell Deki what to do") {
		t.Fatalf("a quiet empty deck says what to do: %+v", h)
	}
	e.Project("films", jobsModule)
	if h := today(e, ""); h.Line != "All is well: nothing to decide, no failures." || h.Mark != "check" {
		t.Fatalf("a quiet deck with a module says all is well: %+v", h)
	}
	e.Must("deck.records_upsert", map[string]any{"project": "films", "collection": "videos", "records": harness.Jsonl(map[string]any{"slug": "knights", "title": "Knights"})}, nil)
	var recs []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "films", "collection": "videos"}, &recs)
	e.WaitState(e.Start("films", "voice", map[string]any{"record": recs[0].ID}), "error")
	e.Must("deck.notify", map[string]any{"project": "films", "text": "a new video is ready"}, nil)
	if h := today(e, ""); h.State != "errors" || h.Failed != 1 || h.Notes != 1 || h.Line != "Nothing to decide. One failure is in the feed below." || h.Mark != "alert-circle" {
		t.Fatalf("a failure alone is said to be in the feed: %+v", h)
	}
	if ru := today(e, "ru"); ru.Line != "Решать нечего. Один сбой — в ленте ниже." {
		t.Fatalf("the line speaks the language of the owner: %+v", ru)
	}
	_, tok := e.Token("asker", "deck.model_set")
	if code, out := e.As(tok).Call("deck.model_set", map[string]any{"connection": "none", "model": "x"}); code != 200 || !strings.Contains(string(out), `"waiting"`) {
		t.Fatalf("a token's call waits for the owner: %d %s", code, out)
	}
	if h := today(e, "ru"); h.Line != "Остальное идёт само, кроме одного сбоя в ленте." || h.Mark != "hand-finger" {
		t.Fatalf("beside a decision the line says the rest, but for the failure: %+v", h)
	}
	var asks []struct{ Allow string }
	e.Must("deck.asks_list", map[string]any{"lang": "ru"}, &asks)
	if len(asks) != 1 || asks[0].Allow != "Разрешить: Задать модель" {
		t.Fatalf("the yes of a decision says what it lets happen: %+v", asks)
	}
}

// TestTheFeedOfTodayHoldsWhatHappenedAndFailed: one feed holds notes and open failures by time, a repeated failure one
// line; read all and close clear it, while the schedule and the history keep them.
func TestTheFeedOfTodayHoldsWhatHappenedAndFailed(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: "the voice server is down"}
	}))

	e.Project("films", jobsModule)
	e.Must("deck.records_upsert", map[string]any{"project": "films", "collection": "videos", "records": harness.Jsonl(
		map[string]any{"slug": "knights", "title": "Knights"}, map[string]any{"slug": "dragons", "title": "Dragons"})}, nil)
	var recs []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "films", "collection": "videos"}, &recs)
	for _, r := range recs {
		e.WaitState(e.Start("films", "voice", map[string]any{"record": r.ID}), "error")
	}
	for _, text := range []string{"one", "two", "three"} {
		e.Must("deck.notify", map[string]any{"project": "films", "text": text}, nil)
	}
	feed := e.Screen("", "cockpit", nil)["b2"]
	items := harness.BlockData[[]harness.ListItem](t, feed["items"])
	head := harness.BlockData[[]map[string]any](t, feed["head"])
	var fails []harness.ListItem
	for _, it := range items {
		if strings.Contains(it.Meta, "voice server is down") {
			fails = append(fails, it)
		}
	}
	if len(items) != 4 || len(fails) != 1 || items[0].Title != "three" || len(head) != 1 || head[0]["verb"] != "read_all" {
		t.Fatalf("three notes and the failure of two runs as one line, newest first, read all counting the notes: %+v %+v", items, head)
	}
	verbs := []string{}
	for _, a := range fails[0].Actions {
		verbs = append(verbs, fmt.Sprint(a["verb"]))
	}
	if !slices.Equal(verbs, []string{"retry", "open", "close"}) {
		t.Fatalf("a failure is retried, opened or closed: %v", verbs)
	}
	e.Must("deck.feed_read", map[string]any{"all": true}, nil)
	var failed []cockpitRun
	e.Must("deck.processes_list", map[string]any{"failed": true}, &failed)
	for _, f := range failed {
		e.Must("deck.process_dismiss", map[string]any{"process": f.ID}, nil)
	}
	if h := today(e, ""); h.Failed != 0 || h.Notes != 0 || h.State != "calm" {
		t.Fatalf("the feed is read and its failure closed: %+v", h)
	}
	if feed := e.Screen("", "cockpit", nil)["b2"]; feed["head"] != nil || len(harness.BlockData[[]harness.ListItem](t, feed["items"])) != 3 {
		t.Fatalf("the notes stay in the feed, read, without the failure: %s %s", feed["head"], feed["items"])
	}
	if runs := harness.BlockData[[]harness.ScreenRow](t, e.Screen("", "schedule", map[string]any{"filter": `{"b0-1-0": {"state": "error"}}`})["b0-1-0"]["rows"]); len(runs) != 2 {
		t.Fatalf("the history of the schedule keeps both failed runs: %+v", runs)
	}
	notes := harness.BlockData[[]harness.ScreenRow](t, e.Screen("", "cockpit", map[string]any{"dialog": "changes"})["b3-1"]["rows"])
	if len(notes) != 3 || notes[0].Cells[2] != "read" {
		t.Fatalf("the history keeps the notes, read: %+v", notes)
	}
	e.Refused("deck.feed_read", map[string]any{}, 422, "give the entry of one note, or all")
	e.Refused("deck.process_dismiss", map[string]any{"all": true, "process": 1}, 422, "give one process, or all")
}

// TestTheOpenFailuresAreThoseNotClosedNorMadeGood: the history of the Schedule finds every failed run, and
// processes_list with failed only those still open: not closed, not made good.
func TestTheOpenFailuresAreThoseNotClosedNorMadeGood(t *testing.T) {
	t.Parallel()
	var calls atomic.Int64
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if n := calls.Add(1); c.Command() != "voice" || n <= 3 {
			return provider.Result{Outcome: provider.Failed, Text: fmt.Sprintf("the voice server is down (%d)", n)}
		}
		return provider.Result{Text: "voiced"}
	}))

	e.Project("films", jobsModule)
	e.Must("deck.records_upsert", map[string]any{"project": "films", "collection": "videos", "records": harness.Jsonl(
		map[string]any{"slug": "knights"}, map[string]any{"slug": "dragons"}, map[string]any{"slug": "castles"})}, nil)
	var recs []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "films", "collection": "videos"}, &recs)
	var ids []int64
	for _, r := range recs {
		id := e.Start("films", "voice", map[string]any{"record": r.ID})
		e.WaitState(id, "error")
		ids = append(ids, id)
	}
	e.Must("deck.process_dismiss", map[string]any{"process": ids[1]}, nil)
	e.WaitState(e.Start("films", "voice", map[string]any{"record": recs[2].ID}), "done")

	var open []struct{ ID int64 }
	e.Must("deck.processes_list", map[string]any{"failed": true}, &open)
	failed := harness.BlockData[[]harness.ScreenRow](t, e.Screen("", "schedule", map[string]any{"filter": `{"b0-1-0": {"state": "error"}}`})["b0-1-0"]["rows"])
	if len(open) != 1 || open[0].ID != ids[0] || len(failed) != 3 {
		t.Fatalf("3 failed as the history finds them, 1 open as processes_list gives it: %+v, %d rows", open, len(failed))
	}
}

// TestCockpitShowsWhatWaitsWithItsVerbs: each item says what and where, holds who and the whole frozen call in its
// details and offers the buttons of its kind; an expired confirmation keeps only deny, which clears it.
func TestCockpitShowsWhatWaitsWithItsVerbs(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"ok": true}`) })
	clock := &harness.Clock{At: time.Now()}
	e := harness.New(t, harness.WithAgent(harness.WebAgent()), harness.WithClock(clock), harness.WithConfig(harness.WithTTL))
	check := harness.NewCatalogCheck(t)
	p := e.Project("notes", harness.WebManifest(svc.URL))

	first := e.Run(p, "work")
	e.WaitState(first, "waiting_answer", "done")
	e.App.Sweep(context.Background(), clock.Add(24*time.Hour+time.Minute))
	second := e.Run(p, "work")
	e.Until("the second confirmation", func() bool { return len(e.CockpitItems()) == 2 })
	e.Run(p, "ask")
	e.Until("the question", func() bool { return len(e.CockpitItems()) == 3 })

	check.Messages(t, e.ScreenMessages("", "cockpit", nil))
	var feed []struct{ Kind string }
	e.Must("deck.feed_list", map[string]any{"failures": true}, &feed)
	if slices.ContainsFunc(feed, func(f struct{ Kind string }) bool { return f.Kind == "failure" }) {
		t.Fatalf("no failing check, no failure in the feed: %+v", feed)
	}
	items := e.CockpitItems()
	old, fresh, q := items[0], items[1], items[2]
	verbs := func(it harness.ListItem) []string {
		var out []string
		for _, a := range it.Actions {
			out = append(out, fmt.Sprint(cmp.Or(a["label"], a["verb"])))
		}
		return out
	}
	by := fmt.Sprintf("#%d work", second)
	if fresh.Title != "post" || !strings.HasSuffix(fresh.Text, "POST "+svc.URL+"/items/n2") || fresh.Meta != "notes · Work" ||
		!strings.HasPrefix(fresh.Details, by+"\nnotes.post\n") {
		t.Fatalf("a confirmation says what, where, its module and who asks in words; the process and the command under its details: %+v", fresh)
	}
	if !strings.Contains(fresh.Details, `"id": "n2"`) || !strings.Contains(fresh.Details, `"text": "hi"`) || strings.Contains(fresh.Details, `"process": `) ||
		strings.Contains(fresh.Details, `"caller"`) {
		t.Fatalf("the call and only the call is in the details: %s", fresh.Details)
	}
	if got := verbs(fresh); !slices.Equal(got, []string{"Allow: post", "deny", "Always let notes run post", "deny_because"}) ||
		fresh.Actions[0]["primary"] != true || fresh.Actions[0]["until"] == nil || fresh.Actions[3]["input"] != "reason" {
		t.Fatalf("a confirmation offers what its yes allows first until it expires, then deny with a reason: %v", fresh.Actions)
	}
	if got := verbs(old); !slices.Equal(got, []string{"deny"}) || !strings.Contains(old.Details, `"id": "n1"`) {
		t.Fatalf("an expired confirmation keeps only deny: %+v", old)
	}
	if got := verbs(q); !slices.Equal(got, []string{"answer"}) || len(q.Questions) != 1 || q.Actions[0]["input"] != "answer" || q.Title != "Question" {
		t.Fatalf("a question carries its options and the answer button: %+v", q)
	}

	params := func(it harness.ListItem, i int) map[string]any { return it.Actions[i]["params"].(map[string]any) }
	if code, out := e.Call("deck.ask_answer", harness.Merge(params(old, 0), map[string]any{"answer": "allow"})); code != 409 || !strings.Contains(string(out), "expired") {
		t.Fatalf("an expired confirmation cannot be allowed: %d %s", code, out)
	}
	e.Must("deck.ask_answer", params(old, 0), nil)
	e.Must("deck.ask_answer", harness.Merge(params(q, 0), map[string]any{"answer": `{"Which topic?": "go"}`}), nil)
	e.Must("deck.ask_answer", harness.Merge(params(fresh, 3), map[string]any{"reason": "not today"}), nil)
	if a, _ := e.App.Store.Q.GetAsk(context.Background(), int64(fresh.ID.(float64))); a.Answer != "deny:not today" {
		t.Fatalf("deny because sends the reason with the no: %q", a.Answer)
	}
	if left := e.CockpitItems(); len(left) != 0 {
		t.Fatalf("answered and cleared items leave the cockpit: %+v", left)
	}
	if len(svc.Requests()) != 0 {
		t.Fatalf("no call was made: %d", len(svc.Requests()))
	}
	if got := e.Calls(second); !strings.Contains(got, "rejected") {
		t.Fatalf("deny because reaches the journal as a refusal: %s", got)
	}
}

// TestAllowAfterTheTermIsRefusedBeforeTheSweep: a confirmation past its term cannot be allowed even before the
// sweep marks it expired.
func TestAllowAfterTheTermIsRefusedBeforeTheSweep(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"ok": true}`) })
	clock := &harness.Clock{At: time.Now()}
	e := harness.New(t, harness.WithAgent(harness.WebAgent()), harness.WithClock(clock), harness.WithConfig(harness.WithTTL))
	p := e.Project("notes", harness.WebManifest(svc.URL))
	e.Run(p, "work")
	a := e.WaitAsk()
	clock.Add(24*time.Hour + time.Minute)
	if code, out := e.Call("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}); code != 409 || !strings.Contains(string(out), "expired") {
		t.Fatalf("allow after the term is refused: %d %s", code, out)
	}
	if made := svc.To("/items/"); len(made) != 0 {
		t.Fatalf("no call was made: %+v", made)
	}
	if items := e.CockpitItems(); len(items) != 1 || len(items[0].Actions) != 1 || items[0].Actions[0]["verb"] != "deny" {
		t.Fatalf("the confirmation shows as expired with deny only: %+v", items)
	}
}

// TestAConfirmationOfAChangedToolIsClosedAsOutOfDate: a waiting call of a tool that changed since is no question
// for the owner any more: deck closes it unmade, and the agent hears it is out of date.
func TestAConfirmationOfAChangedToolIsClosedAsOutOfDate(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{"ok": true}`) })
	e := harness.New(t, harness.WithAgent(harness.WebAgent()), harness.WithConfig(harness.WithTTL))
	p := e.Project("notes", harness.WebManifest(svc.URL))
	id := e.Run(p, "work")
	e.WaitAsk()
	moved := strings.Replace(harness.WebManifest(svc.URL), svc.URL+"/items/{id}", svc.URL+"/archive/{id}", 1)
	e.Must("deck.manifest_save", map[string]any{"project": "notes", "manifest": moved}, nil)
	e.Until("deck to close the out-of-date call", func() bool {
		return !slices.ContainsFunc(e.CockpitItems(), func(it harness.ListItem) bool { return strings.Contains(it.Details, "notes.post") })
	})
	var journal []struct{ Tool, Outcome, Error string }
	e.Must("deck.calls_list", map[string]any{"project": "notes"}, &journal)
	if !slices.ContainsFunc(journal, func(j struct{ Tool, Outcome, Error string }) bool {
		return j.Tool == "notes.post" && j.Outcome == "rejected" && strings.Contains(j.Error, "out of date")
	}) || len(svc.Requests()) != 0 {
		t.Fatalf("the call is closed unmade as out of date: %+v", journal)
	}
	e.WaitState(id, "done", "error", "running", "waiting_answer")
}

// TestRunningNowKeepsALongProcessBehindManyShortOnes: an open process stays in the cockpit however many finished
// after it started.
func TestRunningNowKeepsALongProcessBehindManyShortOnes(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "long" {
			<-ctx.Done()
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))

	p := e.Project("jobs", harness.TestManifest([]string{harness.AgentCommand("long", ""), harness.AgentCommand("short", "")}, nil))
	long := e.Run(p, "long")
	for i := 0; i < 55; i++ {
		e.WaitState(e.Run(p, "short"), "done")
	}
	running := e.CockpitRunning()
	if len(running) != 1 || running[0].ID != float64(long) {
		t.Fatalf("the long process is still running now: %+v", running)
	}
	e.Must("deck.process_stop", map[string]any{"process": long}, nil)
	e.WaitState(long, "stopped")
}

// TestProviderConfirmationSaysWhatAndWhere: a provider tool waiting for the owner names the tool and where it goes.
func TestProviderConfirmationSaysWhatAndWhere(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		c.Approve(ctx, "WebFetch", map[string]any{"url": "https://example.com/page", "prompt": "read it"})
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))

	p := e.Project("jobs", harness.TestManifest([]string{harness.AgentCommand("fetch", "")}, nil))
	e.Run(p, "fetch")
	e.Until("the confirmation", func() bool { return len(e.CockpitItems()) == 1 })
	it := e.CockpitItems()[0]
	if it.Title != "WebFetch" || it.Text != "https://example.com/page" || !strings.Contains(it.Details, "read it") {
		t.Fatalf("the confirmation says the tool and where it goes, with its whole input: %+v", it)
	}
}

// TestNotifyWritesALineToTheCockpitFeed: an agent tells the owner; the line of the bell names the page it opens and
// has the buttons it gave; a screen or a command the project lacks is refused.
func TestNotifyWritesALineToTheCockpitFeed(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	p := e.Project("web", harness.WebNotes)
	e.Must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": `{"title": "alpha"}`}, nil)
	var rec []struct{ ID int64 }
	e.Must("deck.records_list", map[string]any{"project": "web", "collection": "notes"}, &rec)
	done := e.WaitState(e.Run(p, "tell"), "done")
	if strings.Contains(string(done.Result), "error") {
		t.Fatalf("the agent notifies the owner through the gateway: %s", done.Result)
	}
	var feed []struct {
		Project, Text, Screen string
		Record                int64
		Buttons               []struct {
			Label, Command string
			Params         map[string]any
		}
	}
	e.Must("deck.feed_list", map[string]any{"kind": "note"}, &feed)
	if len(feed) != 1 || feed[0].Text != "alpha scored high" || feed[0].Project != "web" || len(feed[0].Buttons) != 1 {
		t.Fatalf("the feed has the line the bell shows: %+v", feed)
	}
	if b := feed[0].Buttons[0]; feed[0].Screen != "note" || feed[0].Record != rec[0].ID ||
		b.Command != "web.touch" || b.Label != "Touch it" || b.Params["note"] != float64(rec[0].ID) {
		t.Fatalf("the line names its page and carries its buttons: %+v", feed[0])
	}
	for _, tc := range []struct {
		in   map[string]any
		want string
	}{
		{map[string]any{"project": "web", "text": "x", "screen": "nowhere"}, `no screen "nowhere"`},
		{map[string]any{"project": "web", "text": "x", "screen": "note"}, "give record"},
		{map[string]any{"project": "web", "text": "x", "actions": []any{map[string]any{"label": "Go", "command": "launch"}}}, `unknown command "launch"`},
		{map[string]any{"project": "web", "text": "x", "actions": `[{"label": "Go", "command": "touch", "params": {"page": 1}}]`}, `no input field "page"`},
		{map[string]any{"project": "web", "text": " "}, "needs text"},
		{map[string]any{"project": "web", "text": "x", "screen": "deck/nowhere"}, `deck has no screen "nowhere"`},
		{map[string]any{"project": "web", "text": "x", "screen": "deck/process"}, "give record"},
	} {
		e.Refused("deck.notify", tc.in, 422, tc.want)
	}
}

// TestANoteOpensItsScreenOfTheProjectOrOfDeck: a note names a screen of its project or of deck, and the cockpit and
// the bell give it a button that opens it.
func TestANoteOpensItsScreenOfTheProjectOrOfDeck(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("web", harness.WebNotes)
	e.Must("deck.notify", map[string]any{"project": "web", "text": "the runs of today", "screen": "deck/schedule"}, nil)
	e.Must("deck.notify", map[string]any{"project": "web", "text": "a new note", "screen": "main"}, nil)
	var feed []struct {
		Text, Screen string
		DeckScreen   bool `json:"deck_screen"`
		Open         map[string]any
		MarkRead     map[string]any `json:"mark_read"`
	}
	e.Must("deck.feed_list", map[string]any{"kind": "note"}, &feed)
	if len(feed) != 2 || feed[1].Screen != "schedule" || !feed[1].DeckScreen || feed[0].Screen != "main" || feed[0].DeckScreen {
		t.Fatalf("a note says whether its screen is of deck: %+v", feed)
	}
	if fmt.Sprint(feed[1].Open) != "map[screen:schedule verb:open]" || fmt.Sprint(feed[0].Open) != "map[project:web screen:main verb:open]" ||
		feed[0].MarkRead["command"] != "deck.feed_read" {
		t.Fatalf("a note gives the buttons the bell draws, opening its screen and marking it read: %+v", feed)
	}
	data, _ := json.Marshal(e.Screen("", "cockpit", nil))
	for _, want := range []string{`{"project":"","record":0,"screen":"schedule","verb":"open"}`, `{"project":"web","record":0,"screen":"main","verb":"open"}`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("the cockpit opens the screen of a note %s: %s", want, data)
		}
	}
}

// TestTheHistoryOfTheCockpitIsADialogOfItsTop: History is a dialog of the cockpit, not one of its blocks, whatever
// lists it shows, so the client draws its button in the top.
func TestTheHistoryOfTheCockpitIsADialogOfItsTop(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.WebNotesAgent()))
	e.Project("web", harness.WebNotes)
	root := func() (children, dialogs []string, history map[string]any) {
		comps := map[string]map[string]any{}
		for _, m := range e.ScreenMessages("", "cockpit", nil) {
			var u struct {
				Components []map[string]any `json:"components"`
			}
			_ = json.Unmarshal(m["updateComponents"], &u)
			for _, c := range u.Components {
				comps[fmt.Sprint(c["id"])] = c
			}
		}
		ids := func(v any) (out []string) {
			list, _ := v.([]any)
			for _, x := range list {
				out = append(out, fmt.Sprint(x))
			}
			return out
		}
		children, dialogs = ids(comps["root"]["children"]), ids(comps["root"]["dialogs"])
		for _, id := range dialogs {
			content, _ := comps[id]["content"].(string)
			if comps[id]["component"] == "Modal" && comps[content]["name"] == "changes" {
				label, _ := comps[fmt.Sprint(comps[id]["trigger"])]["child"].(string)
				history = map[string]any{"id": id, "title": comps[label]["text"]}
			}
		}
		return children, dialogs, history
	}
	children, dialogs, history := root()
	if len(dialogs) != 1 || history == nil || history["title"] != "History" || slices.Contains(children, dialogs[0]) {
		t.Fatalf("the cockpit has History as its one dialog, apart from its blocks: %v %v %v", children, dialogs, history)
	}
	e.WaitState(e.Start("web", "score", map[string]any{"title": "x"}), "done")
	if _, _, again := root(); again == nil || fmt.Sprint(again["id"]) != fmt.Sprint(history["id"]) {
		t.Fatalf("History stays the dialog of the cockpit after a run: %v", again)
	}
}
