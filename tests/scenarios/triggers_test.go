package scenarios

import (
	"context"
	deckhome "deck2/internal/home"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
	"deck2/internal/registry"
)

func TestANewModuleWithoutCode(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		switch c.command() {
		case "collect":
			writeIdeas(ctx, c, "x")
			return provider.Result{Structured: json.RawMessage(`{"written": 3}`)}
		default:
			var in struct{ Process int64 }
			_ = json.Unmarshal([]byte(inputOf.FindStringSubmatch(c.Req.Prompt)[1]), &in)
			out, _ := c.Tool(ctx, "process_get", map[string]any{"process": in.Process})
			var p struct{ Result struct{ Written int } }
			_ = json.Unmarshal([]byte(out), &p)
			return provider.Result{Structured: json.RawMessage(fmt.Sprintf(`{"seen": %d}`, p.Result.Written))}
		}
	})
	m := strings.Replace(ideasManifest, `"triggers": [{"command": "collect", "cron": "0 9 * * *"}]`,
		`"triggers": [{"command": "collect", "cron": "0 9 * * *"}, {"command": "digest", "after": "collect"}]`, 1)
	m = strings.Replace(m, `"commands": [{`, `"commands": [{"name": "digest", "title": "Digest", "description": "Sums up a collection run", "class": "internal",
    "input": `+processField+`, "executor": {"type": "agent", "prompt": "Sum up."},
    "result": {"type": "object", "properties": {"seen": {"type": "integer"}}, "required": ["seen"]}}, {`, 1)
	p := e.createProject("ideas", m)
	e.Clock.Add(time.Hour)
	e.tick()
	e.waitFor("the digest after the scheduled collection", func() bool { return e.countOf("digest") == 1 })
	e.settle()
	var digest procFull
	for _, pr := range e.processes() {
		if pr.Command == "digest" {
			digest = pr
		}
	}
	collect := e.full(digest.Parent)
	if digest.State != "done" || string(digest.Result) != `{"seen":3}` || collect.Command != "collect" || collect.Trigger == 0 || digest.Depth != 2 {
		t.Fatalf("the finish of collect must start digest with the finished process: %+v %+v", digest, collect)
	}
	if rows := e.screenRows(p, "main"); len(rows) != 2 {
		t.Fatalf("the screen shows the written records, got %v", rows)
	}
	e.waitState(e.run(p, "collect"), "done")
	e.waitFor("the second digest after the button", func() bool { return e.countOf("digest") == 2 })
}

func TestTriggerChainStopsAtNinthStep(t *testing.T) {
	t.Parallel()
	var cmds, trigs []string
	for i := 1; i <= 9; i++ {
		in := processField
		if i == 1 {
			in = ""
		} else {
			trigs = append(trigs, fmt.Sprintf(`{"command": "c%d", "after": "c%d"}`, i, i-1))
		}
		cmds = append(cmds, agentCommand(fmt.Sprintf("c%d", i), in))
	}
	e := newEnv(t, ok)
	p := e.createProject("chain", testManifest(cmds, trigs))
	e.run(p, "c1")
	n := e.waitNote("chain.c9")
	e.settle()
	if !n.CanRead || !strings.Contains(n.Text, "c1 → c2 → c3 → c4 → c5 → c6 → c7 → c8") {
		t.Fatalf("the ninth step is refused with a note naming the chain, which the owner marks read: %+v", n)
	}
	var as []askView
	if e.must("deck.asks_list", nil, &as); len(as) != 0 {
		t.Fatalf("a warning waits for no answer, so it is not among the asks: %+v", as)
	}
	ps := e.processes()
	if len(ps) != 8 || e.countOf("c9") != 0 {
		t.Fatalf("eight steps run, the ninth does not: %+v", ps)
	}
	for _, pr := range ps {
		if want := int64(pr.Command[1] - '0'); pr.Depth != want || pr.State != "done" {
			t.Fatalf("depth of %s must be %d: %+v", pr.Command, want, pr)
		}
	}
	e.must("deck.feed_read", map[string]any{"entry": n.ID}, nil)
	if e.warnings("chain.c9") != 0 {
		t.Fatalf("a note marked read stays read: %+v", e.notes())
	}
}

