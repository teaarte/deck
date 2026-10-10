package acp

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"deck2/internal/provider"
)

// writeSkills puts the skills of the turn in a fresh folder at rel in the home of the session, never in the folder the
// agent works in; deck writes there itself, so nothing the agent left, such as a symlink, may lead the writes elsewhere.
func writeSkills(home, rel string, files map[string]string) error {
	dir := home
	for _, part := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
		dir = filepath.Join(dir, part)
		if fi, err := os.Lstat(dir); err == nil && !fi.IsDir() {
			if err := os.Remove(dir); err != nil {
				return err
			}
		}
	}
	dir = filepath.Join(home, rel)
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
