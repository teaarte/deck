// Package sandbox runs every foreign program in a box: it sees only what its box lists and reaches only through its
// door. bubblewrap on Linux; sandbox-exec on macOS, where a node and deck without Docker run.
package sandbox

import (
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"deck2/internal/home"
	"deck2/internal/nonce"
)

// Sandbox starts boxes for one deck: their doors live in its folder, their marks name it.
type Sandbox struct {
	home home.Layout
	// Program is the program of the box on Linux; bwrap found on deck's PATH when empty.
	Program string
	// Also are paths every box reads and writes besides its own: the fakes of the tests.
	Also struct{ Read, Write []string }

	once  sync.Once
	err   error
	mu    sync.Mutex
	sides map[string]http.Handler
}

// New is the sandbox of the deck whose folder is h.
func New(h home.Layout) *Sandbox { return &Sandbox{home: h, sides: map[string]http.Handler{}} }

// Check reports whether a box can be built on this machine, with what to fix when not; it is asked once.
func (s *Sandbox) Check() error {
	s.once.Do(func() { s.err = s.check() })
	return s.err
}

// Side gives deck's side of a door a token a box names in Deck; end takes it back.
func (s *Sandbox) Side(h http.Handler) (tok string, end func()) {
	tok = nonce.Hex(16)
	s.mu.Lock()
	s.sides[tok] = h
	s.mu.Unlock()
	return tok, func() {
		s.mu.Lock()
		delete(s.sides, tok)
		s.mu.Unlock()
	}
}

func (s *Sandbox) side(tok string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		h := s.sides[tok]
		s.mu.Unlock()
		if h == nil {
			http.Error(w, "deck: this program has no way to deck", http.StatusNotFound)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// marked begins the mark of every box of this deck.
func (s *Sandbox) marked() string { return "deck-box " + string(s.home) + " " }

// Mark is the mark of a box of kind for id: every box carries it in its command line, so a deck that starts again
// finds what one before it left running.
func (s *Sandbox) Mark(kind, id string) string {
	return s.marked() + kind + "=" + strings.ReplaceAll(id, " ", "_") + ";"
}

// Reap kills the boxes a deck before this one left running in its folder, found by their marks.
func (s *Sandbox) Reap() { reapMarked(s.marked()) }

// End kills what is left of the process group of a command.
func End(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = Kill(cmd, syscall.SIGKILL)
	}
}

// Kill sends sig to the process group of a started command.
func Kill(cmd *exec.Cmd, sig syscall.Signal) error { return syscall.Kill(-cmd.Process.Pid, sig) }

func group(cmd *exec.Cmd) *exec.Cmd {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return Kill(cmd, syscall.SIGKILL) }
	cmd.WaitDelay = 5 * time.Second
	return cmd
}

// real is the path the kernel sees, which the rules of a sandbox match.
var real = home.Real
