package scenarios

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/internal/store/db"
)

// TestAFileGoesOnlyWithItsRowEvenAcrossACrash: a delete that rolls back keeps the file on disk; one deck committed
// before it died takes the file once deck is back.
func TestAFileGoesOnlyWithItsRowEvenAcrossACrash(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	e := newEnvWith(t, dir, &Fake{})
	e.createProject("notes", plainManifest)
	kept, gone := e.upload("notes", "kept.txt", "text/plain", "stays"), e.upload("notes", "gone.txt", "text/plain", "goes")
	path := func(id int64) string { return filepath.Join(home.Layout(dir).Files(), fmt.Sprint(id)) }
	failed := errors.New("rolled back")
	if err := e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		if err := q.DeleteFile(ctx, kept); err != nil {
			return err
		}
		return failed
	}); !errors.Is(err, failed) {
		t.Fatal(err)
	}
	if err := e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error { return q.DeleteFile(ctx, gone) }); err != nil {
		t.Fatal(err)
	}
	_ = e.App.Close()
	e = newEnvWith(t, dir, &Fake{})
	e.App.Sweep(context.Background(), time.Now())
	if _, err := os.Stat(path(gone)); !os.IsNotExist(err) {
		t.Fatalf("a file whose row deck deleted before it died goes once deck is back: %v", err)
	}
	if _, err := os.Stat(path(kept)); err != nil {
		t.Fatalf("a file whose delete rolled back stays: %v", err)
	}
}

// TestAProcessThatClosedBeforeACrashIsHeardAfterIt: a process deck closed but did not get to hear before it died
// starts its after triggers once deck is back, and only once however often deck comes back.
func TestAProcessThatClosedBeforeACrashIsHeardAfterIt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	release := make(chan struct{})
	fake := &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "job" {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}}
	e := newEnvWith(t, dir, fake)
	e.Fake = fake
	p := e.createProject("chain", testManifest([]string{agentCommand("job", ""), agentCommand("next", processField)},
		[]string{`{"command": "next", "after": "job"}`}))
	id := e.run(p, "job")
	e.waitState(id, "running")
	if err := e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error {
		_, err := q.FinishProcess(ctx, db.FinishProcessParams{State: "done", Result: `{"n": 1}`, Error: "",
			FinishedAt: sql.NullString{String: store.Time(time.Now()), Valid: true}, ID: id})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	close(release)
	_ = e.App.Close()
	for range 2 {
		e = newEnvWith(t, dir, fake)
		e.Fake = fake
		if err := e.App.Runner.ResumeInterrupted(context.Background()); err != nil {
			t.Fatal(err)
		}
		e.waitFor("the after trigger of the closed process", func() bool { return e.countOf("next") >= 1 })
		e.settle()
		if n := e.countOf("next"); n != 1 {
			t.Fatalf("the after trigger starts once: %d", n)
		}
		_ = e.App.Close()
	}
}

