package harness

import (
	"deck2/internal/events"
	"os"
	"testing"
	"time"
)

// wait is how long a test waits for what deck does: long, since a wait that holds returns at once and a loaded
// machine runs several sandboxed programs one after another slowly.
const wait = 2 * time.Minute

// Within is how long a wait of d lasts: four times as long under the race detector or with DECK_TEST_SLOW set.
func Within(d time.Duration) time.Duration {
	if raceOn || os.Getenv("DECK_TEST_SLOW") != "" {
		return 4 * d
	}
	return d
}

// Until polls until ok holds and fails the test with what when the wait of a test passes first.
func Until(t testing.TB, what string, ok func() bool) {
	t.Helper()
	until(t, wait, what, ok)
}

// UntilWithin is Until with a wait of d, for a deck in docker that takes minutes.
func UntilWithin(t testing.TB, d time.Duration, what string, ok func() bool) {
	t.Helper()
	until(t, d, what, ok)
}

func until(t testing.TB, d time.Duration, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(Within(d))
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Until is the one wait of a test on deck.
func (e *Env) Until(what string, ok func() bool) {
	e.T.Helper()
	d := wait
	if e.wait > 0 {
		d = e.wait
	}
	until(e.T, d, what, ok)
}

// Receive waits for a value on ch.
func Receive[T any](t testing.TB, what string, ch <-chan T) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(Within(wait)):
		t.Fatalf("timed out waiting for %s", what)
	}
	var zero T
	return zero
}

// WaitEvent waits for an event of the events stream that has the topic of want, and its project when want names one.
func WaitEvent(t testing.TB, ch <-chan events.Event, want events.Event) events.Event {
	t.Helper()
	timeout := time.After(Within(wait))
	for {
		select {
		case got := <-ch:
			if got.Topic == want.Topic && (want.Project == "" || got.Project == want.Project) {
				return got
			}
		case <-timeout:
			t.Fatalf("no %+v event", want)
		}
	}
}
