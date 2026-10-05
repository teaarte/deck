package acp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"deck2/internal/provider"
)

var envNames = []string{"PATH", "USER", "LOGNAME", "SHELL", "LANG", "TZ"}

// environ keeps the allowlisted variables of deck; HOME, TMPDIR and XDG_* would lead the agent to the owner's
// settings, keys never pass.
func environ(parent []string) []string { return provider.Environ(parent, envNames, "LC_") }

// writeSkills puts the skills of the turn where ACP agents look for them, in a fresh folder of the turn: deck writes
// there itself, so nothing the agent left, such as a symlink, may lead the writes elsewhere.
func writeSkills(cwd string, files map[string]string) error {
	agents := filepath.Join(cwd, ".agents")
	if fi, err := os.Lstat(agents); err == nil && !fi.IsDir() {
		if err := os.Remove(agents); err != nil {
			return err
		}
	}
	dir := filepath.Join(agents, "skills")
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := provider.WriteTree(dir, files, writeNew); err != nil {
		return fmt.Errorf("acp: %w", err)
	}
	return nil
}

// writeNew replaces a file in a folder the agent writes to without following what the agent put there.
func writeNew(path, text string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return err
	}
	_, werr := io.WriteString(f, text)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	return werr
}
