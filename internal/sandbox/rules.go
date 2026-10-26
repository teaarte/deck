package sandbox

import (
	"os"
	"runtime"
	"strings"
)

// OpenHint is where the owner opens a folder of their home to the programs of a project.
const OpenHint = "Of the owner's home a script or MCP server reads only the runtimes on PATH and the folders of its module; the owner adds a folder in the settings of the module, Access."

// Refused says the end of the stderr of a program reads as the sandbox refusing it a path, so OpenHint belongs to it:
// macOS refuses, bubblewrap leaves the owner's home out, so a path there is missing; another missing file is its own.
func Refused(stderr string) bool {
	if strings.Contains(stderr, "Operation not permitted") || strings.Contains(stderr, "Permission denied") {
		return true
	}
	home, err := os.UserHomeDir()
	return runtime.GOOS == "linux" && err == nil && strings.Contains(stderr, "No such file") && strings.Contains(stderr, home+"/")
}
