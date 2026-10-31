package scenarios

import (
	"archive/tar"
	"compress/gzip"
	"context"
	deckhome "deck2/internal/home"
	"deck2/internal/registry"
	"deck2/internal/sandbox"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func served(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("hello")) }))
	t.Cleanup(s.Close)
	return s
}

const collectScript = harness.PythonLib + `
inp = json.load(sys.stdin)
checks = {}
st, out = deck("deck.records_upsert", {"collection": "items", "records": json.dumps({"name": "a", "size": inp["n"]})})
checks["internal_call"] = st == 200 and out["written"] == 1
try:
    urllib.request.urlopen(urllib.request.Request(URL + "/commands", headers={"Authorization": "Bearer " + TOKEN}), timeout=20)
    checks["no_command_list"] = False
except urllib.error.HTTPError as e:
    checks["no_command_list"] = e.code in (404, 405)
rid = out["ids"][0] if st == 200 else 0
st, out = deck(inp["project"] + ".publish", {})
checks["external_waits"] = st == 202 and out["waiting"] > 0
checks["allowed_domain"] = fetch(inp["allowed"]) == 200
checks["other_domain_refused"] = fetch(inp["other"]) == 403
checks["direct_network_blocked"] = fetch(inp["allowed"], proxy=False) != 200
import re
checks["proxy_needs_token"] = fetch(inp["allowed"], proxy=re.sub("//[^@]*@", "//", os.environ["HTTP_PROXY"])) == 407
checks["proxy_key_is_not_the_token"] = fetch(inp["allowed"], proxy=re.sub("//[^@]*@", "//deck:" + TOKEN + "@", os.environ["HTTP_PROXY"])) == 407
checks["outside_write_blocked"] = not can_write(os.path.join(inp["outside"], "x"))
checks["secret_hidden"] = not can_read(inp["secret"])
checks["owner_key_hidden"] = not can_read(inp["key"])
import time
time.sleep(2)
checks["late_key_hidden"] = not can_read(inp["late"])
import socket, subprocess
try:
    s = socket.socket(socket.AF_UNIX)
    s.connect(inp["socket"])
    checks["host_socket_blocked"] = False
except OSError:
    checks["host_socket_blocked"] = True
lock = os.path.join(os.environ["DECK_DATA"], "child.lock")
subprocess.Popen([sys.executable, "-c", "import fcntl, sys, time; f = open(sys.argv[1], 'w'); fcntl.flock(f, fcntl.LOCK_EX); open(sys.argv[1] + '.held', 'w').close(); time.sleep(60)", lock])
while not os.path.exists(lock + ".held"):
    time.sleep(0.05)
with open(os.path.join(os.environ["DECK_RESULTS"], "report.txt"), "w") as f:
    f.write("report %d" % inp["n"])
print(json.dumps({"checks": checks, "files": [{"path": "report.txt", "record": rid}]}))
`

