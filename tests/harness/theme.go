package harness

import (
	"os"
	"path/filepath"
)

// Example saves a theme of the master's skill of themes as the owner's, as Deki does when asked for that look.
func (e *Env) Example(name string) {
	e.T.Helper()
	raw, err := os.ReadFile(filepath.Join(Root, "master", "skills", "themes", "examples", name+".json"))
	if err != nil {
		e.T.Fatal(err)
	}
	e.Must("deck.theme_save", map[string]any{"name": name, "text": string(raw)}, nil)
}
