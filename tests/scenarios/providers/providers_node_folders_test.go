package providers

import (
	"cmp"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/provider/remote"
	"deck2/tests/harness"
)

// nodeScript reads a file of the folder its input names, keeps a record through deck and leaves a result and a file
// of the data of its project.
const nodeScript = harness.PythonLib + `
inp = json.load(sys.stdin)
text = open(os.path.join(inp["allowed"], "notes.txt")).read()
st, out = deck("deck.record_upsert", {"collection": "items", "records": json.dumps({"name": text.strip()})})
open(os.path.join(os.environ["DECK_RESULTS"], "seen.txt"), "w").write(text)
open(os.path.join(os.environ["DECK_DATA"], "last.txt"), "w").write(text)
print(json.dumps({"text": text.strip(), "cwd": os.getcwd(), "saved": st}))
`

// folderOf is a folder of the machine of a node, by its real path, with notes.txt.
func folderOf(t *testing.T, text string) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// scriptsOnNode is a deck whose project lab runs nodeScript, and a node Mac that runs scripts and allows allow.
func scriptsOnNode(t *testing.T, wait time.Duration, allow ...string) (*harness.Env, *harness.Node) {
	e := harness.New(t, harness.WithConfig(func(c *app.Config) { c.NodeWait = wait }))
	n := e.PairNode("Mac")
	n.RunScripts()
	n.Allow(allow...)
	n.Start()
	e.Until("the node to connect", func() bool { return e.App.Nodes.State(n.Joined.Device).Online })
	e.Project("lab", harness.ScriptManifest("read", "", ""))
	e.SaveScript("lab", "scripts/main.py", nodeScript)
	return e, n
}

type failed struct {
	State     string `json:"state"`
	Error     string `json:"error"`
	ErrorType string `json:"error_type"`
}

// failure is how a process ended, with the type of its failure.
func failure(e *harness.Env, id int64) failed {
	var p failed
	e.Must("deck.process_get", map[string]any{"process": id}, &p)
	return p
}

// TestAScriptOfAFolderOfANodeRunsOnTheNode: a script of a folder of a node runs there, calls deck and brings back what
// it wrote; one of deck's folders runs on deck.
func TestAScriptOfAFolderOfANodeRunsOnTheNode(t *testing.T) {
	t.Parallel()
	data := folderOf(t, "blue 101")
	e, n := scriptsOnNode(t, time.Minute, data)
	var added struct {
		Name, Path, Node string
		Repo             bool
	}
	e.Must("deck.folder_add", map[string]any{"project": "lab", "path": data, "name": "data", "node": "Mac"}, &added)
	if added.Path != data || added.Node != "Mac" || added.Repo {
		t.Fatalf("a folder of a node is added by its real path on the node: %+v", added)
	}
	var rows []struct{ Name, Path, Node string }
	e.Must("deck.folders_list", map[string]any{"project": "lab"}, &rows)
	if len(rows) != 1 || rows[0].Node != "Mac" {
		t.Fatalf("the list of folders says the node of each: %+v", rows)
	}
	p := e.WaitState(e.Start("lab", "read", map[string]any{"allowed": data}), "done", "error")
	var got struct {
		Text, Cwd string
		Saved     int
	}
	_ = json.Unmarshal(p.Result, &got)
	nodeHome, _ := filepath.EvalSymlinks(n.Home)
	if p.State != "done" || got.Text != "blue 101" || got.Saved != 200 || !strings.HasPrefix(got.Cwd, nodeHome) {
		t.Fatalf("the script runs on the node, reads its folder and calls deck: %+v %s %v", p, p.Result, e.Log(p.ID))
	}
	h := deckhome.Layout(e.Home)
	if b, err := os.ReadFile(filepath.Join(h.Data(e.ProjectID("lab")), "last.txt")); err != nil || string(b) != "blue 101" {
		t.Fatalf("what the script wrote into the data of its project is on deck: %q %v", b, err)
	}
	var items []harness.RecordRow
	e.Must("deck.records_list", map[string]any{"project": "lab", "collection": "items"}, &items)
	if len(items) != 1 || items[0].Data["name"] != "blue 101" {
		t.Fatalf("the record the script kept through deck is on deck: %+v", items)
	}
	if entries, _ := os.ReadDir(filepath.Dir(deckhome.Layout(n.Home).Mirror("x"))); len(entries) != 0 {
		t.Fatalf("the node keeps no files of a script deck took back: %v", entries)
	}

	local := folderOf(t, "red 7")
	e.Project("home", harness.ScriptManifest("read", "", ""))
	e.SaveScript("home", "scripts/main.py", nodeScript)
	e.Must("deck.folder_add", map[string]any{"project": "home", "path": local, "name": "data"}, nil)
	p = e.WaitState(e.Start("home", "read", map[string]any{"allowed": local}), "done", "error")
	_ = json.Unmarshal(p.Result, &got)
	deckHome, _ := filepath.EvalSymlinks(e.Home)
	if p.State != "done" || got.Text != "red 7" || !strings.HasPrefix(got.Cwd, deckHome) {
		t.Fatalf("a script of a project whose folders are on deck runs on deck: %+v %s", p, p.Result)
	}
}

