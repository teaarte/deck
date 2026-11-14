package script

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"deck2/internal/fault"
	"deck2/internal/release"
	"deck2/internal/sandbox"
)

// Python is the python3 every uv of a script runs on: the first on deck's PATH, behind its launcher.
func Python() (string, error) {
	p, err := sandbox.Find("python3")
	if err != nil {
		return "", err
	}
	return realPython(p), nil
}

// Health is the health of the programs scripts run on: the python and uv of Python scripts, and node.
func Health() (status, text string) {
	var missing []string
	py, uv, node := "no python3", "no uv", "no node"
	if p, err := Python(); err == nil {
		py = fmt.Sprintf("python %s at %s", versionOf(p), p)
	} else {
		missing = append(missing, "python3")
	}
	if p, err := sandbox.Find("uv"); err == nil {
		uv = fmt.Sprintf("uv %s at %s", versionOf(p), p)
	} else {
		missing = append(missing, "uv")
	}
	if p, err := sandbox.Find("node"); err == nil {
		node = fmt.Sprintf("node %s at %s", versionOf(p), p)
	}
	text = fmt.Sprintf("Scripts run on %s through %s; %s.", py, uv, node)
	if len(missing) > 0 {
		return "warning", fmt.Sprintf("%s Python scripts cannot run: the owner installs %s.", text, strings.Join(missing, ", "))
	}
	return "ok", text
}

func versionOf(prog string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, _ := exec.CommandContext(ctx, prog, "--version").Output()
	return release.Semver(string(out))
}

// realPython is the interpreter behind a launcher such as macOS /usr/bin/python3, which in a sandbox runs xcrun
// and writes cache errors to stderr on every start; the launcher itself when it cannot tell.
func realPython(path string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "-c", "import sys; print(sys.executable)").Output()
	real := strings.TrimSpace(string(out))
	if fi, statErr := os.Stat(real); err != nil || statErr != nil || !filepath.IsAbs(real) || fi.IsDir() {
		return path
	}
	return real
}

var otherPython = regexp.MustCompile("resolved to Python ([0-9][^ ,]*), which is incompatible with the script's Python requirement: `([^`]+)`")

// otherPythonOf is the failure of a script whose requires-python the one python of deck does not meet, by what uv
// wrote on its stderr; nil when uv did not say so.
func otherPythonOf(name, stderr string, image bool) *fault.F {
	m := otherPython.FindStringSubmatch(stderr)
	if m == nil {
		return nil
	}
	how := "the owner installs a newer python3"
	if image {
		how = "a newer one comes only with a newer image of deck"
	}
	return fault.With(fault.ProgramMissing, fmt.Sprintf("script %s asks for Python %s; deck runs Python %s: %s", name, m[2], m[1], how), stderr)
}
