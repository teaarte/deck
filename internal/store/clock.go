package store

import "time"

// Now is deck's one clock: every time deck writes or checks is read from it.
func (s *Store) Now() time.Time { return s.now() }

// Wall is the clock of a deck given none, of what runs without a base, such as deck pair, and of the world outside
// deck, such as the expiry of a token another service gave.
func Wall() time.Time { return time.Now() }

// Watch is a stopwatch of real time: a wait bounded in seconds or a meter of a call, which a test clock that stands
// still must not stop.
type Watch struct{ start time.Time }

// Start starts a stopwatch.
func Start() Watch { return Watch{time.Now()} }

// Elapsed is the real time since the stopwatch started.
func (w Watch) Elapsed() time.Duration { return time.Since(w.start) }
