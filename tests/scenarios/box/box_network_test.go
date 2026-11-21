package box

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"deck2/tests/harness"
)

const reachScript = harness.PythonLib + `
inp = json.load(sys.stdin)
print(json.dumps({"checks": {u: fetch(u) for u in inp["use"].split()}}))
`

// TestAScriptAsksAtItsStartForTheHostsItsModuleDeclaredSinceEnabled: hosts a module declares for its scripts after it
// was enabled are asked for at the next run, all in one request; after yes the script reaches them.
func TestAScriptAsksAtItsStartForTheHostsItsModuleDeclaredSinceEnabled(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	old, b, c := harness.Served(t), harness.Served(t), harness.Served(t)
	host := func(u string) string { return strings.TrimPrefix(u, "http://") }
	rights := func(hosts ...string) string {
		q, _ := json.Marshal(hosts)
		return fmt.Sprintf(`, "rights": [{"right": "network", "what": %s, "why": "reads them"}]`, q)
	}
	e.Project("tools", harness.ScriptManifest("collect", "", rights(host(old.URL))))
	e.SaveScript("tools", "scripts/main.py", reachScript)
	e.Must("deck.manifest_save", map[string]any{"project": "tools", "manifest": harness.ScriptManifest("collect", "", rights(host(old.URL), host(b.URL), host(c.URL)))}, nil)

	use := strings.Join([]string{old.URL, b.URL, c.URL}, " ")
	id := e.Start("tools", "collect", map[string]any{"use": use})
	if p := e.WaitState(id, "done", "error", "waiting:right"); p.State != "waiting:right" {
		t.Fatalf("a run whose script needs hosts not granted waits for the owner: %+v", p)
	}
	asks := e.Asks()
	if len(asks) != 1 || !strings.Contains(asks[0].Text, "wants to start") || !strings.Contains(asks[0].Text, host(b.URL)) ||
		!strings.Contains(asks[0].Text, host(c.URL)) || strings.Contains(asks[0].Text, host(old.URL)) {
		t.Fatalf("one request names the hosts not granted: %+v", asks)
	}
	e.Answer(asks[0].ID, "allow")
	p := e.WaitState(id, "done", "error")
	var res struct{ Checks map[string]any }
	_ = json.Unmarshal(p.Result, &res)
	for _, u := range []string{old.URL, b.URL, c.URL} {
		if res.Checks[u] != float64(200) {
			t.Fatalf("after yes the script reaches every host: %+v", p)
		}
	}

	again := e.Start("tools", "collect", map[string]any{"use": use})
	if p := e.WaitState(again, "done", "error", "waiting:right"); p.State != "waiting:right" || len(e.Asks()) != 1 {
		t.Fatalf("a yes for one work is asked again at the next run: %+v %+v", p, e.Asks())
	}
	e.Answer(e.Asks()[0].ID, "deny")
	p = e.WaitState(again, "done", "error")
	_ = json.Unmarshal(p.Result, &res)
	if res.Checks[old.URL] != float64(200) || res.Checks[b.URL] != float64(403) || len(e.Asks()) != 0 {
		t.Fatalf("after no the script runs without those hosts: %+v", p)
	}
	if log := fmt.Sprint(e.Log(again)); !strings.Contains(log, "the proxy refused: "+host(b.URL)) {
		t.Fatalf("a refusal of the proxy is in the log of a script that ended well: %s", log)
	}
	third := e.Start("tools", "collect", map[string]any{"use": use})
	if p := e.WaitState(third, "done", "error", "waiting:right"); p.State != "done" || len(e.Asks()) != 0 {
		t.Fatalf("a no is not asked again: %+v %+v", p, e.Asks())
	}
}
