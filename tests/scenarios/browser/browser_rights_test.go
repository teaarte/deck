package browser

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// TestRealBrowserRightsByModuleAndByRightRevokeAtOneTap: Rights lists the rights under the module and under the right;
// one tap revokes and the next call asks; the report counts what a module did; on a desk and a phone.
func TestRealBrowserRightsByModuleAndByRightRevokeAtOneTap(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{}`) })
	host := strings.TrimPrefix(svc.URL, "http://")
	e := harness.New(t)
	_, tok := e.Token("terminal", "deck.*", "notes.*")
	e.Project("notes", harness.PostManifest(svc.URL, ""))
	e.Secret("notes", "svc", "k")
	e.Project("reach", harness.ScriptManifest("go", "", fmt.Sprintf(`, "rights": [{"right": "network", "what": [%q], "why": "reads it"}]`, host)))
	e.SaveScript("reach", "scripts/main.py", harness.PythonLib+`
print(json.dumps({"got": [fetch("`+svc.URL+`/a"), fetch("`+svc.URL+`/b")]}))
`)
	e.WaitState(e.Start("reach", "go", nil), "done")

	item := func(group, title string) string {
		return `[...document.querySelectorAll('[data-group="` + group + `"] [data-item]')].find((r) => r.innerText.includes('` + title + `'))`
	}
	b := Open(t)
	for _, size := range []struct {
		name string
		w, h int
	}{{"desktop", 1440, 900}, {"390", 390, 844}} {
		e.Must("deck.right_set", map[string]any{"project": "notes", "right": "send", "subject": "post", "answer": "always"}, nil)
		b.call("browser_resize", map[string]any{"width": size.w, "height": size.h})
		b.Open(e, "#/deck/settings?tab=Rights")
		row := item("notes", "Sending in your name")
		b.until(size.name+": the rights of notes under its name, the answer and its revoke", row+` && `+row+`.innerText.includes('always') && `+
			`[...`+row+`.querySelectorAll('button')].some((x) => x.innerText.includes('Revoke'))`)
		b.until(size.name+": the network of reach, its host and when it was used", item("reach", "Network")+`?.innerText.includes('`+host+`')`)
		b.shot("t9-rights-module-" + size.name)
		if got := b.eval(`String(document.documentElement.scrollWidth <= window.innerWidth)`); got != "true" {
			t.Fatalf("%s: Rights does not scroll sideways", size.name)
		}
		b.eval(`([...` + row + `.querySelectorAll('button')].find((x) => x.innerText.includes('Revoke')).click(), '')`)
		e.Until(size.name+": one tap revokes", func() bool {
			var rows []struct{ Right, Answer string }
			e.Must("deck.rights_list", map[string]any{"project": "notes"}, &rows)
			return len(rows) == 1 && rows[0].Answer == ""
		})
		b.until(size.name+": the row says it asks again", item("notes", "Sending in your name")+`?.innerText.includes('asks you')`)
		if code, out := e.As(tok).Call("notes.post", map[string]any{"id": size.name, "text": "hi"}); code != 202 && !strings.Contains(string(out), "waiting") {
			t.Fatalf("%s: after a revoke the next call asks: %d %s", size.name, code, out)
		}
		for _, a := range e.Asks() {
			e.Answer(a.ID, "deny")
		}

		b.eval(`([...document.querySelectorAll('button')].find((x) => x.innerText.trim() === 'By right').click(), '')`)
		b.until(size.name+": the rights under each right, the modules in them", item("Network", "reach")+` && `+item("Sending in your name", "notes"))
		b.shot("t9-rights-right-" + size.name)

		b.Open(e, "#/deck/settings?tab=Rights")
		report := `[...document.querySelectorAll('[data-group="reach"] button')].find((x) => x.innerText.includes('Report'))`
		b.until(size.name+": the report of a module on its row", report)
		b.eval(`(` + report + `.click(), '')`)
		b.until(size.name+": the report of the module, opened from its row", `location.hash.includes('rights-report') && document.body.innerText.includes('reach')`)
		b.Open(e, "#/deck/rights-report?project=reach&period=Today")
		b.until(size.name+": the report counts the requests to the host", `[...document.querySelectorAll('[data-stat-block]')].find((x) => x.dataset.title === 'Requests')?.querySelector('[data-stat]')?.innerText.trim() === '2'
			&& [...document.querySelectorAll('[data-item]')].some((r) => r.innerText.includes('`+host+`') && r.innerText.includes('requests: 2'))`)
		b.shot("t9-report-" + size.name)
	}
}

// TestRealBrowserARunWithTheNetworkWearsItsHost: a run whose box holds the network wears its host beside its name in
// what runs, Today says it, and both go with the run; on a desk and a phone.
func TestRealBrowserARunWithTheNetworkWearsItsHost(t *testing.T) {
	harness.Needs(t, "DECK_REAL_BROWSER")
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{}`) })
	host := strings.TrimPrefix(svc.URL, "http://")
	e := harness.New(t)
	e.Project("slow", harness.ScriptManifest("go", "", fmt.Sprintf(`, "rights": [{"right": "network", "what": [%q], "why": "reads it"}]`, host)))
	e.SaveScript("slow", "scripts/main.py", "import time\ntime.sleep(300)\n")
	b := Open(t)
	for _, size := range []struct {
		name string
		w, h int
	}{{"desktop", 1440, 900}, {"390", 390, 844}} {
		b.call("browser_resize", map[string]any{"width": size.w, "height": size.h})
		p := e.Start("slow", "go", nil)
		b.Open(e, "#/deck/cockpit")
		b.until(size.name+": the run wears its host, and the head of Today says it", `document.querySelector('[data-pill="`+host+`"]') &&
			document.querySelector('[data-today-opens]')?.innerText.includes('Now with the network: 1 · with a login: 0')`)
		b.shot("t9-indicator-" + size.name)
		b.Open(e, "#/deck/today")
		b.until(size.name+": Today says what runs with the network", `document.body.innerText.includes('Now with the network: 1')`)
		e.Must("deck.process_stop", map[string]any{"process": p}, nil)
		e.WaitState(p, "stopped", "error", "done")
		b.until(size.name+": the line goes with the run", `!document.body.innerText.includes('Now with the network')`)
		b.Open(e, "#/deck/cockpit")
		b.until(size.name+": the host goes with the run", `document.querySelector('[data-block]') && !document.querySelector('[data-pill="`+host+`"]')`)
	}
}
