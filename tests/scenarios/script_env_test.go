package scenarios

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStopDoesNotWaitForAPackageInstall(t *testing.T) {
	bin := t.TempDir()
	_ = os.WriteFile(filepath.Join(bin, "uv"), []byte("#!/bin/sh\ntouch \"$HOME/started\"\nsleep 30\n"), 0o755)
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	e := newEnv(t, nil)
	pid := e.createProject("slowpkg", scriptManifest("use", "", `["anything"]`, ""))
	e.saveScript("slowpkg", "scripts/main.py", "print('null')\n")
	marker := filepath.Join(e.App.Cfg.Home, "projects", fmt.Sprint(pid), "env", ".home", "started")
	if _, err := os.Stat(marker); err != nil {
		e.waitFor("the install to start", func() bool { _, err := os.Stat(marker); return err == nil })
	}
	id := e.start("slowpkg", "use", nil)
	time.Sleep(200 * time.Millisecond)
	start := time.Now()
	e.must("deck.process_stop", map[string]any{"process": id}, nil)
	if p := e.waitScript(id, "stopped", "done", "error"); p.State != "stopped" || time.Since(start) > 5*time.Second {
		t.Fatalf("a script waiting for an install stops at once: %+v after %s", p, time.Since(start))
	}
}

func TestChangedPackagesDoNotBreakARunningScript(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv is not installed")
	}
	e := newEnv(t, nil)
	one, two := wheel(t), namedWheel(t, "deckother")
	e.createProject("swap", scriptManifest("use", "", fmt.Sprintf("[%q]", one), ""))
	e.saveScript("swap", "scripts/main.py", "import json, sys, time\nprint('started', file=sys.stderr, flush=True)\ntime.sleep(3)\nimport deckfixture\nprint(json.dumps({'value': deckfixture.VALUE}))\n")
	e.waitHealth("command:swap.use", "ok")
	id := e.start("swap", "use", nil)
	e.waitFor("the script to start", func() bool { return len(e.log(id)) > 0 })
	m := scriptManifest("use", "", fmt.Sprintf("[%q, %q]", one, two), "")
	e.must("deck.manifest_save", map[string]any{"project": "swap", "manifest": m}, nil)
	p := e.waitScript(id, "done", "error")
	if p.State != "done" || !strings.Contains(string(p.Result), "42") {
		t.Fatalf("a script keeps the environment it started with: %+v", p)
	}
}