func TestScriptCommandRunsInTheSandbox(t *testing.T) {
	owner := ownerHome(t)
	key := filepath.Join(owner, ".ssh", "id_ed25519")
	_ = os.MkdirAll(filepath.Dir(key), 0o700)
	_ = os.WriteFile(key, []byte("private"), 0o600)
	t.Setenv("HOME", owner)
	e := harness.New(t)
	allowed, other := served(t), served(t)
	host := strings.TrimPrefix(allowed.URL, "http://")
	pid := e.Project("tools", harness.ScriptManifest("collect", "", fmt.Sprintf(`, "domains": {"read": [%q]}`, host)))
	e.SaveScript("tools", "scripts/main.py", collectScript)
	home := e.App.Cfg.Home
	secret := filepath.Join(deckhome.Layout(home).State(), "secrets", "key")
	_ = os.MkdirAll(filepath.Dir(secret), 0o700)
	_ = os.WriteFile(secret, []byte("s3cret"), 0o600)
	outside := t.TempDir()
	sockDir, _ := os.MkdirTemp("/tmp", "ds")
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	sock, err := net.Listen("unix", filepath.Join(sockDir, "s"))
	if err != nil {
		t.Fatal(err)
	}
	defer sock.Close()

	late := filepath.Join(owner, ".aws", "credentials")
	id := e.Start("tools", "collect", map[string]any{"n": 7, "project": "tools", "allowed": allowed.URL, "other": other.URL,
		"outside": outside, "secret": secret, "key": key, "late": late, "socket": sock.Addr().String()})
	// The sandbox is up before the late file appears; no read shows that.
	time.Sleep(500 * time.Millisecond)
	_ = os.MkdirAll(filepath.Dir(late), 0o700)
	_ = os.WriteFile(late, []byte("aws-secret"), 0o600)
	began := time.Now()
	p := e.WaitState(id, "done", "error")
	if p.State != "done" || p.NotBySchema || time.Since(began) > 20*time.Second {
		t.Fatalf("the script command finishes by its schema, not waiting for programs it started: %+v", p)
	}
	var res struct {
		Checks map[string]bool `json:"checks"`
		Files  []struct{ Record int64 }
	}
	_ = json.Unmarshal(p.Result, &res)
	for _, c := range []string{"internal_call", "no_command_list", "external_waits", "allowed_domain", "other_domain_refused", "direct_network_blocked", "proxy_needs_token", "proxy_key_is_not_the_token",
		"outside_write_blocked", "secret_hidden", "owner_key_hidden", "late_key_hidden", "host_socket_blocked"} {
		// deck's container as the sandbox leaves the network to a program that does not take the proxy.
		if !res.Checks[c] && !(c == "direct_network_blocked" && sandbox.Contained()) {
			t.Errorf("check %s failed: %s", c, p.Result)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "x")); err == nil {
		t.Fatal("the script wrote outside its folders")
	}
	if a := e.WaitAsk(); a.Kind != "call" || !strings.Contains(a.Text, ".publish") {
		t.Fatalf("the frozen external call of a script stays for the owner after the script ends: %+v", a)
	}
	var files []struct {
		ID        int64
		Name      string
		OwnerKind string `json:"owner_kind"`
	}
	e.Must("deck.files_list", map[string]any{"record": res.Files[0].Record}, &files)
	if len(files) != 1 || files[0].Name != "report.txt" || files[0].OwnerKind != "record" {
		t.Fatalf("the result file is bound to the record: %+v", files)
	}
	if b, _ := os.ReadFile(filepath.Join(deckhome.Layout(home).State(), "files", fmt.Sprint(files[0].ID))); string(b) != "report 7" {
		t.Fatalf("the file moved into files/: %q", b)
	}
	if left, _ := os.ReadDir(filepath.Join(deckhome.Layout(home).Work(), "runs", fmt.Sprint(id))); len(left) != 1 || left[0].Name() != "stream" {
		t.Fatalf("a finished script leaves only its log in its run folder: %v", left)
	}
	if b, _ := json.Marshal(log(e, id)); strings.Contains(string(b), "xcodebuild") || strings.Contains(string(b), "DVTDeveloperPaths") {
		t.Fatalf("python runs as the interpreter itself, not the xcrun launcher: %s", b)
	}
	page := e.ScreenData("", "process", map[string]any{"record": id})["b0"]["value"]
	if b, _ := json.Marshal(page); strings.Contains(string(b), `"Error"`) {
		t.Fatalf("a process without an error shows no Error field: %s", b)
	}
	lock := filepath.Join(deckhome.Layout(home).Work(), "projects", fmt.Sprint(pid), "data", "child.lock")
	e.Until("the program the script started to end with the run", func() bool {
		f, err := os.OpenFile(lock, os.O_RDWR, 0)
		if err != nil {
			return false
		}
		defer f.Close()
		return syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB) == nil
	})
}

const indexScript = harness.PythonLib + `
inp = json.load(sys.stdin)
path = os.path.join(os.environ["DECK_DATA"], "index.json")
runs = 0
if os.path.exists(path):
    with open(path) as f:
        runs = json.load(f)["runs"]
with open(path, "w") as f:
    json.dump({"runs": runs + 1}, f)
print(json.dumps({"seen": runs, "folder_write_blocked": not can_write(os.path.join(inp["outside"], "x"))}))
`

func TestScriptKeepsAnIndexInTheProjectDataFolder(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	pid := e.Project("index", harness.ScriptManifest("build", "", ""))
	e.SaveScript("index", "scripts/main.py", indexScript)
	folder := t.TempDir()
	for want := range 2 {
		p := e.WaitState(e.Start("index", "build", map[string]any{"outside": folder}), "done", "error")
		var res struct {
			Seen    int  `json:"seen"`
			Blocked bool `json:"folder_write_blocked"`
		}
		_ = json.Unmarshal(p.Result, &res)
		if p.State != "done" || res.Seen != want || !res.Blocked {
			t.Fatalf("run %d: the next run sees the index and project folders stay read-only: %+v", want, p)
		}
	}
	var snap struct{ Path string }
	e.Must("deck.snapshot_create", nil, &snap)
	names := tarNames(t, snap.Path)
	want := fmt.Sprintf("work/projects/%d/data/index.json", pid)
	if !names[want] || !names["state/deck.db"] {
		t.Fatalf("the snapshot holds the base and the data folder: %v", names)
	}
	for n := range names {
		if strings.HasPrefix(n, "secrets") || n == "token" {
			t.Fatalf("secrets stay out of snapshots: %s", n)
		}
	}
}

