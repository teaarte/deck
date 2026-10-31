package owner

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/internal/store"
	"deck2/tests/harness"
)

// twoRunsManifest has two commands of the schedule: look only reads, keep writes a record.
var twoRunsManifest = harness.Manifest(`{"collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}],
  "commands": [
    {"name": "look", "title": "Look", "description": "Reads the items.", "class": "internal", "result": {"type": "object"},
     "executor": {"type": "script", "script": "look"}},
    {"name": "keep", "title": "Keep", "description": "Writes an item.", "class": "internal", "result": {"type": "object"},
     "executor": {"type": "script", "script": "keep"}}],
  "scripts": [{"name": "look", "file": "scripts/look.py", "runtime": "python"}, {"name": "keep", "file": "scripts/keep.py", "runtime": "python"}],
  "triggers": [{"command": "look", "cron": "0 9 * * *"}, {"command": "keep", "cron": "0 9 * * *"}]}`)

// doneToday is the number the line of the day of Today says.
func doneToday(e *harness.Env) string {
	e.T.Helper()
	var lines []struct{ ID, Title string }
	e.Must("deck.today_get", nil, &lines)
	i := slices.IndexFunc(lines, func(l struct{ ID, Title string }) bool { return l.ID == "day" })
	if i < 0 {
		e.T.Fatalf("Today has a line of the day: %+v", lines)
	}
	return lines[i].Title
}

// TestDoneTodayCountsTheOwnersDayWithoutEmptyRuns: the line of the day counts every run done since the owner's
// midnight, more than a page of them, but not the runs of the schedule that only read; it does not go down.
func TestDoneTodayCountsTheOwnersDayWithoutEmptyRuns(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithClock())
	p := e.Project("jobs", twoRunsManifest)
	e.SaveScript("jobs", "scripts/look.py", harness.PythonLib+`
st, out = deck("deck.records_list", {"collection": "items"})
print(json.dumps({"updated": []}))
`)
	e.SaveScript("jobs", "scripts/keep.py", harness.PythonLib+`
st, out = deck("deck.records_upsert", {"collection": "items", "records": json.dumps({"name": "a"})})
print(json.dumps({"updated": ["a"]}))
`)
	today := store.Time(harness.T0.Add(-time.Hour))
	yesterday := store.Time(harness.T0.Add(-9 * time.Hour))
	insert := func(n int, finished, key string, wrote int) {
		for range n {
			// Runs done earlier, more than a test runs in its time.
			e.SQL(`INSERT INTO process (project_id, command, kind, state, retry_key, wrote, started_at, finished_at)
				VALUES (?, 'look', 'script', 'done', ?, ?, ?, ?)`, p, key, wrote, finished, finished)
		}
	}
	insert(600, today, "", 0)
	insert(300, today, "trigger:1", 0)
	insert(5, today, "trigger:1", 1)
	insert(40, yesterday, "", 0)
	if got := doneToday(e); got != "Done today: 605" {
		t.Fatalf("the day counts every run done since midnight, past a page, but the empty runs of the schedule: %s", got)
	}

	e.Clock.Add(time.Hour + time.Minute)
	e.Tick()
	e.Until("both runs of the schedule to end", func() bool {
		n := 0
		for _, r := range e.Procs() {
			if r.Project == "jobs" && r.Trigger != 0 && slices.Contains([]string{"done", "error", "stopped"}, r.State) {
				n++
			}
		}
		return n == 2
	})
	if got := doneToday(e); got != "Done today: 606" {
		t.Fatalf("a run of the schedule that wrote counts, one that only read does not: %s", got)
	}
	var ru []struct{ ID, Title string }
	e.Must("deck.today_get", map[string]any{"lang": "ru"}, &ru)
	if !slices.ContainsFunc(ru, func(l struct{ ID, Title string }) bool {
		return strings.HasPrefix(l.Title, fmt.Sprint("Сделано сегодня: 606"))
	}) {
		t.Fatalf("the day is told in the owner's words: %+v", ru)
	}
}