// TestACronLaunchMovesItsSlotWithIt: the process a cron trigger starts and the move of the trigger to its next slot
// commit together, so a crash between them cannot start the slot twice.
func TestACronLaunchMovesItsSlotWithIt(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, ok)
	p := e.createProject("daily", testManifest([]string{agentCommand("job", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`}))
	e.Clock.Add(time.Hour)
	ts, err := e.App.Store.Q.ListProjectTriggers(context.Background(), p)
	if err != nil || len(ts) != 1 {
		t.Fatal(ts, err)
	}
	if _, err := e.App.Triggers.Fire(context.Background(), ts[0], map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if got := e.triggers()[0].NextRunAt; got <= ts[0].NextRunAt.String {
		t.Fatalf("the slot moves with the launch: %s, was %s", got, ts[0].NextRunAt.String)
	}
}

// TestACronAcrossTheClockShifts: a daily slot in the hour the clocks skip runs at the first minute after it, and one
// in the hour they repeat runs once; a slot of every hour runs in both passes of the repeated hour.
func TestACronAcrossTheClockShifts(t *testing.T) {
	oslo, err := time.LoadLocation("Europe/Oslo")
	if err != nil {
		t.Skip("no zone data:", err)
	}
	local := time.Local
	time.Local = oslo
	t.Cleanup(func() { time.Local = local })
	for _, c := range []struct {
		cron string
		from time.Time
		want []string
	}{
		{"30 2 * * *", time.Date(2026, 3, 27, 12, 0, 0, 0, oslo), []string{"03-28 02:30 CET", "03-29 03:00 CEST", "03-30 02:30 CEST"}},
		{"30 2 * * *", time.Date(2026, 10, 23, 12, 0, 0, 0, oslo), []string{"10-24 02:30 CEST", "10-25 02:30 CET", "10-26 02:30 CET"}},
		{"30 2 * * *", time.Date(2026, 10, 25, 1, 20, 0, 0, time.UTC), []string{"10-25 02:30 CET", "10-26 02:30 CET", "10-27 02:30 CET"}},
		{"*/15 * * * *", time.Date(2026, 10, 25, 0, 35, 0, 0, time.UTC), []string{"10-25 02:45 CEST", "10-25 02:00 CET", "10-25 02:15 CET"}},
		{"*/15 * * * *", time.Date(2026, 10, 25, 1, 35, 0, 0, time.UTC), []string{"10-25 02:45 CET", "10-25 03:00 CET", "10-25 03:15 CET"}},
		{"*/15 * * * *", time.Date(2026, 3, 29, 0, 35, 0, 0, time.UTC), []string{"03-29 01:45 CET", "03-29 03:00 CEST", "03-29 03:15 CEST"}},
	} {
		clock := &Clock{now: c.from}
		e := newEnv(t, ok, withClock(clock))
		e.Clock = clock
		e.createProject("night", testManifest([]string{agentCommand("job", "")}, []string{`{"command": "job", "cron": "` + c.cron + `"}`}))
		var got []string
		for i := range 3 {
			next, err := time.Parse(time.RFC3339Nano, e.triggers()[0].NextRunAt)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, next.In(oslo).Format("01-02 15:04 MST"))
			clock.Add(next.Sub(clock.Now()))
			e.tick()
			e.settle()
			if n := e.countOf("job"); n != i+1 {
				t.Fatalf("%s from %s: the slot %s starts one run, %d in all", c.cron, c.from, got[i], n)
			}
		}
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Fatalf("the slots of %s across a shift of the clocks: %v, want %v", c.cron, got, c.want)
		}
	}
}

// TestAReworkedProcessStartsItsAfterTriggersAgain: a process sent back for rework and done again starts its after
// triggers on the new result, as a finish heard twice after a crash does not.
func TestAReworkedProcessStartsItsAfterTriggersAgain(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	p := e.createProject("chain", testManifest([]string{agentCommand("job", ""), agentCommand("next", processField)},
		[]string{`{"command": "next", "after": "job"}`}))
	id := e.run(p, "job")
	e.waitState(id, "done")
	e.waitFor("the first after run", func() bool { return e.countOf("next") == 1 })
	e.must("deck.process_rework", map[string]any{"process": id, "remark": "once more"}, nil)
	e.waitFor("the reworked job done", func() bool { return e.countOf("next") == 2 })
}

// TestAFileThatWillNotGoIsInHealthUntilItGoes: a stored file deck cannot remove is red in health from its third try,
// and the row goes once the file does.
func TestAFileThatWillNotGoIsInHealthUntilItGoes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	e := newEnvWith(t, dir, &Fake{})
	e.createProject("notes", plainManifest)
	id := e.upload("notes", "stuck.txt", "text/plain", "stays")
	var stored string
	_ = filepath.WalkDir(home.Layout(dir).Files(), func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			stored = path
		}
		return nil
	})
	// A folder that is not empty in place of the file will not go, for root too, which a closed folder lets through.
	if err := os.Remove(stored); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(stored, "in"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := e.App.Store.Tx(context.Background(), func(ctx context.Context, q *db.Queries) error { return q.DeleteFile(ctx, id) }); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		e.App.Sweep(context.Background(), time.Now())
	}
	e.waitHealth("outbox", "error")
	_ = os.Remove(filepath.Join(stored, "in"))
	e.App.Sweep(context.Background(), time.Now())
	var hs []healthRow
	e.must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if h.Subject == "outbox" {
			t.Fatalf("the red row goes once the file does: %+v", h)
		}
	}
}