func tarNames(t *testing.T, path string) map[string]bool {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	names := map[string]bool{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return names
		}
		if err != nil {
			t.Fatal(err)
		}
		names[h.Name] = true
	}
}

// packageScript installs its package itself: uv reads the PEP 723 block and goes to the index through the proxy.
const packageScript = `#!/usr/bin/env -S uv run --script
# /// script
# dependencies = ["deckfixture"]
# [tool.uv]
# index-url = "%s/simple"
# ///
import json, deckfixture
print(json.dumps({"value": deckfixture.VALUE}))
`

func TestScriptCeilingAndThreeFailuresInARow(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	e.Project("slow", harness.ScriptManifest("wait", "", ""))
	e.SaveScript("slow", "scripts/main.py", "import sys, time\nprint('started', file=sys.stderr, flush=True)\ntime.sleep(120)\n")
	id := e.Start("slow", "wait", nil)
	e.Until("the script to start", func() bool { return len(log(e, id)) > 0 })
	e.Clock.Add(9 * time.Minute)
	e.Sweep()
	if p := e.Proc(id); p.State != "running" {
		t.Fatalf("a script runs until its ceiling of 10 minutes: %+v", p)
	}
	e.Clock.Add(2 * time.Minute)
	e.Sweep()
	if p := e.WaitState(id, "stopped", "done", "error"); p.State != "error" || !strings.Contains(p.Error, "ceiling") {
		t.Fatalf("the ceiling stops the script: %+v", p)
	}

	e.Project("fails", harness.ScriptManifest("break", "", ""))
	e.SaveScript("fails", "scripts/main.py", "import sys\nprint('boom', file=sys.stderr)\nsys.exit(3)\n")
	for i := 1; i <= 4; i++ {
		p := e.WaitState(e.Start("fails", "break", nil), "error", "done")
		if p.State != "error" || !strings.Contains(p.Error, "boom") {
			t.Fatalf("a failing script is an error with its stderr: %+v", p)
		}
		want := 0
		if i >= 3 {
			want = 1
			e.Until("the warning", func() bool { return e.Warnings("failed 3 times in a row") > 0 })
		}
		if n := e.Warnings("failed 3 times in a row"); n != want {
			t.Fatalf("after failure %d: %d warnings, want %d", i, n, want)
		}
	}
}

func TestScriptInterruptedByARestartIsNotResumed(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e1 := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	e1.Project("restart", harness.ScriptManifest("wait", "", ""))
	e1.SaveScript("restart", "scripts/main.py", "import sys, time\nprint('started', file=sys.stderr, flush=True)\ntime.sleep(120)\n")
	id := e1.Start("restart", "wait", nil)
	e1.Until("the script to start", func() bool { return len(log(e1, id)) > 0 })
	_ = e1.App.Close()
	e2 := harness.New(t, harness.WithHome(home), harness.WithProvider(&harness.Fake{}))
	if err := e2.App.Runner.ResumeInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	if p := e2.Proc(id); p.State != "interrupted" {
		t.Fatalf("a script cut by a restart is interrupted: %+v", p)
	}
	if e2.Warnings("interrupted by a restart") != 1 {
		t.Fatal("the owner is told to run it again")
	}
}

func TestResultFilesStayInsideTheResultsFolder(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("escape", harness.ScriptManifest("take", "", ""))
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.txt")
	_ = os.WriteFile(victim, []byte("mine"), 0o600)
	for _, path := range []string{"link/victim.txt", "../../../" + victim, "direct"} {
		e.SaveScript("escape", "scripts/main.py", fmt.Sprintf(`import json, os
res = os.environ["DECK_RESULTS"]
os.symlink(%q, os.path.join(res, "link"))
os.symlink(%q, os.path.join(res, "direct"))
print(json.dumps({"files": [{"path": %q}]}))
`, outside, victim, path))
		p := e.WaitState(e.Start("escape", "take", nil), "done", "error")
		if p.State != "error" || !strings.Contains(p.Error, "not a file in the results folder") {
			t.Fatalf("%s: a result file outside the results folder is refused: %+v", path, p)
		}
		if b, err := os.ReadFile(victim); err != nil || string(b) != "mine" {
			t.Fatalf("%s: the file outside stays where it was: %v", path, err)
		}
	}
}

