package sandbox

import "strings"

// OpenHint is where the owner opens a folder of their home to the programs of a project.
const OpenHint = "Of the owner's home a script or MCP server reads only the runtimes on PATH and the folders of its project; the owner adds a folder in Settings → Projects."

// Refused says the end of the stderr of a program reads as the sandbox refusing it a path, so OpenHint belongs to it.
func Refused(stderr string) bool {
	return strings.Contains(stderr, "Operation not permitted") || strings.Contains(stderr, "Permission denied") ||
		strings.Contains(stderr, "No such file or directory")
}
