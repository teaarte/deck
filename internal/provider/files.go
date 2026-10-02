package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Environ keeps the variables of parent that names lists or that start with one of prefixes.
func Environ(parent, names []string, prefixes ...string) []string {
	var out []string
	for _, kv := range parent {
		name, _, _ := strings.Cut(kv, "=")
		if slices.Contains(names, name) || slices.ContainsFunc(prefixes, func(p string) bool { return strings.HasPrefix(name, p) }) {
			out = append(out, kv)
		}
	}
	return out
}

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

// Under says a path lies inside a folder, by its absolute or its real path.
func Under(path, dir string) bool {
	return slices.ContainsFunc(Paths(dir), func(d string) bool { return strings.HasPrefix(path, d+string(filepath.Separator)) })
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

// Others are name patterns for every name of prefix, a decimal number and suffix but the one with own: other
// lengths of the number, then each other digit at each place.
func Others(prefix, own, suffix string) []string {
	out := []string{prefix + strings.Repeat("?", len(own)+1) + "*" + suffix}
	for n := 1; n < len(own); n++ {
		out = append(out, prefix+strings.Repeat("?", n)+suffix)
	}
	for i := range own {
		for c := '0'; c <= '9'; c++ {
			if byte(c) != own[i] {
				out = append(out, prefix+own[:i]+string(c)+strings.Repeat("?", len(own)-i-1)+suffix)
			}
		}
	}
	return out
}
