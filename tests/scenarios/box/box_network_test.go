package box

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"
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

// TestATriggeredScriptRunsWithoutTheHostsNotGrantedYet: a run of a trigger does not wait for the owner: it runs
// without the new hosts, says which, and one request of its module asks for them however many runs go.
func TestATriggeredScriptRunsWithoutTheHostsNotGrantedYet(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	old, b := harness.Served(t), harness.Served(t)
	host := func(u string) string { return strings.TrimPrefix(u, "http://") }
	use, _ := json.Marshal(old.URL + " " + b.URL)
	manifest := func(hosts ...string) string {
		q, _ := json.Marshal(hosts)
		return harness.ScriptManifest("collect", "", fmt.Sprintf(`, "rights": [{"right": "network", "what": %s, "why": "reads them"}],
  "triggers": [{"command": "collect", "cron": "0 9 * * *", "params": {"use": %s}}]`, q, use))
	}
	e.Project("tools", manifest(host(old.URL)))
	e.SaveScript("tools", "scripts/main.py", reachScript)
	e.Must("deck.manifest_save", map[string]any{"project": "tools", "manifest": manifest(host(old.URL), host(b.URL))}, nil)
	var ts []struct{ ID int64 }
	e.Must("deck.triggers_list", map[string]any{"project": "tools"}, &ts)
	for i := range 2 {
		var run struct{ Process int64 }
		e.Must("deck.trigger_run", map[string]any{"trigger": ts[0].ID}, &run)
		p := e.WaitState(run.Process, "done", "error", "waiting:right")
		var res struct{ Checks map[string]any }
		_ = json.Unmarshal(p.Result, &res)
		if p.State != "done" || res.Checks[old.URL] != float64(200) || res.Checks[b.URL] != float64(403) {
			t.Fatalf("run %d of a trigger goes without the hosts not granted: %+v", i, p)
		}
		if log := fmt.Sprint(e.Log(run.Process)); !strings.Contains(log, "not granted yet: "+host(b.URL)) {
			t.Fatalf("the run says the hosts it went without: %s", log)
		}
	}
	if asks := e.Asks(); len(asks) != 1 || !strings.Contains(asks[0].Text, host(b.URL)) || asks[0].Process != 0 {
		t.Fatalf("one request of the module asks for the hosts: %+v", asks)
	}
}

// TestRunsOfTriggersAtOnceLeaveOneRequest: runs of two triggers at once leave one request of their module.
func TestRunsOfTriggersAtOnceLeaveOneRequest(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	old, b := harness.Served(t), harness.Served(t)
	host := func(u string) string { return strings.TrimPrefix(u, "http://") }
	manifest := func(hosts ...string) string {
		q, _ := json.Marshal(hosts)
		var triggers []string
		for i := range 6 {
			triggers = append(triggers, fmt.Sprintf(`{"command": "collect", "cron": "%d 9 * * *", "params": {"use": %q}}`, i, old.URL+" "+b.URL))
		}
		return harness.ScriptManifest("collect", "", fmt.Sprintf(`, "rights": [{"right": "network", "what": %s, "why": "reads them"}],
  "triggers": [%s]`, q, strings.Join(triggers, ", ")))
	}
	e.Project("tools", manifest(host(old.URL)))
	e.SaveScript("tools", "scripts/main.py", reachScript)
	e.Must("deck.manifest_save", map[string]any{"project": "tools", "manifest": manifest(host(old.URL), host(b.URL))}, nil)
	var ts []struct{ ID int64 }
	e.Must("deck.triggers_list", map[string]any{"project": "tools"}, &ts)
	var wg sync.WaitGroup
	ids := make([]int64, len(ts))
	for i, tr := range ts {
		wg.Go(func() {
			var run struct{ Process int64 }
			e.Must("deck.trigger_run", map[string]any{"trigger": tr.ID}, &run)
			ids[i] = run.Process
		})
	}
	wg.Wait()
	for _, id := range ids {
		e.WaitState(id, "done")
	}
	if asks := e.Asks(); len(asks) != 1 {
		t.Fatalf("runs at once leave one request: %+v", asks)
	}
}

// TestAModuleRequestForHostsClosesOnceTheyAreAnswered: the request a run of a trigger left closes once the owner
// answers its hosts in the rights of the module, and a no to it keeps a yes given since.
func TestAModuleRequestForHostsClosesOnceTheyAreAnswered(t *testing.T) {
	t.Parallel()
	for _, way := range []string{"yes", "no after a yes", "no in rights"} {
		late, refused := way == "no after a yes", way == "no in rights"
		t.Run(way, func(t *testing.T) {
			t.Parallel()
			e := harness.New(t)
			old, b, c := harness.Served(t), harness.Served(t), harness.Served(t)
			host := func(u string) string { return strings.TrimPrefix(u, "http://") }
			use, _ := json.Marshal(old.URL + " " + b.URL + " " + c.URL)
			manifest := func(hosts ...string) string {
				q, _ := json.Marshal(hosts)
				return harness.ScriptManifest("collect", "", fmt.Sprintf(`, "rights": [{"right": "network", "what": %s, "why": "reads them"}],
  "triggers": [{"command": "collect", "cron": "0 9 * * *", "params": {"use": %s}}]`, q, use))
			}
			e.Project("tools", manifest(host(old.URL)))
			e.SaveScript("tools", "scripts/main.py", reachScript)
			e.Must("deck.manifest_save", map[string]any{"project": "tools", "manifest": manifest(host(old.URL), host(b.URL), host(c.URL))}, nil)
			var ts []struct{ ID int64 }
			e.Must("deck.triggers_list", map[string]any{"project": "tools"}, &ts)
			var run struct{ Process int64 }
			e.Must("deck.trigger_run", map[string]any{"trigger": ts[0].ID}, &run)
			e.WaitState(run.Process, "done")
			asks := e.Asks()
			if len(asks) != 1 {
				t.Fatalf("one request of the module: %+v", asks)
			}
			if refused {
				for _, h := range []string{b.URL, c.URL} {
					e.Must("deck.right_set", map[string]any{"project": "tools", "right": "network", "subject": host(h), "answer": "no"}, nil)
				}
				e.Until("the request to close", func() bool { return len(e.Asks()) == 0 })
				var calls []struct{ Outcome, Subject, Error string }
				e.Must("deck.calls_list", map[string]any{"project": "tools"}, &calls)
				for _, c := range calls {
					if c.Subject == host(b.URL) && c.Outcome != "waiting" && (c.Outcome != "rejected" || strings.Contains(c.Error, "in force")) {
						t.Fatalf("a request whose hosts the owner refused closes refused: %+v", calls)
					}
				}
				return
			}
			e.Must("deck.right_set", map[string]any{"project": "tools", "right": "network", "subject": host(b.URL), "answer": "always"}, nil)
			if !late {
				e.Must("deck.right_set", map[string]any{"project": "tools", "right": "network", "subject": host(c.URL), "answer": "always"}, nil)
				e.Until("the request to close", func() bool { return len(e.Asks()) == 0 })
				return
			}
			e.Answer(asks[0].ID, "deny")
			var rows []struct{ Right, Subject, Answer string }
			e.Must("deck.rights_list", map[string]any{"project": "tools"}, &rows)
			got := map[string]string{}
			for _, r := range rows {
				got[r.Subject] = r.Answer
			}
			if got[host(b.URL)] != "always" || got[host(c.URL)] != "no" {
				t.Fatalf("a no to the request keeps the yes given since: %v", got)
			}
		})
	}
}
