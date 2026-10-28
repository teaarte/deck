package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Local is a deck binary on this machine with its home, as on macOS.
type Local struct {
	Bin  string
	Home string
}

func (l Local) Command(addr string) *exec.Cmd {
	cmd := exec.Command(l.Bin, "-addr", addr)
	cmd.Env = append(os.Environ(), "DECK_HOME="+l.Home, "DECK_LAUNCHER=1")
	return cmd
}

func (l Local) Token(context.Context) (string, error) {
	b, err := os.ReadFile(filepath.Join(l.Home, "state", "token"))
	return string(b), err
}

func (l Local) Version(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, l.Bin, "-version").Output()
	return firstWord(out), err
}

// firstWord is the version deck -version prints first.
func firstWord(out []byte) string {
	f := strings.Fields(string(out))
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

func (l Local) Stop(cmd *exec.Cmd) error { return cmd.Process.Signal(syscall.SIGTERM) }

func (l Local) Kill(cmd *exec.Cmd) { _ = cmd.Process.Kill() }

// localDeck is the deck beside the launcher in its app, else the one on PATH, with the home deck takes itself.
func localDeck() (Local, error) {
	home := os.Getenv("DECK_HOME")
	if home == "" {
		h, err := os.UserHomeDir()
		if err != nil {
			return Local{}, err
		}
		home = filepath.Join(h, ".deck2")
	}
	if exe, err := os.Executable(); err == nil {
		self, _ := os.Stat(exe)
		for _, p := range []string{filepath.Join(filepath.Dir(exe), "..", "Resources", "deck"), filepath.Join(filepath.Dir(exe), "deck")} {
			// In deck.app the launcher itself is named deck: it never starts itself.
			if st, err := os.Stat(p); err == nil && !st.IsDir() && (self == nil || !os.SameFile(st, self)) {
				return Local{Bin: p, Home: home}, nil
			}
		}
	}
	bin, err := exec.LookPath("deck")
	return Local{Bin: bin, Home: home}, err
}
