package sandbox

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

// OpenHint is where the owner opens a folder of their home to the programs of a project.
const OpenHint = "Of the owner's home a script or MCP server reads only the runtimes on PATH and the folders of its module; the owner adds a folder in the settings of the module, Access."

// Refused says the end of the stderr of a program reads as the sandbox refusing it a path, so OpenHint belongs to it:
// macOS refuses; bubblewrap leaves the owner's home out, so a file there that exists reads as missing.
func Refused(stderr string) bool {
	if strings.Contains(stderr, "Operation not permitted") || strings.Contains(stderr, "Permission denied") {
		return true
	}
	return runtime.GOOS == "linux" && strings.Contains(stderr, "No such file") && hidden(stderr)
}

// pathIn are the absolute paths a text names.
var pathIn = regexp.MustCompile(`/[^\s'"]+`)

// hidden says a path the text names is in the owner's home, outside deck's own, and there on the machine.
func hidden(text string) bool {
	home, err := os.UserHomeDir()
	if err != nil {
		return false
	}
	deck := os.Getenv("DECK_HOME")
	if deck == "" {
		deck = filepath.Join(home, ".deck2")
	}
	for _, p := range pathIn.FindAllString(text, -1) {
		p = strings.TrimRight(p, ".,:;)")
		if !strings.HasPrefix(p, home+"/") || p == deck || strings.HasPrefix(p, deck+"/") {
			continue
		}
		if _, err := os.Stat(p); err == nil {
			return true
		}
	}
	return false
}
