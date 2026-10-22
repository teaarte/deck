package scenarios

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestScriptInstallsItsPackagesThroughTheProxyAndMissingProgramShowsInHealth(t *testing.T) {
	t.Parallel()
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
	if p := e.waitScript(e.start("closed", "use", nil), "done", "error"); p.State != "error" || !strings.Contains(p.Error, "403") ||
		!strings.Contains(p.Error, "deck: the proxy refused: ") || !strings.Contains(p.Error, "domains.read") {
		t.Fatalf("an index outside domains.read is refused, and the error says which and where it is added: %+v", p)
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

func TestTheProxyOfAScriptDropsHeadersThatAreOnlyForTheHop(t *testing.T) {
	t.Parallel()
	svc := newService(t, func(w http.ResponseWriter, r *http.Request, n int) {
		w.Header().Set("Keep-Alive", "timeout=5")
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
print(json.dumps({"down": "keep-alive: timeout" in got.decode().lower(), "ok": got.endswith(b"ok")}))
`)
	p := e.waitScript(e.start("hops", "go", nil), "done", "error")
	seen := svc.requests()
	if p.State != "done" || string(p.Result) != `{"down":false,"ok":true}` || len(seen) != 1 || seen[0].Header.Get("X-Up") != "" {
		t.Fatalf("headers of the hop go no further than the proxy, either way: %+v %+v", p, seen)
	}
}

// TestScriptWildcardDomainIsOnlyThePublicInternet: "*" does not open loopback, the local network or a tailnet
// to a script; a local service is reached by naming its host:port.
func TestScriptWildcardDomainIsOnlyThePublicInternet(t *testing.T) {
	t.Parallel()
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

// TestADomainIsAHostThatResolvesOnceToWhereItMayGo: a domain is a bare host; "*.x" is x, a public suffix or a URL
// is refused on save; a name that resolves to the owner's machine is refused, one that does not resolve is not reached.
func TestADomainIsAHostThatResolvesOnceToWhereItMayGo(t *testing.T) {
	t.Parallel()
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
	if got.Local != float64(403) || got.Nowhere != float64(502) || len(local.requests()) != 0 {
		t.Fatalf("a name of the owner's machine is refused, a name that does not resolve is a bad gateway: %s", p.Result)
	}
}

// TestANodeScriptFetchesThroughTheProxyOfDeck: fetch of Node goes through deck's proxy as Python's urllib does, so a
// host outside the domains of the project is deck's refusal, not a failed connection, and deck itself answers.
func TestANodeScriptFetchesThroughTheProxyOfDeck(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	e := newEnv(t, nil)
	e.createProject("tools", strings.Replace(scriptManifest("collect", "", ""), `"scripts/main.py", "runtime": "python"`, `"scripts/main.mjs", "runtime": "node"`, 1))
	e.saveScript("tools", "scripts/main.mjs", `const deck = await fetch(process.env.DECK_URL + "/commands/deck.records_list", {method: "POST",
  headers: {Authorization: "Bearer " + process.env.DECK_TOKEN, "Content-Type": "application/json"}, body: JSON.stringify({collection: "items"})});
let outside;
try { outside = (await fetch("http://example.com/")).status; } catch (e) { outside = String(e.cause?.cause?.message || e.cause?.code || e); }
console.log(JSON.stringify({deck: deck.status, outside}));
`)
	p := e.waitScript(e.start("tools", "collect", nil), "done", "error")
	if p.State != "done" || !strings.Contains(string(p.Result), `"deck":200`) || !strings.Contains(string(p.Result), `(403)`) {
		t.Fatalf("fetch of a node script reaches deck and asks deck's proxy for the rest: %+v %s", p, p.Result)
	}
}
