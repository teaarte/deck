package modules

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"deck2/tests/harness"
)

// reportRow is a line of deck.project_report.
type reportRow struct {
	Right, Subject, Group, Says string
	Count, Refused              int64
	Last                        string
}

func report(e *harness.Env, project string) []reportRow {
	var rows []reportRow
	e.Must("deck.project_report", map[string]any{"project": project, "days": 1}, &rows)
	return rows
}

func lineOf(rows []reportRow, right, subject string) reportRow {
	for _, r := range rows {
		if r.Right == right && r.Subject == subject {
			return r
		}
	}
	return reportRow{}
}

// TestTheReportOfAModuleAgreesWithTheJournalOfTheGateway: the door writes the requests of a run to each host to the
// journal; the report says the same numbers, with the calls that needed a right, and the rights when last used.
func TestTheReportOfAModuleAgreesWithTheJournalOfTheGateway(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{}`) })
	host := strings.TrimPrefix(svc.URL, "http://")
	e := harness.New(t)
	e.Project("reach", harness.ScriptManifest("go", "", fmt.Sprintf(`, "rights": [{"right": "network", "what": [%q], "why": "reads it"}]`, host)))
	e.SaveScript("reach", "scripts/main.py", harness.PythonLib+`
print(json.dumps({"got": [fetch("`+svc.URL+`/a"), fetch("`+svc.URL+`/b"), fetch("`+svc.URL+`/c"), fetch("http://127.0.0.1:1/")]}))
`)
	for range 2 {
		if p := e.WaitState(e.Start("reach", "go", nil), "done", "error"); p.State != "done" || !strings.Contains(string(p.Result), "[200,200,200,403]") {
			t.Fatalf("the script reaches its host three times and is refused another: %+v", p)
		}
	}
	if n := len(svc.Requests()); n != 6 {
		t.Fatalf("six requests reached the service: %d", n)
	}
	var calls []struct {
		Need, Subject, Outcome string
		Count                  int64
		Process                int64
	}
	e.Must("deck.calls_list", map[string]any{"project": "reach", "outcome": "reached"}, &calls)
	var journal int64
	for _, c := range calls {
		if c.Need != "network" || c.Subject != host || c.Process == 0 {
			t.Fatalf("a line of the door names the host, the right and the run: %+v", c)
		}
		journal += c.Count
	}
	if len(calls) != 2 || journal != 6 {
		t.Fatalf("the journal has a line a run, six requests in all: %+v", calls)
	}
	got := lineOf(report(e, "reach"), "network", host)
	if got.Count != journal || got.Group != "Network" || !strings.Contains(got.Says, host) || got.Last == "" {
		t.Fatalf("the report counts what the journal does: %+v", got)
	}
	var rights []struct {
		Right, Subject, Used, Module, Title string
	}
	e.Must("deck.rights_list", map[string]any{"project": "reach"}, &rights)
	for _, r := range rights {
		if r.Right == "network" && (r.Used == "" || r.Title != "Network" || r.Module == "") || r.Right == "send" && r.Used != "" && r.Subject == "publish" {
			t.Fatalf("a right says when it was last used, the module and the right in words: %+v", rights)
		}
	}

	notes := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{}`) })
	_, tok := e.Token("terminal", "deck.*", "notes.*")
	e.Project("notes", harness.PostManifest(notes.URL, ""))
	e.Secret("notes", "svc", "k")
	e.Must("deck.right_set", map[string]any{"project": "notes", "right": "send", "subject": "post", "answer": "always"}, nil)
	for i := range 3 {
		if code, out := e.As(tok).Call("notes.post", map[string]any{"id": fmt.Sprint(i), "text": "hi"}); code != 200 {
			t.Fatalf("an always lets the post go: %d %s", code, out)
		}
	}
	e.Must("deck.right_set", map[string]any{"project": "notes", "right": "send", "subject": "post", "answer": "no"}, nil)
	if code, _ := e.As(tok).Call("notes.post", map[string]any{"id": "x", "text": "hi"}); code == 200 {
		t.Fatal("a no refuses the post")
	}
	if got := lineOf(report(e, "notes"), "send", "post"); got.Count != 3 || got.Refused != 1 || len(notes.Requests()) != 3 {
		t.Fatalf("the report counts the posts made and refused as the service saw them: %+v, %d requests", got, len(notes.Requests()))
	}
}

// TestTodaySaysHowManyRequestsTheDayBroughtAndWhatRunsWithTheNetwork: the card of Today counts the requests of the day
// and, while a run holds the network of its box, says so with its host; the line goes with the run.
func TestTodaySaysHowManyRequestsTheDayBroughtAndWhatRunsWithTheNetwork(t *testing.T) {
	t.Parallel()
	svc := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { harness.JsonReply(w, `{}`) })
	host := strings.TrimPrefix(svc.URL, "http://")
	e := harness.New(t)
	_, tok := e.Token("terminal", "deck.*", "notes.*")
	e.Project("notes", harness.PostManifest(svc.URL, ""))
	e.Secret("notes", "svc", "k")
	for i := range 2 {
		e.As(tok).Call("notes.post", map[string]any{"id": fmt.Sprint(i), "text": "hi"})
	}
	type line struct{ Kind, Title, Text string }
	today := func() map[string]line {
		var lines []line
		e.Must("deck.today_get", nil, &lines)
		out := map[string]line{}
		for _, l := range lines {
			out[l.Kind] = l
		}
		return out
	}
	if got := today()["asked"]; got.Title != "Requests today: 2" {
		t.Fatalf("Today counts the requests of the day: %+v", today())
	}
	var ru []line
	e.Must("deck.today_get", map[string]any{"lang": "ru"}, &ru)
	for _, l := range ru {
		if l.Kind == "asked" && !strings.Contains(l.Title, "2") || l.Kind == "asked" && strings.HasPrefix(l.Title, "Requests") {
			t.Fatalf("the count speaks the owner's language: %+v", l)
		}
	}

	e.Project("slow", harness.ScriptManifest("go", "", fmt.Sprintf(`, "rights": [{"right": "network", "what": [%q], "why": "reads it"}]`, host)))
	e.SaveScript("slow", "scripts/main.py", "import time\ntime.sleep(120)\n")
	p := e.Start("slow", "go", nil)
	e.Until("the line of the network", func() bool { return strings.Contains(today()["opens"].Text, host) })
	if got := today()["opens"]; got.Title != "Now with the network: 1 · with a login: 0" {
		t.Fatalf("Today says how many runs hold the network: %+v", got)
	}
	var open []struct {
		ID    int64
		Opens *struct{ Hosts []string }
	}
	e.Must("deck.processes_list", map[string]any{"open": true}, &open)
	if len(open) != 1 || open[0].Opens == nil || len(open[0].Opens.Hosts) != 1 || open[0].Opens.Hosts[0] != host {
		t.Fatalf("an open run says the hosts its box reaches: %+v", open)
	}
	e.Must("deck.process_stop", map[string]any{"process": p}, nil)
	e.WaitState(p, "stopped", "error", "done")
	if got, ok := today()["opens"]; ok {
		t.Fatalf("the line goes with the run: %+v", got)
	}
}
