package process

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/internal/store/db"
	"deck2/internal/untrusted"
)

func (r *Runner) turnText(ctx context.Context, processID int64, text string) {
	_ = r.hear(ctx, processID, func(h Hook, p db.Process) { h.Turn(ctx, p, text) })
}

// logLine is a line of the process log: an event in the words of ACP session/update.
type logLine struct {
	At     string `json:"at"`
	Kind   string `json:"kind"`
	ID     string `json:"id,omitempty"`
	Title  string `json:"title,omitempty"`
	Status string `json:"status,omitempty"`
	Text   string `json:"text,omitempty"`
}

// event appends a top-level provider event to the process log and moves its last output time, at most once a second;
// a thought or message without text, such as a thinking block a model keeps to itself, is no line.
func (r *Runner) event(ctx context.Context, processID int64, stream *os.File, e provider.Event) {
	r.sign(processID, e)
	if (e.Kind == provider.EventThought || e.Kind == provider.EventMessage) && strings.TrimSpace(e.Text) == "" {
		return
	}
	now := r.now()
	b, _ := json.Marshal(logLine{At: store.Time(now), Kind: e.Kind, ID: e.ID, Title: e.Title, Status: e.Status, Text: untrusted.Cut(e.Text, 4000)})
	_, _ = stream.Write(append(b, '\n'))
	r.logged(ctx, processID)
	r.mu.Lock()
	last, seen := r.marked[processID]
	due := !seen || now.Sub(last) >= time.Second
	if due {
		r.marked[processID] = now
	}
	r.mu.Unlock()
	if !due {
		return
	}
	_ = r.st.Tx(ctx, func(ctx context.Context, q *db.Queries) error {
		return q.SetProcessOutput(ctx, db.SetProcessOutputParams{OutputMs: now.UnixMilli(), ID: processID})
	})
}

// runDir is the folder of an open process: its work, results and log.
func (r *Runner) runDir(processID int64) string {
	return r.cfg.Home.Run(processID)
}

// clearRun removes what a script or tool left in its run folder but its log.
func (r *Runner) clearRun(dir string) {
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.Name() != "stream" {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

// tailCache holds the lines Tail read of logs by what it was asked, each with the size and time of the log then.
type tailCache struct {
	mu    sync.Mutex
	lines map[tailKey]tailLines
}

type tailKey struct {
	process int64
	n       int
	kinds   string
}

type tailLines struct {
	size  int64
	mod   time.Time
	lines []json.RawMessage
}

// tailsKept is how many reads of logs the cache holds before it starts again; a read of more lines than tailCached
// is not kept.
const (
	tailsKept  = 512
	tailCached = 1000
)

// Tail is the last n lines of the log of a process, only those of the kinds given when any are; a process
// without a log has none. A log that did not change since is not read again.
func (r *Runner) Tail(processID int64, n int, kinds ...string) ([]json.RawMessage, error) {
	out := []json.RawMessage{}
	f, err := os.Open(r.LogPath(processID))
	if errors.Is(err, os.ErrNotExist) {
		return out, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	key := tailKey{processID, n, strings.Join(kinds, ",")}
	c := &r.tails
	c.mu.Lock()
	if got, ok := c.lines[key]; ok && n <= tailCached && got.size == fi.Size() && got.mod.Equal(fi.ModTime()) {
		c.mu.Unlock()
		return got.lines, nil
	}
	c.mu.Unlock()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 16*1024*1024)
	for sc.Scan() {
		var line struct{ Kind string }
		if json.Unmarshal(sc.Bytes(), &line) != nil || len(kinds) > 0 && !slices.Contains(kinds, line.Kind) {
			continue
		}
		if out = append(out, append(json.RawMessage{}, sc.Bytes()...)); len(out) > n {
			out = out[1:]
		}
	}
	if err := sc.Err(); err != nil || n > tailCached {
		return out, err
	}
	c.mu.Lock()
	if c.lines == nil || len(c.lines) >= tailsKept {
		c.lines = map[tailKey]tailLines{}
	}
	c.lines[key] = tailLines{fi.Size(), fi.ModTime(), out}
	c.mu.Unlock()
	return out, nil
}

// Said is the last message the agent of a process said, on one line; empty when it said none.
func (r *Runner) Said(processID int64) string {
	lines, err := r.Tail(processID, 1, provider.EventMessage)
	if err != nil || len(lines) == 0 {
		return ""
	}
	var l logLine
	_ = json.Unmarshal(lines[0], &l)
	return untrusted.Flat(l.Text, 300)
}

// LogPath is the file with the log of a process.
func (r *Runner) LogPath(processID int64) string {
	return r.cfg.Home.Stream(processID)
}
