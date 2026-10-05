package scenarios

import (
	"archive/tar"
	"compress/gzip"
	"context"
	deckhome "deck2/internal/home"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"deck2/internal/registry"
)

func served(t *testing.T) *httptest.Server {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte("hello")) }))
	t.Cleanup(s.Close)
	return s
}

const collectScript = pythonLib + `
inp = json.load(sys.stdin)
checks = {}
st, out = deck("deck.records_upsert", {"collection": "items", "records": json.dumps({"name": "a", "size": inp["n"]})})
checks["internal_call"] = st == 200 and out["written"] == 1
rid = out["ids"][0] if st == 200 else 0
st, out = deck(inp["project"] + ".publish", {})
checks["external_waits"] = st == 202 and out["waiting"] > 0
checks["allowed_domain"] = fetch(inp["allowed"]) == 200
checks["other_domain_refused"] = fetch(inp["other"]) == 403
checks["direct_network_blocked"] = fetch(inp["allowed"], proxy=False) != 200
checks["proxy_needs_token"] = fetch(inp["allowed"], proxy=os.environ["HTTP_PROXY"].replace("deck:" + TOKEN + "@", "")) == 407
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
subprocess.Popen(["sh", "-c", "sleep 1; echo late > \"$DECK_DATA/late\"; sleep 60"])
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
	e := newEnv(t, nil)
	allowed, other := served(t), served(t)
	host := strings.TrimPrefix(allowed.URL, "http://")
	pid := e.createProject("tools", scriptManifest("collect", "", fmt.Sprintf(`, "domains": {"read": [%q]}`, host)))
	e.saveScript("tools", "scripts/main.py", collectScript)
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
	id := e.start("tools", "collect", map[string]any{"n": 7, "project": "tools", "allowed": allowed.URL, "other": other.URL,
		"outside": outside, "secret": secret, "key": key, "late": late, "socket": sock.Addr().String()})
	time.Sleep(500 * time.Millisecond)
	_ = os.MkdirAll(filepath.Dir(late), 0o700)
	_ = os.WriteFile(late, []byte("aws-secret"), 0o600)
	began := time.Now()
	p := e.waitScript(id, "done", "error")
	if p.State != "done" || p.NotBySchema || time.Since(began) > 20*time.Second {
		t.Fatalf("the script command finishes by its schema, not waiting for programs it started: %+v", p)
	}
	var res struct {
		Checks map[string]bool `json:"checks"`
		Files  []struct{ Record int64 }
	}
	_ = json.Unmarshal(p.Result, &res)
	for _, c := range []string{"internal_call", "external_waits", "allowed_domain", "other_domain_refused", "direct_network_blocked",
		"outside_write_blocked", "secret_hidden", "owner_key_hidden", "late_key_hidden", "host_socket_blocked"} {
		if !res.Checks[c] {
			t.Errorf("check %s failed: %s", c, p.Result)
		}
	}
	if _, err := os.Stat(filepath.Join(outside, "x")); err == nil {
		t.Fatal("the script wrote outside its folders")
	}
	if a := e.waitAsk(); a.Kind != "call" || !strings.Contains(a.Text, ".publish") {
		t.Fatalf("the frozen external call of a script stays for the owner after the script ends: %+v", a)
	}
	var files []struct {
		ID        int64
		Name      string
		OwnerKind string `json:"owner_kind"`
	}
	e.must("deck.files_list", map[string]any{"record": res.Files[0].Record}, &files)
	if len(files) != 1 || files[0].Name != "report.txt" || files[0].OwnerKind != "record" {
		t.Fatalf("the result file is bound to the record: %+v", files)
	}
	if b, _ := os.ReadFile(filepath.Join(deckhome.Layout(home).State(), "files", fmt.Sprint(files[0].ID))); string(b) != "report 7" {
		t.Fatalf("the file moved into files/: %q", b)
	}
	if left, _ := os.ReadDir(filepath.Join(deckhome.Layout(home).Work(), "runs", fmt.Sprint(id))); len(left) != 1 || left[0].Name() != "stream" {
		t.Fatalf("a finished script leaves only its log in its run folder: %v", left)
	}
	if b, _ := json.Marshal(e.log(id)); strings.Contains(string(b), "xcodebuild") || strings.Contains(string(b), "DVTDeveloperPaths") {
		t.Fatalf("python runs as the interpreter itself, not the xcrun launcher: %s", b)
	}
	page := e.screenData("", "process", map[string]any{"record": id})["b0"]["value"]
	if b, _ := json.Marshal(page); strings.Contains(string(b), `"Error"`) {
		t.Fatalf("a process without an error shows no Error field: %s", b)
	}
	time.Sleep(2 * time.Second)
	if _, err := os.Stat(filepath.Join(deckhome.Layout(home).Work(), "projects", fmt.Sprint(pid), "data", "late")); err == nil {
		t.Fatal("a program the script started outlived the run")
	}
}

const indexScript = pythonLib + `
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
	e := newEnv(t, nil)
	pid := e.createProject("index", scriptManifest("build", "", ""))
	e.saveScript("index", "scripts/main.py", indexScript)
	folder := t.TempDir()
	for want := range 2 {
		p := e.waitScript(e.start("index", "build", map[string]any{"outside": folder}), "done", "error")
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
	e.must("deck.snapshot_create", nil, &snap)
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

func TestScriptInstallsItsPackagesThroughTheProxyAndMissingProgramShowsInHealth(t *testing.T) {
	if _, err := exec.LookPath("uv"); err != nil {
		t.Skip("uv is not installed")
	}
	whl := wheel(t, "deckfixture")
	var mu sync.Mutex
	hits := map[string]int{}
	index := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits[r.URL.Path]++
		mu.Unlock()
		switch r.URL.Path {
		case "/simple/deckfixture/":
			fmt.Fprintf(w, `<a href="/files/%s">%s</a>`, filepath.Base(whl), filepath.Base(whl))
		case "/files/" + filepath.Base(whl):
			http.ServeFile(w, r, whl)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(index.Close)
	host := strings.TrimPrefix(index.URL, "http://")
	src := fmt.Sprintf(packageScript, index.URL)
	manifest := func(extra string) string {
		m := strings.Replace(scriptManifest("use", "", extra), `"scripts/main.py", "runtime": "python"`,
			`"scripts/main.py", "runtime": "exec", "programs": ["uv"]`, 1)
		return m
	}
	e := newEnv(t, nil)
	e.createProject("pkgs", manifest(fmt.Sprintf(`, "domains": {"read": [%q]}`, host)))
	e.saveScript("pkgs", "scripts/main.py", src)
	var downloads []int
	for i := range 2 {
		p := e.waitScript(e.start("pkgs", "use", nil), "done", "error")
		if p.State != "done" || !strings.Contains(string(p.Result), "42") {
			t.Fatalf("run %d: the script installs its package from the index in domains.read: %+v", i, p)
		}
		mu.Lock()
		downloads = append(downloads, hits["/files/"+filepath.Base(whl)])
		mu.Unlock()
	}
	if downloads[0] == 0 || downloads[1] != downloads[0] {
		t.Fatalf("the second run takes the package from the cache in the data folder, downloads: %v", downloads)
	}

	e.createProject("closed", manifest(""))
	e.saveScript("closed", "scripts/main.py", src)
	mu.Lock()
	clear(hits)
	mu.Unlock()
	if p := e.waitScript(e.start("closed", "use", nil), "done", "error"); p.State != "error" || !strings.Contains(p.Error, "403") {
		t.Fatalf("an index outside domains.read is refused: %+v", p)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(hits) != 0 {
		t.Fatalf("nothing reaches an index outside domains.read: %v", hits)
	}

	m := strings.Replace(scriptManifest("use", "", ""), `"runtime": "python"`, `"runtime": "python", "programs": ["deck-no-such-program"]`, 1)
	e.createProject("progs", m)
	e.saveScript("progs", "scripts/main.py", "print('null')\n")
	h := e.waitHealth("command:progs.use", "error")
	if !strings.Contains(h.Error, "deck-no-such-program") {
		t.Fatalf("the health names the missing program: %+v", h)
	}
	if p := e.waitScript(e.start("progs", "use", nil), "done", "error"); p.State != "error" || !strings.Contains(p.Error, "deck-no-such-program") {
		t.Fatalf("a script without its program does not run: %+v", p)
	}
}

func TestManifestWithScriptPackagesIsRefusedWithAHint(t *testing.T) {
	e := newEnv(t, nil)
	m := strings.Replace(scriptManifest("use", "", ""), `"runtime": "python"`, `"runtime": "python", "packages": ["requests"]`, 1)
	code, out := e.call("deck.project_create", map[string]any{"name": "old", "manifest": m})
	if code != 422 || !strings.Contains(string(out), "PEP 723") || !strings.Contains(string(out), "domains.read") {
		t.Fatalf("a manifest with packages is refused with the way scripts install them: %d %s", code, out)
	}
}

func TestScriptCeilingAndThreeFailuresInARow(t *testing.T) {
	e := newClockEnv(t, nil)
	e.createProject("slow", scriptManifest("wait", "", ""))
	e.saveScript("slow", "scripts/main.py", "import sys, time\nprint('started', file=sys.stderr, flush=True)\ntime.sleep(120)\n")
	id := e.start("slow", "wait", nil)
	e.waitFor("the script to start", func() bool { return len(e.log(id)) > 0 })
	e.Clock.Add(9 * time.Minute)
	e.sweep()
	if p := e.process(id); p.State != "running" {
		t.Fatalf("a script runs until its ceiling of 10 minutes: %+v", p)
	}
	e.Clock.Add(2 * time.Minute)
	e.sweep()
	if p := e.waitScript(id, "stopped", "done", "error"); p.State != "stopped" || !strings.Contains(p.Error, "ceiling") {
		t.Fatalf("the ceiling stops the script: %+v", p)
	}

	e.createProject("fails", scriptManifest("break", "", ""))
	e.saveScript("fails", "scripts/main.py", "import sys\nprint('boom', file=sys.stderr)\nsys.exit(3)\n")
	for i := 1; i <= 4; i++ {
		p := e.waitScript(e.start("fails", "break", nil), "error", "done")
		if p.State != "error" || !strings.Contains(p.Error, "boom") {
			t.Fatalf("a failing script is an error with its stderr: %+v", p)
		}
		want := 0
		if i >= 3 {
			want = 1
			e.waitFor("the warning", func() bool { return e.warnings("failed 3 times in a row") > 0 })
		}
		if n := e.warnings("failed 3 times in a row"); n != want {
			t.Fatalf("after failure %d: %d warnings, want %d", i, n, want)
		}
	}
}

func TestScriptInterruptedByARestartIsNotResumed(t *testing.T) {
	home := t.TempDir()
	e1 := newEnvWith(t, home, &Fake{})
	e1.createProject("restart", scriptManifest("wait", "", ""))
	e1.saveScript("restart", "scripts/main.py", "import sys, time\nprint('started', file=sys.stderr, flush=True)\ntime.sleep(120)\n")
	id := e1.start("restart", "wait", nil)
	e1.waitFor("the script to start", func() bool { return len(e1.log(id)) > 0 })
	_ = e1.App.Close()
	e2 := newEnvWith(t, home, &Fake{})
	if err := e2.App.Runner.ResumeInterrupted(t.Context()); err != nil {
		t.Fatal(err)
	}
	if p := e2.process(id); p.State != "interrupted" {
		t.Fatalf("a script cut by a restart is interrupted: %+v", p)
	}
	if e2.warnings("interrupted by a restart") != 1 {
		t.Fatal("the owner is told to run it again")
	}
}

func TestResultFilesStayInsideTheResultsFolder(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("escape", scriptManifest("take", "", ""))
	outside := t.TempDir()
	victim := filepath.Join(outside, "victim.txt")
	_ = os.WriteFile(victim, []byte("mine"), 0o600)
	for _, path := range []string{"link/victim.txt", "../../../" + victim, "direct"} {
		e.saveScript("escape", "scripts/main.py", fmt.Sprintf(`import json, os
res = os.environ["DECK_RESULTS"]
os.symlink(%q, os.path.join(res, "link"))
os.symlink(%q, os.path.join(res, "direct"))
print(json.dumps({"files": [{"path": %q}]}))
`, outside, victim, path))
		p := e.waitScript(e.start("escape", "take", nil), "done", "error")
		if p.State != "error" || !strings.Contains(p.Error, "not a file in the results folder") {
			t.Fatalf("%s: a result file outside the results folder is refused: %+v", path, p)
		}
		if b, err := os.ReadFile(victim); err != nil || string(b) != "mine" {
			t.Fatalf("%s: the file outside stays where it was: %v", path, err)
		}
	}
}

func TestResultFilesAreKeptWholeOrNotAtAll(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("twice", scriptManifest("take", "", ""))
	e.saveScript("twice", "scripts/main.py", `import json, os
open(os.path.join(os.environ["DECK_RESULTS"], "a.txt"), "w").write("a")
print(json.dumps({"files": [{"path": "a.txt"}, {"path": "./a.txt"}]}))
`)
	id := e.start("twice", "take", nil)
	if p := e.waitScript(id, "done", "error"); p.State != "error" || !strings.Contains(p.Error, "named twice") {
		t.Fatalf("a file named twice is refused: %+v", p)
	}
	var files []struct{ ID int64 }
	e.must("deck.files_list", map[string]any{"process": id}, &files)
	if len(files) != 0 {
		t.Fatalf("a refused result keeps no file rows: %+v", files)
	}
}

func TestAChattyScriptDoesNotWriteTheDatabaseForEachLine(t *testing.T) {
	e := newClockEnv(t, nil)
	e.createProject("chatty", scriptManifest("talk", "", ""))
	e.saveScript("chatty", "scripts/main.py", "import sys\nfor i in range(3000):\n    print('line', i, file=sys.stderr, flush=True)\nprint('null')\n")
	changes := func() int {
		var n int
		if err := e.App.Store.DB.QueryRow(`SELECT total_changes()`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := changes()
	if p := e.waitScript(e.start("chatty", "talk", nil), "done", "error"); p.State != "done" {
		t.Fatalf("the script finishes: %+v", p)
	}
	if n := changes() - before; n > 300 {
		t.Fatalf("3000 lines of stderr are one output mark a second, not a write each: %d rows changed", n)
	}
}

func TestTheProxyOfAScriptDropsHeadersThatAreOnlyForTheHop(t *testing.T) {
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		w.Header().Set("Connection", "X-Down")
		w.Header().Set("X-Down", "hop")
		_, _ = w.Write([]byte("ok"))
	})
	host := strings.TrimPrefix(svc.URL, "http://")
	e := newEnv(t, nil)
	e.createProject("hops", scriptManifest("go", "", fmt.Sprintf(`, "domains": {"read": [%q]}`, host)))
	e.saveScript("hops", "scripts/main.py", `import base64, json, os, socket, urllib.parse
p = urllib.parse.urlparse(os.environ["HTTP_PROXY"])
auth = base64.b64encode((p.username + ":" + p.password).encode()).decode()
s = socket.create_connection((p.hostname, p.port))
s.sendall(("GET `+svc.URL+`/x HTTP/1.1\r\nHost: `+host+`\r\nProxy-Authorization: Basic " + auth +
    "\r\nConnection: close, X-Up\r\nX-Up: hop\r\n\r\n").encode())
got = b""
while True:
    b = s.recv(65536)
    if not b:
        break
    got += b
print(json.dumps({"down": "x-down" in got.decode().lower(), "ok": got.endswith(b"ok")}))
`)
	p := e.waitScript(e.start("hops", "go", nil), "done", "error")
	seen := svc.requests()
	if p.State != "done" || string(p.Result) != `{"down":false,"ok":true}` || len(seen) != 1 || seen[0].Header.Get("X-Up") != "" {
		t.Fatalf("headers named in Connection go no further than the proxy, either way: %+v %+v", p, seen)
	}
}

// TestScriptWildcardDomainIsOnlyThePublicInternet: "*" does not open loopback, the local network or a tailnet
// to a script; a local service is reached by naming its host:port.
func TestScriptWildcardDomainIsOnlyThePublicInternet(t *testing.T) {
	e := newEnv(t, nil)
	named, local := served(t), served(t)
	host := strings.TrimPrefix(named.URL, "http://")
	e.createProject("tools", scriptManifest("collect", "", fmt.Sprintf(`, "domains": {"read": ["*", %q]}`, host)))
	e.saveScript("tools", "scripts/main.py", pythonLib+`
inp = json.load(sys.stdin)
print(json.dumps({"named": fetch(inp["allowed"]), "local": fetch(inp["other"]), "tailnet": fetch("http://100.100.100.100/")}))
`)
	p := e.waitScript(e.start("tools", "collect", map[string]any{"allowed": named.URL, "other": local.URL}), "done", "error")
	var got struct{ Named, Local, Tailnet any }
	_ = json.Unmarshal(p.Result, &got)
	if p.State != "done" || got.Named != float64(200) || got.Local != float64(403) || got.Tailnet != float64(403) {
		t.Fatalf("a named host:port is reached, a local or tailnet address by \"*\" is not: %+v %s", p, p.Result)
	}
}

// TestAScriptReadsNoFolderOfAnotherProject: the data of another project, its runs and deck's own files stay closed
// to a script, which reads and writes only its own.
func TestAScriptReadsNoFolderOfAnotherProject(t *testing.T) {
	e := newEnv(t, nil)
	e.createProject("other", scriptManifest("collect", "", ""))
	b := e.createProject("tools", scriptManifest("collect", "", ""))
	theirs := filepath.Join(deckhome.Layout(e.App.Cfg.Home).Work(), "projects", fmt.Sprint(b-1), "data", "secret.txt")
	if err := os.MkdirAll(filepath.Dir(theirs), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(theirs, []byte("theirs"), 0o600); err != nil {
		t.Fatal(err)
	}
	e.saveScript("tools", "scripts/main.py", pythonLib+`
inp = json.load(sys.stdin)
print(json.dumps({"read": can_read(inp["other"]), "write": can_write(os.path.join(os.path.dirname(inp["other"]), "x")),
  "db": can_read(inp["outside"]), "own": can_write(os.path.join(os.environ["DECK_DATA"], "x"))}))
`)
	p := e.waitScript(e.start("tools", "collect", map[string]any{"other": theirs, "outside": filepath.Join(deckhome.Layout(e.App.Cfg.Home).State(), "deck.db")}), "done", "error")
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

// TestADomainIsAHostThatResolvesOnceToWhereItMayGo: a domain is a bare host; "*.x" is x, a public suffix or a URL
// is refused on save; a name that resolves to the owner's machine or does not resolve is not reached.
func TestADomainIsAHostThatResolvesOnceToWhereItMayGo(t *testing.T) {
	e := newEnv(t, nil)
	for domain, says := range map[string]string{"https://hf.co/models": "a host", "co.uk": "public suffix", "com": "public suffix"} {
		code, out := e.call("deck.project_create", map[string]any{"name": "bad", "manifest": scriptManifest("collect", "", fmt.Sprintf(`, "domains": {"read": [%q]}`, domain))})
		if code != 422 || !strings.Contains(string(out), says) {
			t.Errorf("%s is refused on save naming %q: %d %s", domain, says, code, out)
		}
	}
	_, tok := e.token("terminal", "deck.*")
	e.callWith(tok, "deck.project_create", map[string]any{"name": "masks", "manifest": scriptManifest("collect", "", `, "domains": {"read": ["*.hf.co"]}`)})
	if r := e.project("masks").Rights; len(r) != 1 || r[0] != "read domain: hf.co" {
		t.Fatalf("*.hf.co is hf.co with its subdomains: %v", r)
	}

	local := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { _, _ = w.Write([]byte("hello")) })
	_, port, _ := net.SplitHostPort(strings.TrimPrefix(local.URL, "http://"))
	e.createProject("tools", scriptManifest("collect", "", fmt.Sprintf(`, "domains": {"read": ["localhost:%s", "nowhere.invalid"]}`, port)))
	e.saveScript("tools", "scripts/main.py", pythonLib+`
inp = json.load(sys.stdin)
print(json.dumps({"local": fetch(inp["allowed"]), "nowhere": fetch("http://nowhere.invalid/")}))
`)
	p := e.waitScript(e.start("tools", "collect", map[string]any{"allowed": "http://localhost:" + port + "/"}), "done", "error")
	var got struct{ Local, Nowhere any }
	_ = json.Unmarshal(p.Result, &got)
	if got.Local != float64(403) || got.Nowhere != float64(403) || len(local.requests()) != 0 {
		t.Fatalf("a name of the owner's machine and a name that does not resolve are refused: %s", p.Result)
	}
}

const homeScript = pythonLib + `
import subprocess
inp = json.load(sys.stdin)
home = os.path.expanduser(inp["home"])
tool = subprocess.run(["tool"], capture_output=True, text=True)
try:
    listed = len(os.listdir(home)) > 0
except OSError:
    listed = False
print(json.dumps({"rc": can_read(os.path.join(home, ".zshrc")), "documents": can_read(os.path.join(home, "Documents", "notes.txt")),
  "config": can_read(os.path.join(home, ".config", "app", "token")), "listed": listed,
  "runtime": tool.stdout.strip(), "folder": can_read(os.path.join(inp["folder"], "readme")),
  "folder_write": can_write(os.path.join(inp["folder"], "x")), "cpus": os.cpu_count()}))
`

// TestTheSandboxClosesTheHomeButForRuntimesAndTheFoldersOfTheProject: a script and an MCP server read nothing of the
// owner's home folder but the runtimes deck finds on PATH and the folders the owner gave the project, read-only.
func TestTheSandboxClosesTheHomeButForRuntimesAndTheFoldersOfTheProject(t *testing.T) {
	owner := ownerHome(t)
	for path, text := range map[string]string{".zshrc": "export OPENAI_API_KEY=sk-owner", "Documents/notes.txt": "private",
		".config/app/token": "t0ken", "rt/lib/tool/data": "runtime-data", "code/readme": "the code",
		"rt/bin/tool": "#!/bin/sh\ncat \"$(dirname \"$0\")/../lib/tool/data\"\n"} {
		p := filepath.Join(owner, path)
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", owner)
	t.Setenv("PATH", filepath.Join(owner, "rt", "bin")+string(os.PathListSeparator)+os.Getenv("PATH"))
	e := newEnv(t, nil)
	self, _ := os.Executable()
	e.createProject("tools", scriptManifest("collect", "", fmt.Sprintf(`, "tools": {"mcp": {"servers": {"fake": {"command": %q,
    "env": {"DECK_FAKE_MCP": "project"}, "toolFilter": ["peek"], "classes": {"peek": "internal"}}}}}`, self)))
	e.must("deck.folder_add", map[string]any{"project": "tools", "path": filepath.Join(owner, "code")}, nil)
	e.saveScript("tools", "scripts/main.py", homeScript)
	p := e.waitScript(e.start("tools", "collect", map[string]any{"home": owner, "folder": filepath.Join(owner, "code")}), "done", "error")
	var got struct {
		RC, Documents, Config, Listed, Folder, FolderWrite bool
		Runtime                                            string
		CPUs                                               int
	}
	_ = json.Unmarshal(p.Result, &got)
	if p.State != "done" || got.RC || got.Documents || got.Config || got.Listed || got.Runtime != "runtime-data" || !got.Folder || got.FolderWrite || got.CPUs < 1 {
		t.Fatalf("a script reads of the home only the runtimes on PATH and the folders of its project: %+v %s", p, p.Result)
	}
	for path, want := range map[string]string{filepath.Join(owner, ".zshrc"): "read=false", filepath.Join(owner, "code", "readme"): "read=true"} {
		out, _ := e.App.Gateway.CallAs(context.Background(), registry.Actor{Kind: registry.Agent, Workspace: 1, ProjectID: e.projectID("tools")},
			"tools.fake.peek", json.RawMessage(fmt.Sprintf(`{"path": %q}`, path)))
		if b, _ := json.Marshal(out); !strings.Contains(string(b), want) {
			t.Fatalf("an MCP server reads of the home only what a script does: reading %s gave %s", path, b)
		}
	}
}

const processesScript = pythonLib + `
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
	e := newEnv(t, nil)
	e.createProject("tools", scriptManifest("collect", "", ""))
	e.saveScript("tools", "scripts/main.py", processesScript)
	p := e.waitScript(e.start("tools", "collect", map[string]any{"n": os.Getpid()}), "done", "error")
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
