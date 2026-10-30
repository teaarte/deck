package scenarios

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/store"
)

// twoRunsManifest has two commands of the schedule: look only reads, keep writes a record.
const twoRunsManifest = `{"version": 13,
  "collections": [{"name": "items", "description": "An item.", "key": "name",
    "schema": {"type": "object", "properties": {"name": {"type": "string"}}, "required": ["name"]}}],
  "commands": [
    {"name": "look", "title": "Look", "description": "Reads the items.", "class": "internal", "result": {"type": "object"},
     "executor": {"type": "script", "script": "look"}},
    {"name": "keep", "title": "Keep", "description": "Writes an item.", "class": "internal", "result": {"type": "object"},
     "executor": {"type": "script", "script": "keep"}}],
  "scripts": [{"name": "look", "file": "scripts/look.py", "runtime": "python"}, {"name": "keep", "file": "scripts/keep.py", "runtime": "python"}],
  "triggers": [{"command": "look", "cron": "0 9 * * *"}, {"command": "keep", "cron": "0 9 * * *"}]}`

// doneToday is the number the line of the day of Today says.
func (e *Env) doneToday() string {
	e.T.Helper()
	var lines []struct{ ID, Title string }
	e.must("deck.today", nil, &lines)
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
	e := newClockEnv(t, nil)
	p := e.createProject("jobs", twoRunsManifest)
	e.saveScript("jobs", "scripts/look.py", pythonLib+`
st, out = deck("deck.records_list", {"collection": "items"})
print(json.dumps({"updated": []}))
`)
	e.saveScript("jobs", "scripts/keep.py", pythonLib+`
st, out = deck("deck.records_upsert", {"collection": "items", "records": json.dumps({"name": "a"})})
print(json.dumps({"updated": ["a"]}))
`)
	today := store.Time(t0.Add(-time.Hour))
	yesterday := store.Time(t0.Add(-9 * time.Hour))
	insert := func(n int, finished, key string, wrote int) {
		for range n {
			if _, err := e.App.Store.DB.Exec(`INSERT INTO process (project_id, command, kind, state, retry_key, wrote, started_at, finished_at)
				VALUES (?, 'look', 'script', 'done', ?, ?, ?, ?)`, p, key, wrote, finished, finished); err != nil {
				t.Fatal(err)
			}
		}
	}
	insert(600, today, "", 0)
	insert(300, today, "trigger:1", 0)
	insert(5, today, "trigger:1", 1)
	insert(40, yesterday, "", 0)
	if got := e.doneToday(); got != "Done today: 605" {
		t.Fatalf("the day counts every run done since midnight, past a page, but the empty runs of the schedule: %s", got)
	}

	e.Clock.Add(time.Hour + time.Minute)
	e.tick()
	e.waitFor("both runs of the schedule to end", func() bool {
		var n int
		_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM process WHERE project_id = ? AND trigger_id IS NOT NULL AND finished_at IS NOT NULL`, p).Scan(&n)
		return n == 2
	})
	if got := e.doneToday(); got != "Done today: 606" {
		t.Fatalf("a run of the schedule that wrote counts, one that only read does not: %s", got)
	}
	var ru []struct{ ID, Title string }
	e.must("deck.today", map[string]any{"lang": "ru"}, &ru)
	if !slices.ContainsFunc(ru, func(l struct{ ID, Title string }) bool {
		return strings.HasPrefix(l.Title, fmt.Sprint("Сделано сегодня: 606"))
	}) {
		t.Fatalf("the day is told in the owner's words: %+v", ru)
	}
}
