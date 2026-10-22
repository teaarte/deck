// Package paths is the one check that a path is inside a folder, as written or by its real path.
package paths

import (
	"path/filepath"
	"slices"
	"strings"
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