// TestTodayHealthAndTheScheduleSayOneThing: a schedule that failed is one failure on Today, on Health with its
// count and on top of the Schedule, each with its way to run it again; closed once, it is red nowhere.
func TestTodayHealthAndTheScheduleSayOneThing(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Outcome: provider.Failed, Text: "the model is down"}
	}))

	e.Project("digest", harness.TestManifest([]string{harness.AgentCommand("work", "")}, []string{`{"command": "work", "cron": "0 9 * * *"}`}))
	var ts []struct {
		ID           int64
		Title        string
		ProjectTitle string `json:"project_title"`
		NextAt       string `json:"next_at"`
		LastAt       string `json:"last_at"`
		Failure      string
		LastTone     string `json:"last_tone"`
		LastProcess  int64  `json:"last_process"`
	}
	read := func() {
		ts = nil
		e.Must("deck.triggers_list", map[string]any{"project": "digest", "lang": "ru"}, &ts)
	}
	read()
	if len(ts) != 1 || ts[0].Title != "work" || ts[0].ProjectTitle != "digest" || ts[0].NextAt == "" || ts[0].LastAt != "" {
		t.Fatalf("the schedule says what runs, of which module, and when next: %+v", ts)
	}
	e.Must("deck.trigger_run", map[string]any{"trigger": ts[0].ID}, nil)
	read()
	e.WaitState(ts[0].LastProcess, "error")
	read()
	if ts[0].Failure == "" || ts[0].LastTone != "error" || ts[0].LastAt == "" {
		t.Fatalf("a failed last run is a failure of the schedule with when it ran: %+v", ts)
	}
	if h := today(e, ""); h.Failed != 1 {
		t.Fatalf("Today counts the failure: %+v", h)
	}
	broken := harness.BlockData[[]harness.ListItem](t, e.SettingsBlock("Broken now", nil)["items"])
	if len(broken) != 1 || broken[0].Text != ts[0].Failure && !strings.Contains(broken[0].Text, "the model is down") || !hasLabel(broken[0].Actions, "Run again") {
		t.Fatalf("Health says the same failure with a way to run it again: %+v", broken)
	}
	counted := false
	for _, m := range e.ScreenMessages("", "settings", nil) {
		counted = counted || strings.Contains(string(m["updateComponents"]), `"count":"failures"`)
	}
	if !counted {
		t.Fatal("the tab of Health counts the failures")
	}
	top := harness.BlockData[[]harness.ListItem](t, e.ScreenBlock("", "schedule", "Did not pass", nil)["items"])
	if len(top) != 1 || !strings.Contains(top[0].Title, "work") || !hasLabel(top[0].Actions, "Run again") {
		t.Fatalf("the schedule says on top what did not pass: %+v", top)
	}
	rows := harness.BlockData[[]harness.ScreenRow](t, e.ScreenBlock("", "schedule", "What runs by itself", nil)["rows"])
	rows = slices.DeleteFunc(rows, func(r harness.ScreenRow) bool { return !strings.HasPrefix(fmt.Sprint(r.Cells[0]), "work") })
	if len(rows) != 1 || rows[0].Cells[3] != "see above" || rows[0].Tones[3] != "error" {
		t.Fatalf("the row of the schedule points up instead of saying it twice: %+v", rows)
	}

	var got []struct{ Label, Command string }
	raw, _ := json.Marshal(broken[0].Actions)
	_ = json.Unmarshal(raw, &got)
	i := slices.IndexFunc(got, func(a struct{ Label, Command string }) bool { return a.Label == "Got it" })
	if i < 0 || got[i].Command != "deck.process_dismiss" {
		t.Fatalf("got it closes the failure: %+v", got)
	}
	e.Must("deck.process_dismiss", map[string]any{"process": ts[0].LastProcess}, nil)
	read()
	if h := today(e, ""); h.Failed != 0 || ts[0].Failure != "" || ts[0].LastTone == "error" {
		t.Fatalf("a closed failure is red nowhere: %+v %+v", h, ts)
	}
	if broken := harness.BlockData[[]harness.ListItem](t, e.SettingsBlock("Broken now", nil)["items"]); len(broken) != 0 {
		t.Fatalf("Health is quiet again: %+v", broken)
	}
}

func hasLabel(acts []map[string]any, label string) bool {
	return slices.ContainsFunc(acts, func(a map[string]any) bool { return a["label"] == label })
}

// TestTodayTakesTheButtonsOfADecisionFromItsRow: the buttons of what waits are said once, by asks_list, and Today
// shows those, as the chat does.
func TestTodayTakesTheButtonsOfADecisionFromItsRow(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	_, tok := e.Token("asker", "deck.model_set")
	if code, out := e.As(tok).Call("deck.model_set", map[string]any{"connection": "none", "model": "x"}); code != 200 || !strings.Contains(string(out), `"waiting"`) {
		t.Fatalf("a token's call waits for the owner: %d %s", code, out)
	}
	var asks []struct{ Actions []map[string]any }
	e.Must("deck.asks_list", map[string]any{"lang": "ru"}, &asks)
	if len(asks) != 1 || len(asks[0].Actions) < 3 {
		t.Fatalf("a decision carries its buttons: %+v", asks)
	}
	items := harness.BlockData[[]harness.ListItem](t, e.Screen("", "cockpit", map[string]any{"lang": "ru"})["b0"]["items"])
	verbs := func(acts []map[string]any) []string {
		var out []string
		for _, a := range acts {
			out = append(out, fmt.Sprint(a["verb"], a["label"], a["hold"]))
		}
		return out
	}
	if len(items) != 1 || !slices.Equal(verbs(items[0].Actions), verbs(asks[0].Actions)) {
		t.Fatalf("Today shows the buttons of the row: %v, the row %v", verbs(items[0].Actions), verbs(asks[0].Actions))
	}
	own := harness.Manifest(`{"collections": [{"name": "notes", "description": "Notes.", "key": "title",
    "schema": {"type": "object", "properties": {"title": {"type": "string"}}, "required": ["title"]}}],
  "screens": [{"name": "main", "title": "Main", "blocks": [{"type": "list", "source": {"collection": "notes"}, "item": {"title": "title", "actions": "title"}}]}]}`)
	if code, out := e.Call("deck.manifest_check", map[string]any{"manifest": own}); code != 200 || !strings.Contains(string(out), "only on deck's own screens") {
		t.Fatalf("a module names its buttons itself: %d %s", code, out)
	}
}
