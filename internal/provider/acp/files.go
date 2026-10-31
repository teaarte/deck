package acp

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"deck2/internal/home"
)

// writeSkills puts the skills of the turn in a fresh folder at rel in the home of the session, never in the folder the
// agent works in; deck writes there itself, so nothing the agent left, such as a symlink, may lead the writes elsewhere.
func writeSkills(own, rel string, files map[string]string) error {
	dir := own
	for _, part := range strings.Split(filepath.Dir(rel), string(filepath.Separator)) {
		dir = filepath.Join(dir, part)
		if fi, err := os.Lstat(dir); err == nil && !fi.IsDir() {
			if err := os.Remove(dir); err != nil {
				return err
			}
		}
	}
	dir = filepath.Join(own, rel)
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	if err := home.WriteTree(dir, files); err != nil {
		return fmt.Errorf("acp: %w", err)
	}
	return nil
}
