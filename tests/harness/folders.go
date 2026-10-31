package harness

import (
	"os"
	"path/filepath"
	"testing"
)

// RepoFolder is a folder that looks like a git repo to deck, by its real path.
func RepoFolder(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "My Repo")
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o700); err != nil {
		t.Fatal(err)
	}
	real, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return real
}
