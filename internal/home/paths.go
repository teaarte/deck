package home

import (
	"errors"
	"fmt"
	"io"
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

// Abs is a path that must be absolute, cleaned.
func Abs(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%q is not an absolute path", path)
	}
	return filepath.Clean(path), nil
}

// Real is path with its links resolved, as the kernel sees it; a missing path resolves through its nearest existing
// folder.
func Real(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	rest := ""
	for d := abs; ; d = filepath.Dir(d) {
		if r, err := filepath.EvalSymlinks(d); err == nil {
			return filepath.Join(r, rest)
		}
		if d == filepath.Dir(d) {
			return abs
		}
		rest = filepath.Join(filepath.Base(d), rest)
	}
}

// Resolve is the real path of the regular file name under root; a link or a way out of root is refused.
func Resolve(root, name string) (string, error) {
	r, err := os.OpenRoot(root)
	if err != nil {
		return "", err
	}
	defer r.Close()
	clean := filepath.Clean("/" + name)[1:]
	fi, err := r.Lstat(clean)
	if err != nil || !fi.Mode().IsRegular() {
		return "", ErrNotRegular
	}
	real, err := filepath.EvalSymlinks(filepath.Join(root, clean))
	if err != nil {
		return "", err
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil || !Inside(base, real) {
		return "", errors.New("outside the folder")
	}
	return real, nil
}

// Inside says path lies inside dir and is not dir itself, as the two are written.
func Inside(dir, path string) bool { return path != dir && Within(dir, path) }

// Overlap says one of two folders is the other or lies inside it, as the two are written.
func Overlap(a, b string) bool { return Within(a, b) || Within(b, a) }

// ErrNotRegular is a name that is no regular file.
var ErrNotRegular = errors.New("not a regular file")

// ErrTooBig is a file longer than its reader takes.
var ErrTooBig = errors.New("larger than deck reads")

// ReadIn reads the regular file name of dir whole, never through a link out of dir nor past limit bytes: a file of a
// folder a box writes is read only so.
func ReadIn(dir, name string, limit int64) ([]byte, error) {
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	return ReadRoot(root, name, limit)
}

// ReadRoot is ReadIn under a root already open.
func ReadRoot(root *os.Root, name string, limit int64) ([]byte, error) {
	f, err := OpenRegular(root, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, fmt.Errorf("%s: %w (%d bytes)", name, ErrTooBig, limit)
	}
	return b, nil
}

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
