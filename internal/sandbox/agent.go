package sandbox

import (
	"path/filepath"

	"deck2/internal/home"
)

// Agent gives what an agent may not read or write in deck's folder, by both its paths since macOS links /tmp and
// /var: the state only deck reads.
func Agent(deck home.Layout) []string {
	out := []string{deck.State() + "/**"}
	if real, err := filepath.EvalSymlinks(string(deck)); err == nil && real != string(deck) {
		out = append(out, home.Layout(real).State()+"/**")
	}
	return out
}