// TestAFolderItsNodeDoesNotAllowIsRefused: a folder outside the list of its node is not added, a folder the owner
// takes off the list later refuses the script that reads it, and health says what the owner adds.
func TestAFolderItsNodeDoesNotAllowIsRefused(t *testing.T) {
	t.Parallel()
	data, other := folderOf(t, "blue 101"), folderOf(t, "secret")
	e, n := scriptsOnNode(t, time.Minute, data)
	e.Refused("deck.folder_add", map[string]any{"project": "lab", "path": other, "node": "Mac"}, 422,
		"folder "+other+" is not allowed on this node: on its machine add it with deck -join <deck's address> --install --allow '"+other+"'")
	e.Refused("deck.folder_add", map[string]any{"project": "lab", "path": filepath.Join(other, "gone"), "node": "Mac"}, 422, "is not allowed on this node")
	// A list that holds the folder of the node still keeps it out, however a path spells it.
	n.Allow(data, filepath.Dir(n.Home))
	upper := filepath.Join(filepath.Dir(n.Home), strings.ToUpper(filepath.Base(n.Home)))
	for _, path := range []string{n.Home, filepath.Join(n.Home, "state"), upper} {
		e.Refused("deck.folder_add", map[string]any{"project": "lab", "path": path, "node": "Mac"}, 422, "")
	}
	e.Refused("deck.folder_add", map[string]any{"project": "lab", "path": n.Home, "node": "Mac"}, 422, "a folder of deck on this machine")
	n.Allow(data)
	e.Must("deck.folder_add", map[string]any{"project": "lab", "path": data, "name": "data", "node": "Mac"}, nil)
	n.Allow(data, other)
	e.Refused("deck.folder_add", map[string]any{"project": "lab", "path": other, "name": "data", "node": "Mac"}, 422, "already has a folder named data")
	n.Allow()
	id := e.Start("lab", "read", map[string]any{"allowed": data})
	e.WaitState(id, "done", "error")
	if p := failure(e, id); p.State != "error" || p.ErrorType != "rights.denied" || !strings.Contains(p.Error, "--allow '"+data+"'") {
		t.Fatalf("the node refuses the script of a folder its owner took off the list: %+v", p)
	}
	h := e.WaitHealth("node:Mac", "warning")
	if !strings.Contains(h.Error, "folder '"+data+"' is not allowed on it") || !strings.Contains(h.Error, "--install --allow '"+data+"'") {
		t.Fatalf("the health of the node says which folder its owner adds: %+v", h)
	}
}

// TestAScriptOfANodeAwayWaitsForIt: a script whose node is away waits for it, as the feed and health say, and runs once
// it is back; one whose node does not come back in time fails and says so.
func TestAScriptOfANodeAwayWaitsForIt(t *testing.T) {
	t.Parallel()
	data := folderOf(t, "blue 101")
	e, n := scriptsOnNode(t, harness.Within(2*time.Second), data)
	e.Must("deck.folder_add", map[string]any{"project": "lab", "path": data, "name": "data", "node": "Mac"}, nil)
	n.Stop()
	if h := e.WaitHealth("node:Mac", "warning"); !strings.Contains(h.Error, "the turns and scripts of its folders wait for it") {
		t.Fatalf("the health of a node away says its folders wait: %+v", h)
	}
	gone := e.Start("lab", "read", map[string]any{"allowed": data})
	e.WaitState(gone, "done", "error")
	if p := failure(e, gone); p.State != "error" || p.ErrorType != "network" || !strings.Contains(p.Error, "did not come back") {
		t.Fatalf("a script whose node does not come back fails and says so: %+v", p)
	}
	id := e.Start("lab", "read", map[string]any{"allowed": data})
	e.Until("the log to say the script waits", func() bool {
		b, _ := json.Marshal(e.Log(id))
		return strings.Contains(string(b), "script main waits up to")
	})
	n.Start()
	if p := e.WaitState(id, "done", "error"); p.State != "done" {
		t.Fatalf("a script whose node came back runs there: %+v %v", p, e.Log(id))
	}
}

