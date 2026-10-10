package scenarios

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/provider"
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
	Seconds     int64
	Long        bool
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

// TestARunThatTakesLongerThanUsualIsMarked: an open run says how long it works and, past twice the usual time of
// its command, that it takes longer than usual.
func TestARunThatTakesLongerThanUsualIsMarked(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	var n atomic.Int32
	clock := &Clock{now: t0}
	e := newEnvWith(t, t.TempDir(), &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		if n.Add(1) > usualRunsForTest {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return provider.Result{Text: "voiced"}
	}}, withClock(clock))
	e.createProject("shorts", jobsModule)
	for range usualRunsForTest {
		e.waitState(e.start("shorts", "voice", nil), "done", "error")
	}
	id := e.start("shorts", "voice", nil)
	e.waitState(id, "running")
	clock.Add(3 * time.Minute)
	var open []cockpitRun
	e.must("deck.processes_list", map[string]any{"open": true}, &open)
	if len(open) != 1 || open[0].ID != id || open[0].Seconds < 180 || !open[0].Long {
		t.Fatalf("the run says how long it works and that it is longer than usual: %+v", open)
	}
}

const usualRunsForTest = 5
