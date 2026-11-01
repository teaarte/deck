package script

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"time"

	"deck2/internal/release"
	"deck2/internal/sandbox"
)

// Runtime is the python and node every script of deck runs on: the first python3 of 3.10 or newer, brew's before the
// system's, and the node deck finds.
type Runtime struct {
	Python, PythonVersion string
	Node, NodeVersion     string
	// Old says no python3 of 3.10 or newer is on the machine, so Python is an older one.
	Old bool
}

var picked = sync.OnceValue(pick)

// Runtimes is the runtime of scripts, picked once.
func Runtimes() Runtime { return picked() }

// Health is the health of the runtime of scripts: its status and the python and node they see.
func (r Runtime) Health() (status, text string) {
	py := "no python3"
	if r.Python != "" {
		py = fmt.Sprintf("python %s at %s", r.PythonVersion, r.Python)
	}
	node := "no node"
	if r.Node != "" {
		node = fmt.Sprintf("node %s at %s", r.NodeVersion, r.Node)
	}
	text = fmt.Sprintf("Scripts run on %s; %s.", py, node)
	switch {
	case r.Python == "":
		return "warning", fmt.Sprintf("%s Python scripts cannot run: the owner installs python 3.10 or newer (%s).", text, installPython())
	case r.Old:
		return "warning", fmt.Sprintf("%s It is older than 3.10: the owner installs a newer one (%s).", text, installPython())
	}
	return "ok", text
}

// pathFirst is the environment with the folder of the python of scripts first on PATH.
func pathFirst(env []string) []string {
	py := Runtimes().Python
	if py == "" {
		return env
	}
	dir := filepath.Dir(py)
	for i, kv := range env {
		if rest, ok := strings.CutPrefix(kv, "PATH="); ok {
			env[i] = "PATH=" + strings.Join(append([]string{dir}, slices.DeleteFunc(filepath.SplitList(rest), func(d string) bool { return d == dir })...), string(os.PathListSeparator))
			return env
		}
	}
	return append(env, "PATH="+dir)
}

// installPython is the command that installs python on this system.
func installPython() string {
	if runtime.GOOS == "darwin" {
		return "brew install python"
	}
	if sandbox.WSL() {
		return "wsl -d deck -u root -- apt-get install -y python3"
	}
	return "sudo apt-get install python3"
}

func pick() Runtime {
	var r Runtime
	dirs := []string{"/opt/homebrew/bin", "/usr/local/bin"}
	for _, d := range filepath.SplitList(sandbox.Path()) {
		if d != "" && !slices.Contains(dirs, d) {
			dirs = append(dirs, d)
		}
	}
	for _, d := range dirs {
		p := filepath.Join(d, "python3")
		if fi, err := os.Stat(p); err != nil || fi.IsDir() || fi.Mode()&0o111 == 0 {
			continue
		}
		v := version(p, "-c", "import sys; print('%d.%d.%d' % sys.version_info[:3])")
		if v == "" {
			continue
		}
		if newEnough(v) {
			r.Python, r.PythonVersion, r.Old = p, v, false
			break
		}
		if r.Python == "" {
			r.Python, r.PythonVersion, r.Old = p, v, true
		}
	}
	if p, err := sandbox.Find("node"); err == nil {
		r.Node, r.NodeVersion = p, release.Semver(version(p, "--version"))
	}
	return r
}

func version(prog string, args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, prog, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func newEnough(v string) bool {
	n := release.Numbers(v)
	return n != nil && slices.Compare(n, []int{3, 10, 0}) >= 0
}

// python is the python3 a script runs on.
func python() (string, error) {
	if p := Runtimes().Python; p != "" {
		return p, nil
	}
	return "", fmt.Errorf("no python3 on this machine")
}
