package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Paths gives a folder by its absolute and its real path.
func Paths(dir string) []string {
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
	return dir != "" && slices.ContainsFunc(Paths(dir), func(d string) bool { return path != d && Within(d, path) })
}

// Within says path is dir or lies inside it, as the two are written.
func Within(dir, path string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// WriteFile writes a file atomically with mode 600, making its folder.
func WriteFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, werr := f.Write(data)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr == nil {
		werr = os.Rename(f.Name(), path)
	}
	if werr != nil {
		_ = os.Remove(f.Name())
	}
	return werr
}

// WriteTree writes files by their paths under dir with write, refusing a path that leaves dir.
func WriteTree(dir string, files map[string]string, write func(path, text string) error) error {
	for path, text := range files {
		f := filepath.Join(dir, path)
		if !strings.HasPrefix(f, dir+string(filepath.Separator)) {
			return fmt.Errorf("file %q is outside %s", path, dir)
		}
		if err := os.MkdirAll(filepath.Dir(f), 0o700); err != nil {
			return err
		}
		if err := write(f, text); err != nil {
			return err
		}
	}
	return nil
}

// WritePrivate writes a new file with mode 600.
func WritePrivate(path, text string) error { return os.WriteFile(path, []byte(text), 0o600) }

// Quote is s as one word for a POSIX shell.
func Quote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }
