package owner

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// TestANodeProcessRunsTheScriptOfItsFolder: deck and its node are two processes, the node the binary of deck -join
// with a folder on its list; a script of a project whose folder is on the node runs there and reads it.
func TestANodeProcessRunsTheScriptOfItsFolder(t *testing.T) {
	t.Parallel()
	bin := buildDeck(t)
	data, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "notes.txt"), []byte("blue 101"), 0o600); err != nil {
		t.Fatal(err)
	}
	e := harness.New(t)
	var link struct{ Code string }
	e.Must("deck.pair_link", map[string]any{"kind": "node", "name": "Mac"}, &link)
	nodeHome := t.TempDir()
	n := exec.Command(bin, "-join", e.URL, link.Code, "--name", "Mac", "--allow", data)
	n.Env = append(os.Environ(), "DECK_HOME="+nodeHome)
	var said strings.Builder
	n.Stdout, n.Stderr = &said, &said
	// Cleanups run last first: the node is gone before what it said is read.
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("the node said:\n%s", said.String())
		}
	})
	if err := n.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = n.Process.Kill(); _ = n.Wait() })
	e.Until("the node to connect", func() bool {
		var rows []device
		e.Must("deck.devices_list", map[string]any{}, &rows)
		return slices.ContainsFunc(rows, func(d device) bool { return d.Kind == "node" && e.App.Nodes.State(d.ID).Online })
	})
	e.Project("lab", harness.ScriptManifest("read", "", ""))
	e.SaveScript("lab", "scripts/main.py", `import json, os, sys
inp = json.load(sys.stdin)
print(json.dumps({"text": open(os.path.join(inp["allowed"], "notes.txt")).read(), "cwd": os.getcwd()}))
`)
	e.Refused("deck.folder_add", map[string]any{"project": "lab", "path": nodeHome, "node": "Mac"}, 422, "a folder of deck on this machine")
	e.Must("deck.folder_add", map[string]any{"project": "lab", "path": data, "name": "data", "node": "Mac"}, nil)
	p := e.WaitState(e.Start("lab", "read", map[string]any{"allowed": data}), "done", "error")
	var got struct{ Text, Cwd string }
	_ = json.Unmarshal(p.Result, &got)
	home, _ := filepath.EvalSymlinks(nodeHome)
	if p.State != "done" || got.Text != "blue 101" || !strings.HasPrefix(got.Cwd, home) {
		t.Fatalf("the node process runs the script and it reads the folder of the node: %+v %s", p, p.Result)
	}
}