// TestAnAgentCommitsInARepositoryOfItsNode: an agent command whose repository is on a node runs on the agent of the
// node, though its assignment names deck's own, and commits there; deck's agent takes no turn.
func TestAnAgentCommitsInARepositoryOfItsNode(t *testing.T) {
	own, onNode := harness.NewCodex(t), harness.NewCodex(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	repo := folderOf(t, "blue 101")
	for _, args := range [][]string{{"init", "-q"}, {"add", "."}, {"-c", "user.name=owner", "-c", "user.email=owner@example.com", "commit", "-qm", "start"}} {
		if out, err := exec.Command("git", append([]string{"-C", repo}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}
	raw, err := os.ReadFile(harness.Testdata("codex/ok.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	var stream []string
	for _, l := range lines {
		if strings.HasPrefix(l, `{"mcp"`) {
			stream = append(stream, `{"shell": "echo blue > color.txt && git add color.txt && git -c user.name=agent -c user.email=agent@example.com commit -qm blue"}`)
		}
		stream = append(stream, l)
	}
	onNode.StreamText(1, strings.Join(stream, "\n"))
	e := harness.New(t, harness.WithCodex(own))
	n := e.PairNode("Mac")
	n.RunAgents(func(spec remote.Spec, home string, _ func(provider.ModelHooks) http.Handler) provider.Provider {
		return onNode.Adapter(home)
	})
	n.Allow("codex", repo)
	n.Start()
	e.Until("codex@Mac among the agents", func() bool { return hasModel(e, "codex@Mac") })
	p := e.Project("paint", strings.Replace(harness.ColorManifest, `"executor": {"type": "agent",`, `"executor": {"type": "agent", "folder": true,`, 1))
	var added struct{ Repo bool }
	e.Must("deck.folder_add", map[string]any{"project": "paint", "path": repo, "node": "Mac"}, &added)
	if !added.Repo {
		t.Fatalf("the node says its folder is a repository: %+v", added)
	}
	e.UseModels("paint.work", "codex")
	id := e.Run(p, "work")
	got := e.WaitState(id, "done", "error")
	var proc struct{ Provider string }
	e.Must("deck.process_get", map[string]any{"process": id}, &proc)
	if got.State != "done" || proc.Provider != "codex@Mac" {
		t.Fatalf("the turn of a folder of a node runs on the agent of the node: %+v %+v %v", got, proc, e.Log(id))
	}
	if c := onNode.Call(1); strings.TrimSpace(c.Cwd) != repo {
		t.Fatalf("the agent works in the repository on the node: %q", c.Cwd)
	}
	out, err := exec.Command("git", "-C", repo, "log", "--format=%s").Output()
	if err != nil || strings.Fields(string(out))[0] != "blue" {
		t.Fatalf("the agent committed in the repository of the node: %q %v", out, err)
	}
	if _, err := os.Stat(filepath.Join(own.Dir, "calls")); err == nil {
		t.Fatal("deck's own Codex took no turn")
	}
}

// TestAFolderGoesOnlyFromTheMachineItIsOn: removing a folder takes only the row of the machine named, deck's own by
// default; a revoked node takes its folders with it.
func TestAFolderGoesOnlyFromTheMachineItIsOn(t *testing.T) {
	t.Parallel()
	mine, theirs := folderOf(t, "a"), folderOf(t, "b")
	for _, dir := range []string{mine, theirs} {
		if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
			t.Fatalf("git init: %v %s", err, out)
		}
	}
	e, n := scriptsOnNode(t, time.Minute, theirs)
	rows := func() (on []string) {
		var rs []struct{ Name, Node string }
		e.Must("deck.folders_list", map[string]any{"project": "lab"}, &rs)
		for _, r := range rs {
			on = append(on, r.Name+"@"+cmp.Or(r.Node, "deck"))
		}
		return on
	}
	addMine := func() { e.Must("deck.folder_add", map[string]any{"project": "lab", "path": mine, "name": "code"}, nil) }
	addTheirs := func() {
		e.Must("deck.folder_add", map[string]any{"project": "lab", "path": theirs, "name": "code", "node": "Mac"}, nil)
	}
	addMine()
	addTheirs()
	e.Must("deck.folder_delete", map[string]any{"project": "lab", "name": "code"}, nil)
	if got := rows(); !slices.Equal(got, []string{"code@Mac"}) {
		t.Fatalf("a folder removed without a node goes from deck's machine only: %v", got)
	}
	e.Refused("deck.folder_delete", map[string]any{"project": "lab", "name": "code"}, 404, "no folder code on deck's machine")
	addMine()
	e.Must("deck.folder_delete", map[string]any{"project": "lab", "name": "code", "node": "Mac"}, nil)
	if got := rows(); !slices.Equal(got, []string{"code@deck"}) {
		t.Fatalf("a folder removed on a node goes from that node only: %v", got)
	}
	addTheirs()
	e.Must("deck.device_revoke", map[string]any{"device": n.Joined.Device}, nil)
	if got := rows(); !slices.Equal(got, []string{"code@deck"}) {
		t.Fatalf("a revoked node takes its folders with it: %v", got)
	}
}

// TestScriptsOnANodeKeepEveryChangeOfTheirData: the scripts of a node work on a copy of the data of their project, so
// runs of a command that may run side by side take turns there but for one it called, and a changed file stays deck's.
func TestScriptsOnANodeKeepEveryChangeOfTheirData(t *testing.T) {
	t.Parallel()
	data := folderOf(t, "x")
	e, _ := scriptsOnNode(t, time.Minute, data)
	e.Project("count", strings.Replace(harness.ScriptManifest("read", "", ""), `"executor": {"type": "script"`, `"runs": "many", "executor": {"type": "script"`, 1))
	e.SaveScript("count", "scripts/main.py", harness.PythonLib+`
import time
inp = json.load(sys.stdin)
if inp.get("use") == "parent":
    st, out = deck("count.read", {"allowed": inp["allowed"], "use": "child"})
    for _ in range(300):
        _, child = deck("deck.process_get", {"process": out["process"]})
        if child["state"] in ("done", "error"):
            break
        time.sleep(0.1)
    print(json.dumps({"child": child["state"]}))
    sys.exit(0)
at = os.path.join(os.environ["DECK_DATA"], "count.txt")
n = int(open(at).read()) if os.path.exists(at) else 0
if inp.get("use") == "wait":
    print("started", file=sys.stderr, flush=True)
    while not os.path.exists(os.path.join(inp["allowed"], "go")):
        time.sleep(0.1)
else:
    time.sleep(1)
open(at, "w").write(str(n + 1))
print(json.dumps({"n": n + 1}))
`)
	e.Must("deck.folder_add", map[string]any{"project": "count", "path": data, "name": "data", "node": "Mac"}, nil)
	a, b := e.Start("count", "read", map[string]any{"allowed": data}), e.Start("count", "read", map[string]any{"allowed": data})
	for _, id := range []int64{a, b} {
		if p := e.WaitState(id, "done", "error"); p.State != "done" {
			t.Fatalf("a script of a node runs: %+v %v", p, e.Log(id))
		}
	}
	at := filepath.Join(deckhome.Layout(e.Home).Data(e.ProjectID("count")), "count.txt")
	if b, _ := os.ReadFile(at); string(b) != "2" {
		t.Fatalf("two runs of a node one after the other keep both changes: %q", b)
	}

	id := e.Start("count", "read", map[string]any{"allowed": data, "use": "wait"})
	e.Until("the script to start on the node", func() bool { return strings.Contains(fmt.Sprint(e.Log(id)), "started") })
	if err := os.WriteFile(at, []byte("10"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(data, "go"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	p := e.WaitState(id, "done", "error")
	if b, _ := os.ReadFile(at); p.State != "done" || string(b) != "10" {
		t.Fatalf("a file deck changed while the script ran on the node stays deck's: %q %+v", b, p)
	}
	if log := fmt.Sprint(e.Log(id)); !strings.Contains(log, "count.txt") || !strings.Contains(log, "stay as they are on deck") {
		t.Fatalf("the log of the run says which of its changes are lost: %s", log)
	}
	p = e.WaitState(e.Start("count", "read", map[string]any{"allowed": data, "use": "parent"}), "done", "error")
	if p.State != "done" || !strings.Contains(string(p.Result), `"child":"done"`) {
		t.Fatalf("a run that calls its own command does not wait for itself: %+v %s", p, p.Result)
	}
}
