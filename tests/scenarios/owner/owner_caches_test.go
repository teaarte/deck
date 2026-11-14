package owner

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	deckhome "deck2/internal/home"
	"deck2/tests/harness"
)

// put writes the files of a module's folder by their paths in it, each changed at.
func put(t *testing.T, dir string, at time.Time, files ...string) {
	t.Helper()
	for _, f := range files {
		p := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(strings.Repeat("x", 1<<20)), 0o600); err != nil {
			t.Fatal(err)
		}
		for d := p; d != dir; d = filepath.Dir(d) {
			if err := os.Chtimes(d, at, at); err != nil {
				t.Fatal(err)
			}
		}
	}
}

// TestTheDailyCleanupRemovesCachesUntouchedForAMonth: a cache of a module or its MCP server untouched for 30 days
// goes, its data and uv's, old but of a script run now, stay; the disk row names every module's folder.
func TestTheDailyCleanupRemovesCachesUntouchedForAMonth(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	now := e.Clock.Now()
	old, recent := now.Add(-31*24*time.Hour), now.Add(-24*time.Hour)
	h := deckhome.Layout(e.App.Cfg.Home)
	id := e.Project("big", strings.Replace(harness.ScriptManifest("use", "", ""), `"runtime": "python"`, `"runtime": "bash"`, 1))
	e.SaveScript("big", "scripts/main.py", "echo '{}'\n")
	if p := e.WaitState(e.Start("big", "use", nil), "done", "error"); p.State != "done" {
		t.Fatalf("the script runs: %+v", p)
	}
	if fi, err := os.Stat(filepath.Join(h.Scripts(id), "scripts", "main.py")); err != nil || !fi.ModTime().Equal(now) {
		t.Fatalf("a run stamps the file of its script by deck's clock: %v %v", fi, err)
	}
	e.Project("tiny", harness.ScriptManifest("use", "", ""))
	dir := h.Project(id)
	put(t, dir, old, "data/out/a.bin", "data/.cache/huggingface/model.bin", "mcp/files/state.json", "mcp/files/.npm/_npx/old/pkg.js",
		"data/.cache/uv/environments-v2/main-0000000000000001/lib.py", "data/.cache/uv/environments-v2/stale-0000000000000003/lib.py")
	put(t, dir, recent, "data/.cache/pip/http/fresh")
	put(t, dir, now.Add(-10*24*time.Hour), "data/.cache/uv/environments-v2/main-0000000000000002/lib.py")
	put(t, filepath.Join(dir, "mcp", "other"), old, ".cache/uv/x")
	if err := os.Chtimes(filepath.Join(dir, "mcp", "other"), recent, recent); err != nil {
		t.Fatal(err)
	}

	e.Sweep()
	for _, gone := range []string{"data/.cache/huggingface", "mcp/files/.npm/_npx", "data/.cache/uv/environments-v2/main-0000000000000001"} {
		if _, err := os.Stat(filepath.Join(dir, gone)); !os.IsNotExist(err) {
			t.Errorf("a cache untouched for 30 days goes: %s %v", gone, err)
		}
	}
	for _, kept := range []string{"data/out/a.bin", "mcp/files/state.json", "data/.cache/pip/http/fresh", "scripts/scripts/main.py",
		"data/.cache/uv/environments-v2/main-0000000000000002/lib.py", "data/.cache/uv/environments-v2/stale-0000000000000003/lib.py",
		"mcp/other/.cache/uv/x"} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("data, a fresh cache, uv's of a script run lately and the caches of a server started lately stay: %s %v", kept, err)
		}
	}

	e.App.Snapshots.CheckDisk(context.Background(), now.Add(time.Hour))
	row := e.WaitHealth("disk", "ok")
	if !regexp.MustCompile(`; modules: \d+ MB, caches of them: \d+ MB, by module in MB: big [1-9]\d*, master 0, tiny 0; runs: \d+ MB$`).MatchString(row.Error) {
		t.Fatalf("the disk row names the folder of every module, the largest first, and its caches: %q", row.Error)
	}

	// The cleanup above changed folders by the machine's clock, past the one of the test.
	e.Clock.Add(time.Since(now) + 32*24*time.Hour)
	e.Sweep()
	if _, err := os.Stat(filepath.Join(dir, "data/.cache/uv")); !os.IsNotExist(err) {
		t.Fatalf("uv's cache of a module whose scripts ran no more for 30 days goes whole: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "data/out/a.bin")); err != nil {
		t.Fatalf("the data of a module stays: %v", err)
	}
}