func TestResultFilesAreKeptWholeOrNotAtAll(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("twice", harness.ScriptManifest("take", "", ""))
	e.SaveScript("twice", "scripts/main.py", `import json, os
open(os.path.join(os.environ["DECK_RESULTS"], "a.txt"), "w").write("a")
print(json.dumps({"files": [{"path": "a.txt"}, {"path": "./a.txt"}]}))
`)
	id := e.Start("twice", "take", nil)
	if p := e.WaitState(id, "done", "error"); p.State != "error" || !strings.Contains(p.Error, "named twice") {
		t.Fatalf("a file named twice is refused: %+v", p)
	}
	var files []struct{ ID int64 }
	e.Must("deck.files_list", map[string]any{"process": id}, &files)
	if len(files) != 0 {
		t.Fatalf("a refused result keeps no file rows: %+v", files)
	}
}

func TestAChattyScriptDoesNotWriteTheDatabaseForEachLine(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	e.Project("chatty", harness.ScriptManifest("talk", "", ""))
	e.SaveScript("chatty", "scripts/main.py", "import sys\nfor i in range(3000):\n    print('line', i, file=sys.stderr, flush=True)\nprint('null')\n")
	changes := func() int {
		var n int
		if err := e.App.Store.DB.QueryRow(`SELECT total_changes()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := changes()
	if p := e.WaitState(e.Start("chatty", "talk", nil), "done", "error"); p.State != "done" {
		t.Fatalf("the script finishes: %+v", p)
	}
	if n := changes() - before; n > 300 {
		t.Fatalf("3000 lines of stderr are one output mark a second, not a write each: %d rows changed", n)
	}
}

// TestAScriptReadsNoFolderOfAnotherProject: the data of another project, its runs and deck's own files stay closed
// to a script, which reads and writes only its own.
func TestAScriptReadsNoFolderOfAnotherProject(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("other", harness.ScriptManifest("collect", "", ""))
	b := e.Project("tools", harness.ScriptManifest("collect", "", ""))
	theirs := filepath.Join(deckhome.Layout(e.App.Cfg.Home).Work(), "projects", fmt.Sprint(b-1), "data", "secret.txt")
	if err := os.MkdirAll(filepath.Dir(theirs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(theirs, []byte("theirs"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.SaveScript("tools", "scripts/main.py", harness.PythonLib+`
inp = json.load(sys.stdin)
print(json.dumps({"read": can_read(inp["other"]), "write": can_write(os.path.join(os.path.dirname(inp["other"]), "x")),
  "db": can_read(inp["outside"]), "own": can_write(os.path.join(os.environ["DECK_DATA"], "x"))}))
`)
	p := e.WaitState(e.Start("tools", "collect", map[string]any{"other": theirs, "outside": filepath.Join(deckhome.Layout(e.App.Cfg.Home).State(), "deck.db")}), "done", "error")
	var got struct{ Read, Write, DB, Own bool }
	_ = json.Unmarshal(p.Result, &got)
	if p.State != "done" || got.Read || got.Write || got.DB || !got.Own {
		t.Fatalf("a script reads and writes only its own folders: %+v %s", p, p.Result)
	}
}

// ownerHome is a home of the owner outside the temp folders, which the Linux sandbox empties anyway.
func ownerHome(t *testing.T) string {
	t.Helper()
	cache, err := os.UserCacheDir()
	if err == nil {
		_ = os.MkdirAll(cache, 0o700)
	}
	dir, err := os.MkdirTemp(cache, "deck2-owner-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

const homeScript = harness.PythonLib + `
import subprocess
inp = json.load(sys.stdin)
home = os.path.expanduser(inp["home"])
tool = subprocess.run(["tool"], capture_output=True, text=True)
tools = {}
for t in ["local", "shim", "linked", "doc"]:
    try:
        tools[t] = subprocess.run([t], capture_output=True, text=True).stdout.strip()
    except OSError as e:
        tools[t] = str(e)
try:
    listed = ".zshrc" in os.listdir(home)
except OSError:
    listed = False
print(json.dumps({"rc": can_read(os.path.join(home, ".zshrc")), "documents": can_read(os.path.join(home, "Documents", "notes.txt")),
  "config": can_read(os.path.join(home, ".config", "app", "token")), "listed": listed,
  "runtime": tool.stdout.strip(), "folder": can_read(os.path.join(inp["folder"], "readme")),
  "folder_write": can_write(os.path.join(inp["folder"], "x")), "cpus": os.cpu_count(), "tools": tools,
  "state": can_read(os.path.join(home, ".local", "state", "history")), "beside": can_read(os.path.join(home, "Documents", "scripts", "other.txt"))}))
`

// TestTheSandboxClosesTheHomeButForRuntimesAndTheFoldersOfTheProject: a script and an MCP server read nothing of the
// owner's home folder but the runtimes deck finds on PATH and the folders the owner gave the project, read-only.
func TestTheSandboxClosesTheHomeButForRuntimesAndTheFoldersOfTheProject(t *testing.T) {
	owner := ownerHome(t)
	for path, text := range map[string]string{".zshrc": "export OPENAI_API_KEY=sk-owner", "Documents/notes.txt": "private",
		".config/app/token": "t0ken", "rt/lib/tool/data": "runtime-data", "code/readme": "the code",
		"rt/bin/tool":      "#!/bin/sh\ncat \"$(dirname \"$0\")/../lib/tool/data\"\n",
		".local/bin/local": "#!/bin/sh\necho local\n", ".local/lib/x": "", ".local/state/history": "rm -rf secrets",
		".pyenv/shims/shim": "#!/bin/sh\ncat \"$(dirname \"$0\")/../versions/1/data\"\n", ".pyenv/versions/1/data": "shim-data",
		"sdk/v1/bin/linked": "#!/bin/sh\ncat \"$(dirname \"$0\")/../lib/data\"\n", "sdk/v1/lib/data": "linked-data",
		"Documents/scripts/doc.sh": "#!/bin/sh\necho doc\n", "Documents/scripts/other.txt": "private"} {
		p := filepath.Join(owner, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(owner, "sdk", "v1"), filepath.Join(owner, "sdk", "current")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(owner, "Documents", "scripts", "doc.sh"), filepath.Join(owner, "rt", "bin", "doc")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", owner)
	path := []string{filepath.Join(owner, "rt", "bin"), filepath.Join(owner, ".local", "bin"), filepath.Join(owner, ".pyenv", "shims"),
		filepath.Join(owner, "sdk", "current", "bin"), os.Getenv("PATH")}
	t.Setenv("PATH", strings.Join(path, string(os.PathListSeparator)))
	e := harness.New(t)
	self := harness.Fakes
	e.Project("tools", harness.ScriptManifest("collect", "", fmt.Sprintf(`, "tools": {"mcp": {"servers": {"fake": {"command": %q,
    "env": {"DECK_FAKE_MCP": "project"}, "toolFilter": ["peek"], "classes": {"peek": "internal"}}}}}`, self)))
	e.Must("deck.folder_add", map[string]any{"project": "tools", "path": filepath.Join(owner, "code")}, nil)
	e.SaveScript("tools", "scripts/main.py", homeScript)
	p := e.WaitState(e.Start("tools", "collect", map[string]any{"home": owner, "folder": filepath.Join(owner, "code")}), "done", "error")
	var got struct {
		RC, Documents, Config, Listed, Folder, FolderWrite, State, Beside bool
		Runtime                                                           string
		CPUs                                                              int
		Tools                                                             map[string]string
	}
	_ = json.Unmarshal(p.Result, &got)
	want := map[string]string{"local": "local", "shim": "shim-data", "linked": "linked-data", "doc": "doc"}
	if p.State != "done" || got.RC || got.Documents || got.Config || got.Listed || got.Runtime != "runtime-data" || !got.Folder || got.FolderWrite || got.CPUs < 1 ||
		!maps.Equal(got.Tools, want) || got.State || got.Beside {
		t.Fatalf("a script reads of the home only the runtimes on PATH and the folders of its project: %+v %s", p, p.Result)
	}
	for path, want := range map[string]string{filepath.Join(owner, ".zshrc"): "read=false", filepath.Join(owner, "code", "readme"): "read=true"} {
		out, _ := e.App.Gateway.CallAs(context.Background(), registry.Actor{Kind: registry.Agent, Workspace: 1, ProjectID: e.ProjectID("tools")},
			"tools.fake.peek", json.RawMessage(fmt.Sprintf(`{"path": %q}`, path)))
		if b, _ := json.Marshal(out); !strings.Contains(string(b), want) {
			t.Fatalf("an MCP server reads of the home only what a script does: reading %s gave %s", path, b)
		}
	}
}

const processesScript = harness.PythonLib + `
import ctypes, platform
inp = json.load(sys.stdin)
listed = len([d for d in os.listdir("/proc") if d.isdigit()]) > 10 if os.path.isdir("/proc") else None
if sys.platform == "darwin":
    libc, size = ctypes.CDLL(None), ctypes.c_size_t(0)
    listed = libc.sysctl((ctypes.c_int * 3)(1, 14, 0), 3, None, ctypes.byref(size), None, 0) == 0 and size.value > 0
print(json.dumps({"listed": listed, "deck": os.path.exists("/proc/%d/environ" % inp["n"]), "cpus": os.cpu_count(),
  "system": platform.system(), "release": platform.release()}))
`

// TestAScriptSeesNoOtherProcess: a script lists no processes of the machine but reads what the machine and its
// system are.
func TestAScriptSeesNoOtherProcess(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("tools", harness.ScriptManifest("collect", "", ""))
	e.SaveScript("tools", "scripts/main.py", processesScript)
	p := e.WaitState(e.Start("tools", "collect", map[string]any{"n": os.Getpid()}), "done", "error")
	var got struct {
		Listed, Deck    bool
		CPUs            int
		System, Release string
	}
	_ = json.Unmarshal(p.Result, &got)
	if p.State != "done" || got.Listed || got.Deck || got.CPUs < 1 || got.System == "" || got.Release == "" {
		t.Fatalf("a script sees the machine and its system, not the processes on it: %+v %s", p, p.Result)
	}
}

// TestAScriptThatFailsSaysHowToOpenAFolder: a script that fails has the end of its stderr, where its program names
// the path it was refused, and where the owner opens a folder; deck does not watch the refusals of the system.
func TestAScriptThatFailsSaysHowToOpenAFolder(t *testing.T) {
	owner := ownerHome(t)
	_ = os.WriteFile(filepath.Join(owner, ".zshrc"), []byte("private"), 0o600)
	t.Setenv("HOME", owner)
	e := harness.New(t)
	e.Project("tools", harness.ScriptManifest("collect", "", ""))
	e.SaveScript("tools", "scripts/main.py", "import json, sys\nprint(open(json.load(sys.stdin)[\"key\"]).read())\n")
	id := e.Start("tools", "collect", map[string]any{"key": filepath.Join(owner, ".zshrc")})
	p := e.WaitState(id, "done", "error")
	if p.State != "error" || !strings.Contains(p.Error, filepath.Join(owner, ".zshrc")) || !strings.Contains(p.Error, "the settings of the module, Access") {
		t.Fatalf("the error of the script names the path from its stderr and how to open a folder: %+v", p)
	}
}

// TestAScriptEndsWhenItExitsThoughAChildHoldsItsOutput: a program the script left running with its stdout and
// stderr does not hold the run; output past 32 MB is an error.
func TestAScriptEndsWhenItExitsThoughAChildHoldsItsOutput(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("tools", harness.ScriptManifest("collect", "", ""))
	e.SaveScript("tools", "scripts/main.py", `import json, subprocess, sys
if json.load(sys.stdin).get("n") == 1:
    sys.stdout.write("x" * (33 << 20))
    sys.exit(0)
subprocess.Popen(["sleep", "60"])
print(json.dumps({"ok": True}))
`)
	start := time.Now()
	got := e.WaitState(e.Start("tools", "collect", nil), "done", "error")
	if got.State != "done" || string(got.Result) != `{"ok":true}` || time.Since(start) > 30*time.Second {
		t.Fatalf("the run ends with the script, not with its child: %+v after %s", got, time.Since(start))
	}
	got = e.WaitState(e.Start("tools", "collect", map[string]any{"n": 1}), "done", "error")
	if got.State != "error" {
		t.Fatalf("output past 32 MB is an error: %+v", got)
	}
}

// TestAMissingProgramSaysHowToInstallItAndIsCheckedAgain: a missing program with its install text refuses the run
// at once; once installed, the check button finds it without a new save of the manifest.
func TestAMissingProgramSaysHowToInstallItAndIsCheckedAgain(t *testing.T) {
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	e := harness.New(t)
	m := strings.Replace(harness.ScriptManifest("use", "", ""), `"runtime": "python"`,
		`"runtime": "python", "programs": ["sh", {"name": "deck-later-tool", "install": "brew install deck-later-tool"}]`, 1)
	e.Project("progs", m)
	e.SaveScript("progs", "scripts/main.py", "print('{\"out\": \"here\"}')\n")
	if code, out := e.Call("deck.project_create", map[string]any{"name": "blank", "manifest": strings.Replace(harness.ScriptManifest("use", "", ""),
		`"runtime": "python"`, `"runtime": "python", "programs": [""]`, 1)}); code != 200 {
		t.Fatalf("an empty program, which manifests took before, still loads: %d %s", code, out)
	}
	h := e.WaitHealth("command:progs.use", "error")
	if !strings.Contains(h.Error, "deck-later-tool") || !strings.Contains(h.Error, "brew install deck-later-tool") {
		t.Fatalf("the health names the missing program and how to install it: %+v", h)
	}
	if p := e.WaitState(e.Start("progs", "use", nil), "done", "error"); p.State != "error" || !strings.Contains(p.Error, "brew install deck-later-tool") {
		t.Fatalf("a run without its program is refused at once with the same line: %+v", p)
	}
	if err := os.WriteFile(filepath.Join(bin, "deck-later-tool"), []byte("#!/bin/sh\necho here\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if code, out := e.Press(e.RowAction("progs", "settings", "deck.programs_check", nil)); code != 200 {
		t.Fatalf("the check button of the health row of the command works: %d %s", code, out)
	}
	if h := e.WaitHealth("command:progs.use", ""); h.Status != "ok" {
		t.Fatalf("the check finds the program the owner installed: %+v", h)
	}
	if p := e.WaitState(e.Start("progs", "use", nil), "done", "error"); p.State != "done" || !strings.Contains(string(p.Result), "here") {
		t.Fatalf("the script runs with its program: %+v %s", p, p.Result)
	}
}

// TestAScriptMayRunForHours: a script, such as a render or a long download, takes a ceiling of up to four hours.
func TestAScriptMayRunForHours(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	e.Project("slow", strings.Replace(harness.ScriptManifest("wait", "", ""), `"runtime": "python"`, `"runtime": "python", "timeout_minutes": 180`, 1))
	e.SaveScript("slow", "scripts/main.py", "import sys, time\nprint('started', file=sys.stderr, flush=True)\ntime.sleep(120)\n")
	id := e.Start("slow", "wait", nil)
	e.Until("the script to start", func() bool { return len(log(e, id)) > 0 })
	e.Clock.Add(170 * time.Minute)
	e.Sweep()
	if p := e.Proc(id); p.State != "running" {
		t.Fatalf("a script runs until its ceiling of three hours: %+v", p)
	}
	e.Clock.Add(11 * time.Minute)
	e.Sweep()
	if p := e.WaitState(id, "stopped", "done", "error"); p.State != "error" || !strings.Contains(p.Error, "ceiling") {
		t.Fatalf("the ceiling stops the script: %+v", p)
	}
}

// TestTheProcessesOfARecordAreTheOnesItsIdWentInto: processes_list with a record gives the processes whose input
// field record names it, so a record page shows how its pipeline goes; its id in another field is not.
func TestTheProcessesOfARecordAreTheOnesItsIdWentInto(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("tools", harness.ScriptManifest("collect", "", ""))
	e.SaveScript("tools", "scripts/main.py", "print('{}')\n")
	var w struct{ IDs []int64 }
	e.Must("deck.records_upsert", map[string]any{"project": "tools", "collection": "items", "records": harness.Jsonl(map[string]any{"name": "a"}, map[string]any{"name": "b"})}, &w)
	mine := e.WaitState(e.Start("tools", "collect", map[string]any{"record": w.IDs[0]}), "done")
	e.WaitState(e.Start("tools", "collect", map[string]any{"record": w.IDs[1], "n": w.IDs[0]}), "done")
	e.WaitState(e.Start("tools", "collect", nil), "done")
	var ps []struct{ ID int64 }
	e.Must("deck.processes_list", map[string]any{"project": "tools", "record": w.IDs[0]}, &ps)
	if len(ps) != 1 || ps[0].ID != mine.ID {
		t.Fatalf("the processes of a record are the ones its id went into: %+v", ps)
	}
}

// TestAProjectChangedWhileAScriptRunsLeavesDeckUp: a script has a session of the gateway but no MCP server, so the
// renew of tools after a change of projects passes it by.
func TestAProjectChangedWhileAScriptRunsLeavesDeckUp(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("tools", harness.ScriptManifest("slow", "", ""))
	e.SaveScript("tools", "scripts/main.py", "import json, time\ntime.sleep(2)\nprint(json.dumps({}))\n")
	id := e.Start("tools", "slow", nil)
	// The script program is up; its process is running from the start, so the state does not show it.
	time.Sleep(500 * time.Millisecond)
	e.Project("other", harness.PlainManifest)
	if p := e.WaitState(id, "done", "error"); p.State != "done" {
		t.Fatalf("the script ends as it would: %+v", p)
	}
}

// TestAScriptCannotCallACommandOfAnotherProject: a script that names a command of another project through deck is
// refused, and that project starts nothing.
func TestAScriptCannotCallACommandOfAnotherProject(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("a", harness.ScriptManifest("collect", "", ""))
	e.Project("b", harness.ScriptManifest("collect", "", ""))
	e.SaveScript("a", "scripts/main.py", harness.PythonLib+`
st, out = deck("b.publish", {})
print(json.dumps({"status": st, "out": out}))
`)
	p := e.WaitState(e.Start("a", "collect", nil), "done", "error")
	var res struct {
		Status int
		Out    any
	}
	if err := json.Unmarshal(p.Result, &res); err != nil || res.Status != 403 || !strings.Contains(fmt.Sprint(res.Out), "another project") {
		t.Fatalf("a command of another project is refused: %+v %s", p, p.Result)
	}
	var ps []harness.Proc
	e.Must("deck.processes_list", map[string]any{"project": "b"}, &ps)
	if len(ps) != 0 {
		t.Fatalf("the other project starts nothing: %+v", ps)
	}
}

// TestAScriptThatFailsForItsOwnReasonGetsNoHintOfFolders: a script that fails for a reason of its own, such as a 422
// of deck or a missing file, has the end of its stderr, without a hint about folders the sandbox keeps closed.
func TestAScriptThatFailsForItsOwnReasonGetsNoHintOfFolders(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("tools", harness.ScriptManifest("collect", "", ""))
	e.SaveScript("tools", "scripts/main.py", harness.PythonLib+`
st, out = deck("deck.records_upsert", {"collection": "items", "records": [{"name": "a"}]})
sys.exit("deck answered %d: %s" % (st, out))
`)
	p := e.WaitState(e.Start("tools", "collect", nil), "done", "error")
	if p.State != "error" || !strings.Contains(p.Error, "deck answered 422") || strings.Contains(p.Error, "the settings of the module, Access") {
		t.Fatalf("the error is the script's own, without a hint of folders: %+v", p)
	}
	e.SaveScript("tools", "scripts/main.py", `import os
os.replace(os.path.join(os.environ["DECK_DATA"], "items.json.tmp"), os.path.join(os.environ["DECK_DATA"], "items.json"))
`)
	p = e.WaitState(e.Start("tools", "collect", nil), "done", "error")
	if p.State != "error" || !strings.Contains(p.Error, "No such file or directory") || strings.Contains(p.Error, "the settings of the module, Access") {
		t.Fatalf("a missing file of its own folder is the script's error, without a hint of folders: %+v", p)
	}
}

// TestScriptsRunOnOnePythonDeckPicksAndHealthNamesIt: every script runs on the python3 of 3.10 or newer deck picks,
// its folder first on PATH, so uv and a shebang find it too; health names the python and node scripts see.
func TestScriptsRunOnOnePythonDeckPicksAndHealthNamesIt(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	e.Project("tools", harness.ScriptManifest("collect", "", ""))
	e.SaveScript("tools", "scripts/main.py", `import json, os, shutil, sys
print(json.dumps({"version": list(sys.version_info[:2]), "first": os.environ["PATH"].split(":")[0], "which": shutil.which("python3")}))
`)
	p := e.WaitState(e.Start("tools", "collect", nil), "done", "error")
	var got struct {
		Version      []int
		First, Which string
	}
	if p.State != "done" || json.Unmarshal(p.Result, &got) != nil || len(got.Version) != 2 || got.Version[0] != 3 || got.Version[1] < 10 ||
		filepath.Dir(got.Which) != got.First {
		t.Fatalf("the script runs on python 3.10 or newer, the first python3 of its PATH: %+v %+v", p, got)
	}
	rows := e.Health()
	for _, r := range rows {
		if r.Subject == "scripts" {
			if r.Status != "ok" || !strings.Contains(r.Error, "python 3.") || !strings.Contains(r.Error, filepath.Join(got.First, "python3")) {
				t.Fatalf("health names the python of scripts: %+v", r)
			}
			return
		}
	}
	t.Fatalf("health has a row of the runtime of scripts: %+v", rows)
}
