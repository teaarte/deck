package sandbox

import (
	"os"
	"path/filepath"

	"deck2/internal/home"
)

// Agent gives what an agent may not read or write in deck's folder, by both its paths since macOS links /tmp and
// /var: all of it but work, such as the state only deck reads and a log with the owner's link, as it is now.
func Agent(deck home.Layout) []string {
	var out []string
	for _, p := range Shut(deck) {
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			p += "/**"
		}
		out = append(out, p)
	}
	return out
}

// Shut are the files and folders of deck's folder an agent may not touch, by both its paths: all but work; the folder
// itself stays open, so the paths above a run resolve.
func Shut(deck home.Layout) []string {
	roots := []string{string(deck)}
	if real, err := filepath.EvalSymlinks(string(deck)); err == nil && real != string(deck) {
		roots = append(roots, real)
	}
	var out []string
	for _, r := range roots {
		out = append(out, home.Layout(r).State())
		top, _ := os.ReadDir(r)
		for _, e := range top {
			if e.Name() != "work" && e.Name() != "state" {
				out = append(out, filepath.Join(r, e.Name()))
			}
		}
	}
	return out
}
