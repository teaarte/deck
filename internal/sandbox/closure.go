package sandbox

import (
	"os"
	"path/filepath"
	"slices"
	"strings"

	"deck2/internal/home"
	"deck2/internal/paths"
)

// Closure is what deck keeps from a confined program: Hide closes deck's folder and the owner's home, Read opens
// the runtimes on PATH inside them again, Private keeps the credential stores closed inside Read.
type Closure struct{ Hide, Read, Private []string }

// Close is the closure of a program with path as its PATH under deck's folder deck; a home it cannot tell closes the
// folders of all homes.
func Close(layout home.Layout, path string) Closure {
	deck := string(layout)
	home, err := os.UserHomeDir()
	if err != nil || real(home) == "/" {
		return Closure{Hide: []string{deck, "/Users", "/home"}}
	}
	read := slices.DeleteFunc(Runtimes(home, path), func(r string) bool {
		return paths.Overlap(real(deck), r)
	})
	return Closure{Hide: []string{deck, home}, Read: read, Private: Private()}
}

// common are the folders of a home that hold the data of many programs; none of them is an installation.
var common = []string{".local", ".config", ".cache", "Library"}

// Runtimes are the folders of path inside home by both names, with the installation above each that holds lib,
// libexec or versions (node, python, a JDK, pyenv shims); a linked program opens alone or with its installation.
func Runtimes(home, path string) []string {
	home = real(home)
	var out []string
	add := func(p string) {
		if !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	var folder func(dir string, links int)
	folder = func(dir string, links int) {
		r := real(dir)
		if !paths.Inside(home, r) || slices.Contains(out, r) {
			return
		}
		if c := filepath.Clean(dir); c != r {
			add(c)
		}
		add(r)
		if up := filepath.Dir(r); up != home && !slices.Contains(common, strings.TrimPrefix(up, home+"/")) && installation(up) {
			add(up)
		}
		if links == 0 {
			return
		}
		entries, _ := os.ReadDir(r)
		for _, e := range entries {
			to, err := filepath.EvalSymlinks(filepath.Join(r, e.Name()))
			if e.Type()&os.ModeSymlink == 0 || err != nil || !paths.Inside(home, to) {
				continue
			}
			if up := filepath.Dir(filepath.Dir(to)); filepath.Base(filepath.Dir(to)) == "bin" || installation(up) {
				folder(filepath.Dir(to), links-1)
			} else {
				add(to)
			}
		}
	}
	for _, d := range filepath.SplitList(path) {
		if filepath.IsAbs(d) {
			folder(d, 2)
		}
	}
	return out
}

// installation is a folder that holds what the programs in its bin load.
func installation(dir string) bool {
	for _, n := range []string{"lib", "libexec", "versions"} {
		if fi, err := os.Stat(filepath.Join(dir, n)); err == nil && fi.IsDir() {
			return true
		}
	}
	return false
}

// Private are the owner's credential stores and the data of their other apps.
func Private() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(private))
	for _, p := range private {
		out = append(out, filepath.Join(home, p))
	}
	return out
}

var private = []string{".ssh", ".aws", ".azure", ".gnupg", ".kube", ".docker", ".netrc", ".git-credentials", ".npmrc",
	".pypirc", ".cargo/credentials", ".cargo/credentials.toml", ".terraform.d", ".password-store", ".mozilla",
	".config/gcloud", ".config/gh", ".config/op", ".config/google-chrome", ".config/chromium", ".local/share/keyrings",
	".claude", ".claude.json", ".codex", ".dsh", ".config/opencode", ".local/share/opencode", ".config/goose", ".local/share/goose", ".local/state/goose", "Library/Keychains", "Library/Cookies", "Library/Application Support",
	"Library/Containers", "Library/Group Containers", "Library/Mail", "Library/Messages", "Library/Safari"}
