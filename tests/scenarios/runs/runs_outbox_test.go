package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/store/db"
	"deck2/tests/harness"
)

// TestAFileGoesOnlyWithItsRowEvenAcrossACrash: a delete that rolls back keeps the file on disk; one deck committed
// before it died takes the file once deck is back.
func TestAFileGoesOnlyWithItsRowEvenAcrossACrash(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	e := harness.New(t, harness.WithHome(dir), harness.WithProvider(&harness.Fake{}))
	e.Project("notes", harness.PlainManifest)
	kept, gone := e.Upload("notes", "kept.txt", "text/plain", "stays"), e.Upload("notes", "gone.txt", "text/plain", "goes")
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
	// A delete deck committed just before it died.
	e.SQL(`DELETE FROM file WHERE id = ?`, gone)
	_ = e.App.Close()
	e = harness.New(t, harness.WithHome(dir), harness.WithProvider(&harness.Fake{}))
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
	fake := &harness.Fake{Script: func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "job" {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}}
	e := harness.New(t, harness.WithHome(dir), harness.WithProvider(fake))
	p := e.Project("chain", harness.TestManifest([]string{harness.AgentCommand("job", ""), harness.AgentCommand("next", harness.ProcessField)},
		[]string{`{"command": "next", "after": "job"}`}))
	id := e.Run(p, "job")
	e.WaitState(id, "running")
	// A process deck closed just before it died, before it heard of it.
	e.SQL(`UPDATE process SET state = 'done', result = '{"n": 1}', finished_at = strftime('%Y-%m-%dT%H:%M:%fZ', 'now'),
		deadline_ms = NULL, silent_since_ms = NULL WHERE id = ? AND state = 'running'`, id)
	close(release)
	_ = e.App.Close()
	for range 2 {
		e = harness.New(t, harness.WithHome(dir), harness.WithProvider(fake))
		e.Resume()
		e.Until("the after trigger of the closed process", func() bool { return e.CountOf("next") >= 1 })
		e.Settle()
		if n := e.CountOf("next"); n != 1 {
			t.Fatalf("the after trigger starts once: %d", n)
		}
		_ = e.App.Close()
	}
}

// TestACronLaunchMovesItsSlotWithIt: the process a cron trigger starts and the move of the trigger to its next slot
// commit together, so a crash between them cannot start the slot twice.
func TestACronLaunchMovesItsSlotWithIt(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok), harness.WithClock())
	p := e.Project("daily", harness.TestManifest([]string{harness.AgentCommand("job", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`}))
	e.Clock.Add(time.Hour)
	ts, err := e.App.Store.Q.ListProjectTriggers(context.Background(), p)
	if err != nil || len(ts) != 1 {
		t.Fatal(ts, err)
	}
	if _, err := e.App.Triggers.Fire(context.Background(), ts[0], map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if got := e.Triggers()[0].NextRunAt; got <= ts[0].NextRunAt.String {
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
	// deck's zone is the one of its process, so the test runs again in a process of its own in Oslo.
	if os.Getenv("TZ") != "Europe/Oslo" {
		self, _ := os.Executable()
		run := exec.Command(self, "-test.run=^TestACronAcrossTheClockShifts$", "-test.count=1")
		run.Env = append(os.Environ(), "TZ=Europe/Oslo", "DECK_FAKES="+harness.Fakes)
		if out, err := run.CombinedOutput(); err != nil {
			t.Fatalf("in Oslo: %v\n%s", err, out)
		}
		return
	}
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
		clock := &harness.Clock{At: c.from}
		e := harness.New(t, harness.WithAgent(harness.Ok), harness.WithClock(clock))
		e.Project("night", harness.TestManifest([]string{harness.AgentCommand("job", "")}, []string{`{"command": "job", "cron": "` + c.cron + `"}`}))
		var got []string
		for i := range 3 {
			next, err := time.Parse(time.RFC3339Nano, e.Triggers()[0].NextRunAt)
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, next.In(oslo).Format("01-02 15:04 MST"))
			clock.Add(next.Sub(clock.Now()))
			e.Tick()
			e.Settle()
			if n := e.CountOf("job"); n != i+1 {
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
	e := harness.New(t, harness.WithAgent(harness.Ok))
	p := e.Project("chain", harness.TestManifest([]string{harness.AgentCommand("job", ""), harness.AgentCommand("next", harness.ProcessField)},
		[]string{`{"command": "next", "after": "job"}`}))
	id := e.Run(p, "job")
	e.WaitState(id, "done")
	e.Until("the first after run", func() bool { return e.CountOf("next") == 1 })
	e.Must("deck.process_rework", map[string]any{"process": id, "remark": "once more"}, nil)
	e.Until("the reworked job done", func() bool { return e.CountOf("next") == 2 })
}

// TestAFileThatWillNotGoIsInHealthUntilItGoes: a stored file deck cannot remove is red in health from its third try,
// and the row goes once the file does.
func TestAFileThatWillNotGoIsInHealthUntilItGoes(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	e := harness.New(t, harness.WithHome(dir), harness.WithProvider(&harness.Fake{}))
	e.Project("notes", harness.PlainManifest)
	id := e.Upload("notes", "stuck.txt", "text/plain", "stays")
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
	// The row of a file gone for good, as the trash leaves it after a day.
	e.SQL(`DELETE FROM file WHERE id = ?`, id)
	for range 3 {
		e.App.Sweep(context.Background(), time.Now())
	}
	e.WaitHealth("outbox", "error")
	_ = os.Remove(filepath.Join(stored, "in"))
	e.App.Sweep(context.Background(), time.Now())
	for _, h := range e.Health() {
		if h.Subject == "outbox" {
			t.Fatalf("the red row goes once the file does: %+v", h)
		}
	}
}
