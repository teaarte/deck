package provider

import (
	"strings"

	"deck2"
	"deck2/internal/release"
	"deck2/internal/sandbox"
)

// CIVersion is the version of an agent CLI CI tests deck with, by the ARG of deck's Dockerfile that names it.
func CIVersion(arg string) string {
	for line := range strings.Lines(deck2.Dockerfile) {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "ARG "+arg+"="); ok {
			return v
		}
	}
	return ""
}

// Untested says a CLI of version runs on another version than ci, the one CI tests deck with; false when either is
// unknown.
func Untested(version, ci string) bool {
	return ci != "" && release.Semver(version) != "" && release.Semver(version) != release.Semver(ci)
}

// Install is the command that installs a program as the owner runs it here: in WSL, from Windows into the
// distribution of deck.
func Install(cmd string) string {
	if sandbox.WSL() {
		return `wsl -d deck -- bash -lc "` + cmd + `"`
	}
	return cmd
}
