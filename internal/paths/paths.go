// Package paths is the one check that a path is inside a folder, as written or by its real path.
package paths

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// Names gives a folder by its absolute and its real path.
func Names(dir string) []string {
	var out []string
	if abs, err := filepath.Abs(dir); err == nil {
		out = append(out, abs)
	}
	if real, err := filepath.EvalSymlinks(dir); err == nil && !slices.Contains(out, real) {
		out = append(out, real)
	}
	return out
}

// Under says a path lies inside a folder, by its absolute or its real path; an empty folder holds nothing.
func Under(path, dir string) bool {
	return dir != "" && slices.ContainsFunc(Names(dir), func(d string) bool { return path != d && Within(d, path) })
}

// Within says path is dir or lies inside it, as the two are written.
func Within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// Inside says path lies inside dir and is not dir itself, as the two are written.
func Inside(dir, path string) bool { return path != dir && Within(dir, path) }

// Overlap says one of two folders is the other or lies inside it, as the two are written.
func Overlap(a, b string) bool { return Within(a, b) || Within(b, a) }

// ErrNotRegular is a name that is no regular file.
var ErrNotRegular = errors.New("not a regular file")

// OpenRegular opens a regular file of root for reading; the root keeps every link inside it, and a pipe opens without
// waiting for a writer.
func OpenRegular(root *os.Root, name string) (*os.File, error) {
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, ErrNotRegular
	}
	return f, nil
}