// TestAWarningOfABaseFromBeforeIsANote: an open warning an older base kept among the asks is a note of the feed.
func TestAWarningOfABaseFromBeforeIsANote(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := newEnvWith(t, home, &Fake{})
	p := e.createProject("chain", testManifest([]string{agentCommand("c1", "")}, nil))
	e.olderBase(5)
	if _, err := e.App.Store.DB.Exec(`INSERT INTO ask (workspace_id, project_id, kind, text) VALUES (1, ?, 'warning', 'deck stopped a chain')`, p); err != nil {
		t.Fatal(err)
	}
	_ = e.App.Close()
	e = newEnvWith(t, home, &Fake{})
	var as []askView
	if e.must("deck.asks_list", nil, &as); len(as) != 0 {
		t.Fatalf("the warning is no ask any more: %+v", as)
	}
	e.waitNote("deck stopped a chain")
}

func TestScheduledRunDoesNotOverlapItsPrevious(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		<-release
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	e.createProject("daily", testManifest([]string{agentCommand("job", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`}))
	e.Clock.Add(time.Hour)
	e.tick()
	e.waitFor("the first run", func() bool { return e.countOf("job") == 1 })
	e.Clock.Add(24 * time.Hour)
	e.tick()
	if n := e.countOf("job"); n != 1 {
		t.Fatalf("a trigger does not start while its previous run goes on, got %d runs", n)
	}
	close(release)
	e.settle()
	e.Clock.Add(24 * time.Hour)
	e.tick()
	e.waitFor("the next run once the previous finished", func() bool { return e.countOf("job") == 2 })
}

func TestEmptyResultStartsNothingAndIsPurgedAfterADay(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`null`)}
	})
	p := e.createProject("quiet", nullable(testManifest([]string{agentCommand("check", ""), agentCommand("notify", processField)},
		[]string{`{"command": "notify", "after": "check"}`})))
	id := e.run(p, "check")
	e.waitState(id, "done")
	e.settle()
	if got := e.full(id); !got.Empty || string(got.Result) != "null" || e.countOf("notify") != 0 {
		t.Fatalf("an empty result is done, collapsed and starts no after trigger: %+v, %d", got, e.countOf("notify"))
	}
	e.Clock.Add(23 * time.Hour)
	e.sweep()
	if e.full(id).ID != id {
		t.Fatal("an empty process stays for a day")
	}
	e.Clock.Add(2 * time.Hour)
	e.sweep()
	e.refused("deck.process_get", map[string]any{"process": id}, 404, "no process")
	if _, err := os.Stat(filepath.Join(deckhome.Layout(e.App.Cfg.Home).Work(), "runs", fmt.Sprint(id))); !os.IsNotExist(err) {
		t.Fatalf("the folder of a purged process goes with it: %v", err)
	}
	kept := e.run(p, "check")
	e.waitState(kept, "done")
	e.settle()
	e.Clock.Add(8 * 24 * time.Hour)
	e.sweep()
	if _, err := os.Stat(filepath.Join(deckhome.Layout(e.App.Cfg.Home).Work(), "runs", fmt.Sprint(kept))); !os.IsNotExist(err) {
		t.Fatalf("the run folder and log of a process finished a week ago are cleared: %v", err)
	}
}

// TestARunLaterOfABaseFromBeforeGoes: deck runs nothing once at a time any more; a run later an older base kept
// leaves the schedule, and its commands are gone.
func TestARunLaterOfABaseFromBeforeGoes(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := newEnvWith(t, home, &Fake{})
	e.createProject("later", testManifest([]string{agentCommand("remind", "")}, []string{`{"command": "remind", "cron": "0 9 * * *"}`}))
	e.olderBase(16)
	if _, err := e.App.Store.DB.Exec(`INSERT INTO "trigger" (project_id, command, kind, params, next_run_at)
		SELECT id, 'remind', 'once', '{}', '2030-01-01T00:00:00.000Z' FROM project WHERE name = 'later'`); err != nil {
		t.Fatal(err)
	}
	_ = e.App.Close()
	e = newEnvWith(t, home, &Fake{})
	if ts := e.triggers(); len(ts) != 1 || ts[0].Kind != "cron" {
		t.Fatalf("only the schedule of the manifest stays: %+v", ts)
	}
	for _, name := range []string{"deck.run_later", "deck.run_later_cancel"} {
		e.refused(name, map[string]any{"command": "later.remind", "at": "2030-01-01T00:00:00Z", "trigger": 1}, 404, "unknown command")
	}
}

func TestMissedRunsAreSpreadAfterSleep(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, ok)
	e.createProject("many", testManifest([]string{agentCommand("a", ""), agentCommand("b", ""), agentCommand("c", ""), agentCommand("d", "")},
		[]string{`{"command": "a", "cron": "0 9 * * *"}`, `{"command": "b", "cron": "0 9 * * *"}`, `{"command": "c", "cron": "0 9 * * *"}`,
			`{"command": "d", "cron": "0 9 * * *", "skip_missed": true}`}))
	wake := e.Clock.Add(72 * time.Hour)
	e.tick()
	e.settle()
	if n := len(e.processes()); n != 1 {
		t.Fatalf("after a sleep the missed runs start one at a time, got %d", n)
	}
	next := map[string]string{}
	for _, tr := range e.triggers() {
		next[tr.Command] = tr.NextRunAt
	}
	slot := func(d time.Duration) string { return wake.Add(d).UTC().Format("2006-01-02T15:04:05.000Z") }
	if next["b"] != slot(time.Minute) || next["c"] != slot(2*time.Minute) || next["d"] != slot(time.Hour) {
		t.Fatalf("missed runs get slots a minute apart; a trigger that skips missed runs moves on: %v", next)
	}
	for i := 2; i <= 3; i++ {
		e.Clock.Add(time.Minute)
		e.tick()
		e.settle()
		if n := len(e.processes()); n != i {
			t.Fatalf("slot %d: want %d runs, got %d", i, i, n)
		}
	}
	e.tick()
	if e.countOf("d") != 0 || len(e.processes()) != 3 {
		t.Fatal("each missed trigger runs exactly once, a skipping one not at all")
	}
}

func TestPausedTriggersAndProjectsDoNotRun(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, ok)
	m := testManifest([]string{agentCommand("job", ""), agentCommand("other", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`})
	e.createProject("paused", m)
	tr := e.triggers()[0]
	e.must("deck.trigger_pause", map[string]any{"trigger": tr.ID, "paused": true}, nil)
	e.Clock.Add(4 * time.Hour)
	e.tick()
	e.must("deck.trigger_pause", map[string]any{"trigger": tr.ID, "paused": false}, nil)
	e.tick()
	if e.countOf("job") != 0 {
		t.Fatal("a paused trigger does not run, and resuming it does not catch up the pause")
	}
	if tomorrow := e.triggers()[0].NextRunAt; tomorrow != time.Date(2026, 9, 26, 9, 0, 0, 0, time.Local).UTC().Format("2006-01-02T15:04:05.000Z") {
		t.Fatalf("a resumed trigger waits for its next slot: %s", tomorrow)
	}
	e.must("deck.project_pause", map[string]any{"project": "paused", "paused": true}, nil)
	e.must("deck.manifest_save", map[string]any{"project": "paused", "manifest": strings.Replace(m, `"triggers": [`,
		`"triggers": [{"command": "other", "cron": "0 9 * * *"}, `, 1)}, nil)
	e.Clock.Add(24 * time.Hour)
	e.tick()
	if ts := e.triggers(); len(e.processes()) != 0 || len(ts) != 2 || !ts[0].Paused || !ts[1].Paused {
		t.Fatalf("a paused project runs no trigger, also one added later: %+v", ts)
	}
	e.must("deck.project_pause", map[string]any{"project": "paused", "paused": false}, nil)
	e.Clock.Add(21 * time.Hour)
	e.tick()
	e.waitFor("both triggers after the project goes on", func() bool { return len(e.processes()) == 2 })
}

func TestPausedTriggersStartNoAfterTriggers(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	p := e.createProject("chain", testManifest([]string{agentCommand("a", ""), agentCommand("b", processField)}, []string{`{"command": "b", "after": "a"}`}))
	for _, tr := range e.triggers() {
		if tr.Command == "b" {
			e.must("deck.trigger_pause", map[string]any{"trigger": tr.ID, "paused": true}, nil)
		}
	}
	e.waitState(e.run(p, "a"), "done")
	e.settle()
	if e.countOf("b") != 0 {
		t.Fatal("a paused after trigger starts nothing")
	}
}

func TestTriggerIdentityAndReservedRetryKey(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	code, out := e.call("deck.project_create", map[string]any{"name": "twice", "manifest": testManifest(
		[]string{agentCommand("pack", `{"type": "object", "properties": {"tone": {"type": "string"}}}`)},
		[]string{`{"command": "pack", "cron": "0 9 * * *", "params": {"tone": "short"}}`, `{"command": "pack", "cron": "0 9 * * *", "params": {"tone": "long"}}`})})
	if code != 422 || !strings.Contains(string(out), `same cron "0 9 * * *"`) {
		t.Fatalf("one command has one trigger per schedule: %d %s", code, out)
	}
	code, out = e.call("deck.project_create", map[string]any{"name": "keyed", "manifest": testManifest([]string{
		agentCommand("job", `{"type": "object", "properties": {"retry_key": {"type": "string"}}}`)}, nil)})
	if code != 422 || !strings.Contains(string(out), "reserved") {
		t.Fatalf("retry_key is reserved: %d %s", code, out)
	}
	e.createProject("jobs", testManifest([]string{agentCommand("job", `{"type": "object", "properties": {}, "additionalProperties": false}`)}, nil))
	var job commandView
	e.must("deck.command_describe", map[string]any{"command": "jobs.job"}, &job)
	if !strings.Contains(string(job.Input), `"retry_key"`) {
		t.Fatalf("every project command takes a retry key: %s", job.Input)
	}
}

func TestPauseBetweenReadingAndFiringATriggerHolds(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, ok)
	pid := e.createProject("race", testManifest([]string{agentCommand("job", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`}))
	rows, err := e.App.Store.Q.ListProjectTriggers(context.Background(), pid)
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	e.must("deck.project_pause", map[string]any{"project": "race", "paused": true}, nil)
	if _, err := e.App.Triggers.Fire(context.Background(), rows[0], map[string]any{}); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("a trigger read before its project paused does not start: %v", err)
	}
	e.must("deck.project_pause", map[string]any{"project": "race", "paused": false}, nil)
	e.must("deck.trigger_pause", map[string]any{"trigger": rows[0].ID, "paused": true}, nil)
	if _, err := e.App.Triggers.Fire(context.Background(), rows[0], map[string]any{}); err == nil {
		t.Fatal("a trigger read before it paused does not start")
	}
	if n := e.countOf("job"); n != 0 {
		t.Fatalf("no process started: %d", n)
	}
}

func TestAScheduledCallTheOwnerAllowedIsTheLastRunOfItsTrigger(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	defer close(release)
	pub := strings.Replace(agentCommand("publish", ""), `"class": "internal"`, `"class": "external"`, 1)
	e.createProject("daily", testManifest([]string{pub}, []string{`{"command": "publish", "cron": "0 9 * * *"}`}))
	e.Clock.Add(time.Hour)
	e.tick()
	a := e.waitAsk()
	if a.Kind != "call" || e.countOf("publish") != 0 {
		t.Fatalf("an external command of a schedule waits for the owner: %+v", a)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := e.sse(ctx)
	e.must("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}, nil)
	waitTopic(t, events, "triggers")
	e.waitFor("the allowed run", func() bool { return e.countOf("publish") == 1 })
	ps := e.processes()
	if ts := e.triggers(); len(ts) != 1 || ts[0].Last == 0 || ts[0].Last != ps[0].ID {
		t.Fatalf("the run the owner allowed is the last run of its trigger: %+v %+v", ts, ps)
	}
}

func TestAnAfterTriggerCallTheOwnerAllowedKeepsItsParent(t *testing.T) {
	t.Parallel()
	e := newEnv(t, ok)
	pub := strings.Replace(agentCommand("publish", `{"type": "object", "properties": {"process": {"type": "integer"}}}`), `"class": "internal"`, `"class": "external"`, 1)
	e.createProject("chain", testManifest([]string{agentCommand("first", ""), pub}, []string{`{"command": "publish", "after": "first"}`}))
	first := e.start("chain", "first", nil)
	e.waitState(first, "done")
	a := e.waitAsk()
	e.must("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}, nil)
	e.waitFor("the allowed run", func() bool { return e.countOf("publish") == 1 })
	for _, p := range e.processes() {
		if p.Command == "publish" && (p.Parent != first || p.Depth != 2) {
			t.Fatalf("the run the owner allowed is the child of the run that fired it: %+v", p)
		}
	}
}

// TestScheduleShowsEveryTriggerRunsOneNowAndPausesAProject: the schedule lists the triggers of all projects;
// run now starts one and keeps its slot; a paused project runs nothing on schedule; the history filters.
func TestScheduleShowsEveryTriggerRunsOneNowAndPausesAProject(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, ok)
	check := newCatalogCheck(t)
	e.createProject("alpha", testManifest([]string{agentCommand("job", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`}))
	e.createProject("beta", testManifest([]string{agentCommand("sync", "")}, []string{`{"command": "sync", "cron": "0 10 * * *"}`}))
	check.messages(t, e.screenMessages("", "schedule", nil))
	rows := func() []screenRow {
		var out []screenRow
		for _, r := range blockData[[]screenRow](t, e.screenData("", "schedule", nil)["b0-0-0"]["rows"]) {
			if r.Cells[0] != "master" {
				out = append(out, r)
			}
		}
		return out
	}
	find := func(rs []screenRow, project string) screenRow {
		for _, r := range rs {
			if r.Cells[0] == project {
				return r
			}
		}
		t.Fatalf("no trigger of %s in %+v", project, rs)
		return screenRow{}
	}
	state := func(project string) (word, last, next string) {
		var ts []struct {
			Word      string `json:"word"`
			LastWord  string `json:"last_word"`
			NextRunAt string `json:"next_run_at"`
		}
		e.must("deck.triggers_list", map[string]any{"project": project}, &ts)
		return ts[0].Word, ts[0].LastWord, ts[0].NextRunAt
	}
	button := func(r screenRow, verb string) map[string]any {
		for _, a := range r.Actions {
			if a["verb"] == verb {
				return a
			}
		}
		t.Fatalf("row %v has no %s: %+v", r.Cells, verb, r.Actions)
		return nil
	}
	press := func(a map[string]any, out any) {
		t.Helper()
		e.must(a["command"].(string), a["params"], out)
	}
	rs := rows()
	job := find(rs, "alpha")
	if len(rs) != 2 || fmt.Sprint(job.Cells[:4]) != "[alpha job 0 9 * * * fake]" {
		t.Fatalf("every trigger of every project with its command, how often and its model: %+v", rs)
	}
	if m := button(job, "model"); m["pick"] != "model" || m["params"].(map[string]any)["command"] != "job" || m["params"].(map[string]any)["project"] != "alpha" {
		t.Fatalf("the model of a trigger is picked in its row: %+v", m)
	}
	_, _, next := state("alpha")

	var started struct{ Process int64 }
	press(button(job, "run_now"), &started)
	e.waitState(started.Process, "done")
	if _, last, now := state("alpha"); last != "done" || now != next {
		t.Fatalf("run now starts the command and keeps its slot: %s %s", last, now)
	}
	button(find(rows(), "alpha"), "open")

	e.must("deck.project_pause", map[string]any{"project": "alpha", "paused": true}, nil)
	if word, _, _ := state("alpha"); word != "module paused" || fmt.Sprint(find(rows(), "alpha").Tones) != "[muted muted <nil> <nil> <nil>]" {
		t.Fatalf("a paused project shows on its triggers: %s %+v", word, find(rows(), "alpha"))
	}
	e.Clock.Add(26 * time.Hour)
	e.tick()
	e.waitFor("beta on its schedule", func() bool { return e.countOf("sync") >= 1 })
	e.settle()
	if n := e.countOf("job"); n != 1 {
		t.Fatalf("a paused project runs nothing on schedule, only what the owner ran: %d", n)
	}
	e.must("deck.project_pause", map[string]any{"project": "alpha", "paused": false}, nil)
	press(button(find(rows(), "alpha"), "pause"), nil)
	if word, _, _ := state("alpha"); word != "paused" {
		t.Fatalf("a trigger pauses alone: %s", word)
	}
	press(button(find(rows(), "alpha"), "resume"), nil)

	history := e.screenData("", "schedule", map[string]any{"filter": `{"b0-1-0": {"project": "alpha"}}`})["b0-1-0"]
	hrows := blockData[[]screenRow](t, history["rows"])
	if len(hrows) != 1 || hrows[0].Cells[1] != "alpha" || !strings.Contains(string(history["filters"]), `"value":"alpha"`) {
		t.Fatalf("the history filters by project: %+v %s", hrows, history["filters"])
	}
	if states := blockData[[]screenRow](t, e.screenData("", "schedule", map[string]any{"filter": `{"b0-1-0": {"state": "error"}}`})["b0-1-0"]["rows"]); len(states) != 0 {
		t.Fatalf("the history filters by state: %+v", states)
	}
	e.refused("deck.trigger_run", map[string]any{"trigger": 999}, 404, "no trigger")
}

// TestRunNowReturnsTheRunStillGoing: a second run now while the first goes gives that run.
func TestRunNowReturnsTheRunStillGoing(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "job" {
			select {
			case <-gate:
			case <-ctx.Done():
			}
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	e.createProject("alpha", testManifest([]string{agentCommand("job", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`}))
	job := e.triggers()[0].ID
	var first, second struct{ Process int64 }
	e.must("deck.trigger_run", map[string]any{"trigger": job}, &first)
	e.must("deck.trigger_run", map[string]any{"trigger": job}, &second)
	if first.Process == 0 || second.Process != first.Process || e.countOf("job") != 1 {
		t.Fatalf("run now while the run goes gives that run: %d %d", first.Process, second.Process)
	}
	for _, tr := range e.triggers() {
		if tr.ID == job && tr.Last != first.Process {
			t.Fatalf("a run now is the last run of its trigger: %+v", tr)
		}
	}
	close(gate)
	e.waitState(first.Process, "done")
}

// TestRunNowTakesACommandOnlyTriggersCallAndTheScheduleHearsAboutRuns: run now has the trigger's rights, and
// a run of a trigger tells the schedule to read its next run and last result again.
func TestRunNowTakesACommandOnlyTriggersCallAndTheScheduleHearsAboutRuns(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, ok)
	own := strings.Replace(agentCommand("digest", ""), `"class": "internal"`, `"class": "internal", "callers": ["trigger"]`, 1)
	e.createProject("alpha", testManifest([]string{own}, []string{`{"command": "digest", "cron": "0 9 * * *"}`}))
	tr := e.triggers()[0]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := e.sse(ctx)
	var started struct{ Process int64 }
	e.must("deck.trigger_run", map[string]any{"trigger": tr.ID}, &started)
	e.waitState(started.Process, "done")
	waitTopic(t, events, "triggers")
	e.Clock.Add(2 * time.Hour)
	e.tick()
	e.waitFor("the scheduled run", func() bool { return e.countOf("digest") == 2 })
	waitTopic(t, events, "triggers")
}

func TestTriggerParamsKeepLargeIntegers(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, idle)
	m := strings.Replace(calcManifest, `"executor": {"type": "agent", "prompt": "OLD PROMPT"}}]`,
		`"executor": {"type": "agent", "prompt": "OLD PROMPT"}}],
  "triggers": [{"command": "calc", "cron": "0 9 * * *", "params": {"n": 9007199254740993}}]`, 1)
	e.createProject("nums", m)
	e.Clock.Add(48 * time.Hour)
	e.App.Scheduler.Tick(context.Background())
	e.App.Runner.Wait()
	ps := e.processes()
	if len(ps) != 1 || ps[0].Project != "nums" {
		t.Fatalf("trigger params reach the command exactly: %+v", ps)
	}
	if got, _ := e.App.Store.Q.GetProcess(context.Background(), ps[0].ID); !strings.Contains(got.Input, "9007199254740993") {
		t.Fatalf("trigger params reach the command exactly: %s", got.Input)
	}
}

// TestAScheduledRunThatFailsToStartSaysWhy: a cron trigger whose start fails shows the reason in the health of its
// project until a run of it starts.
func TestAScheduledRunThatFailsToStartSaysWhy(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, ok)
	text := strings.Replace(testManifest([]string{agentCommand("remind", "")}, []string{`{"command": "remind", "cron": "0 * * * *"}`}),
		`"prompt": "Do remind."`, `"prompt_file": "prompts/remind.md"`, 1)
	e.createProject("later", text)
	e.Clock.Add(time.Hour)
	e.tick()
	if h := e.waitHealth("trigger:later.remind", "error"); !strings.Contains(h.Error, "prompts/remind.md") || e.countOf("remind") != 0 {
		t.Fatalf("the health of the project says why it did not start: %+v", h)
	}
	e.must("deck.document_save", map[string]any{"project": "later", "name": "prompts/remind.md", "kind": "prompt", "text": "Remind."}, nil)
	e.Clock.Add(time.Hour)
	e.tick()
	e.waitFor("the run once it starts", func() bool { return e.countOf("remind") == 1 })
	var hs []healthRow
	e.must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if h.Subject == "trigger:later.remind" {
			t.Fatalf("the health row goes once it starts: %+v", h)
		}
	}
}

// TestEnablingADraftClearsWhyItsTriggersDidNotStart: a schedule of a draft does not start and says why in health;
// once the owner enables the project the reason is gone, and so is the red row.
func TestEnablingADraftClearsWhyItsTriggersDidNotStart(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, ok)
	_, tok := e.token("terminal", "deck.*")
	m := testManifest([]string{agentCommand("remind", "")}, []string{`{"command": "remind", "cron": "0 * * * *"}`})
	if code, out := e.callWith(tok, "deck.project_create", map[string]any{"name": "later", "manifest": m}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	e.Clock.Add(time.Hour)
	e.tick()
	if h := e.waitHealth("trigger:later.remind", "error"); !strings.Contains(h.Error, "draft") {
		t.Fatalf("a schedule of a draft says why it did not start: %+v", h)
	}
	e.must("deck.project_enable", map[string]any{"project": "later", "revision": e.project("later").Revision}, nil)
	var hs []healthRow
	e.must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if h.Subject == "trigger:later.remind" {
			t.Fatalf("enabling the project clears why its schedule did not start: %+v", h)
		}
	}
}

// TestAHungToolIsSeenAndStopsAtItsCeiling: a tool call that does not answer holds a ceiling like a script; a cron slot
// it swallows is a warning in health until a run starts, and the stop at the ceiling is an error.
func TestAHungToolIsSeenAndStopsAtItsCeiling(t *testing.T) {
	t.Parallel()
	slow := newService(t, func(w http.ResponseWriter, r *http.Request, n int) { <-r.Context().Done() })
	e := newClockEnv(t, nil)
	e.createProject("feed", fmt.Sprintf(`{"version": 12, "commands": [{"name": "fetch", "title": "Fetch", "description": "Fetches the feed.",
	  "class": "internal", "executor": {"type": "tool", "tool": "pull"}}],
	  "tools": {"http": [{"name": "pull", "description": "Pulls the feed", "method": "GET", "url": "%s/feed", "class": "internal"}]},
	  "triggers": [{"command": "fetch", "cron": "0 9 * * *"}]}`, slow.URL))
	e.Clock.Add(time.Hour)
	e.tick()
	e.waitFor("the first run", func() bool { return e.countOf("fetch") == 1 })
	id := e.processes()[0].ID
	e.waitFor("the call at the service", func() bool { return len(slow.requests()) == 1 })
	if e.full(id).Deadline == "" {
		t.Fatalf("a tool call holds a ceiling: %+v", e.full(id))
	}
	e.Clock.Add(24 * time.Hour)
	e.tick()
	if h := e.waitHealth("trigger:feed.fetch", "warning"); !strings.Contains(h.Error, fmt.Sprint(id)) || e.countOf("fetch") != 1 {
		t.Fatalf("a slot the run still going swallowed is a warning naming that run: %+v", h)
	}
	e.sweep()
	if got := e.waitState(id, "error"); !strings.Contains(got.Error, "ceiling") {
		t.Fatalf("a stop at the ceiling is an error, not a stop of the owner: %+v", got)
	}
	e.Clock.Add(24 * time.Hour)
	e.tick()
	e.waitFor("the next run", func() bool { return e.countOf("fetch") == 2 })
	var hs []healthRow
	e.must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if h.Subject == "trigger:feed.fetch" {
			t.Fatalf("the warning goes once a run starts: %+v", h)
		}
	}
}

// TestRunNowKeepsTheRightsOfItsCaller: a token runs now only a command it may call itself, and only the owner
// runs a command of a paused project now.
func TestRunNowKeepsTheRightsOfItsCaller(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, ok)
	e.createProject("alpha", testManifest([]string{agentCommand("c1", "")}, []string{`{"command": "c1", "cron": "0 9 * * *"}`}))
	job := e.triggers()[0].ID
	_, narrow := e.token("narrow", "deck.trigger_run")
	if code, out := e.callWith(narrow, "deck.trigger_run", map[string]any{"trigger": job}); code != 403 || e.countOf("c1") != 0 {
		t.Fatalf("a token that may not call the command does not run it now: %d %s, %d runs", code, out, e.countOf("c1"))
	}
	_, wide := e.token("wide", "deck.trigger_run", "alpha.*")
	if code, out := e.callWith(wide, "deck.trigger_run", map[string]any{"trigger": job}); code != 200 || e.countOf("c1") != 1 {
		t.Fatalf("a token that may call the command runs it now: %d %s", code, out)
	}
	e.must("deck.project_pause", map[string]any{"project": "alpha", "paused": true}, nil)
	master := registry.Actor{Kind: registry.Master, Workspace: 1}
	if _, err := e.App.Gateway.CallAs(context.Background(), master, "deck.trigger_run", []byte(fmt.Sprintf(`{"trigger": %d}`, job))); err == nil ||
		!strings.Contains(err.Error(), "paused") || e.countOf("c1") != 1 {
		t.Fatalf("the master does not run a command of a paused project now: %v, %d runs", err, e.countOf("c1"))
	}
	e.settle()
	var started struct{ Process int64 }
	e.must("deck.trigger_run", map[string]any{"trigger": job}, &started)
	if e.waitState(started.Process, "done"); e.countOf("c1") != 2 {
		t.Fatalf("the owner runs it now in a pause: %d runs", e.countOf("c1"))
	}
}
