package scenarios

import (
	"context"
	deckhome "deck2/internal/home"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
)

func TestNewVectorWithoutCode(t *testing.T) {
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

func TestTriggerChainStopsAtSixthStep(t *testing.T) {
	var cmds, trigs []string
	for i := 1; i <= 6; i++ {
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
	a := e.waitAsk()
	e.settle()
	if a.Kind != "warning" || !strings.Contains(a.Text, "chain.c6") {
		t.Fatalf("the sixth step must be refused with a warning, got %+v", a)
	}
	ps := e.processes()
	if len(ps) != 5 || e.countOf("c6") != 0 {
		t.Fatalf("five steps run, the sixth does not: %+v", ps)
	}
	for _, pr := range ps {
		if want := int64(pr.Command[1] - '0'); pr.Depth != want || pr.State != "done" {
			t.Fatalf("depth of %s must be %d: %+v", pr.Command, want, pr)
		}
	}
	e.answer(a.ID, "ok")
	if _, out := e.call("deck.asks_list", nil); strings.Contains(string(out), "warning") {
		t.Fatalf("an answered warning leaves the list: %s", out)
	}
}

func TestScheduledRunDoesNotOverlapItsPrevious(t *testing.T) {
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

func TestLaneTriggerDoesNotStartTwiceForTheSameCard(t *testing.T) {
	release := make(chan struct{})
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		<-release
		return provider.Result{Text: "ok"}
	})
	e.createProject("work", boardManifest)
	a := e.card(map[string]any{"project": "work", "title": "A", "lane": "doing"})
	e.must("deck.card_move", map[string]any{"card": a.ID, "lane": "ready"}, nil)
	e.must("deck.card_move", map[string]any{"card": a.ID, "lane": "doing"}, nil)
	e.card(map[string]any{"project": "work", "title": "B", "lane": "doing"})
	if n := e.countOf("pack"); n != 2 {
		t.Fatalf("each card gets one run while it goes on, got %d", n)
	}
	close(release)
	e.settle()
}

func TestEmptyResultStartsNothingAndIsPurgedAfterADay(t *testing.T) {
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

func TestRunLaterFiresOnTimeAndOnceAfterSleep(t *testing.T) {
	e := newClockEnv(t, ok)
	e.createProject("later", testManifest([]string{agentCommand("remind", `{"type": "object", "properties": {"what": {"type": "string"}}}`)}, nil))
	at := func(d time.Duration) string { return e.Clock.Now().Add(d).Format(time.RFC3339) }
	var r struct{ Trigger int64 }
	e.must("deck.run_later", map[string]any{"command": "later.remind", "at": at(time.Hour), "params": `{"what": "call"}`}, &r)
	if ts := e.triggers(); len(ts) != 1 || ts[0].Kind != "once" || ts[0].ID != r.Trigger {
		t.Fatalf("a run later is a row of the schedule: %+v", ts)
	}
	e.Clock.Add(30 * time.Minute)
	e.tick()
	if e.countOf("remind") != 0 {
		t.Fatal("run later must not fire early")
	}
	e.Clock.Add(30 * time.Minute)
	e.tick()
	e.waitFor("the run at its time", func() bool { return e.countOf("remind") == 1 })
	if len(e.triggers()) != 0 {
		t.Fatal("a fired run later leaves the schedule")
	}
	e.must("deck.run_later", map[string]any{"command": "later.remind", "at": at(2 * time.Hour)}, nil)
	e.must("deck.run_later", map[string]any{"command": "later.remind", "at": at(3 * time.Hour)}, &r)
	e.must("deck.run_later_cancel", map[string]any{"trigger": r.Trigger}, nil)
	e.Clock.Add(10 * time.Hour)
	e.tick()
	e.tick()
	e.settle()
	if n := e.countOf("remind"); n != 2 {
		t.Fatalf("a run later missed in sleep fires once, a cancelled one never: %d runs", n)
	}
	e.refused("deck.run_later", map[string]any{"command": "later.nope", "at": at(time.Hour)}, 404, "unknown command")
	e.refused("deck.run_later", map[string]any{"command": "later.remind", "at": at(time.Hour), "params": `{"what": 5}`}, 422, "what: got number, want string")
}

func TestMissedRunsAreSpreadAfterSleep(t *testing.T) {
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

func TestPausedProjectStartsNoLaneOrAfterTriggers(t *testing.T) {
	e := newEnv(t, ok)
	e.createProject("work", boardManifest)
	e.must("deck.project_pause", map[string]any{"project": "work", "paused": true}, nil)
	e.card(map[string]any{"project": "work", "title": "A", "lane": "doing"})
	p := e.createProject("chain", testManifest([]string{agentCommand("a", ""), agentCommand("b", processField)}, []string{`{"command": "b", "after": "a"}`}))
	for _, tr := range e.triggers() {
		if tr.Command == "b" {
			e.must("deck.trigger_pause", map[string]any{"trigger": tr.ID, "paused": true}, nil)
		}
	}
	e.waitState(e.run(p, "a"), "done")
	e.settle()
	if e.countOf("pack") != 0 || e.countOf("b") != 0 {
		t.Fatal("paused lane and after triggers start nothing")
	}
}

func TestTriggerIdentityAndReservedRetryKey(t *testing.T) {
	e := newEnv(t, nil)
	code, out := e.call("deck.project_create", map[string]any{"name": "twice", "manifest": strings.Replace(boardManifest,
		`"triggers": [{"command": "pack", "lane": "doing", "params": {"tone": "short"}}]`,
		`"triggers": [{"command": "pack", "lane": "doing", "params": {"tone": "short"}}, {"command": "pack", "lane": "doing", "params": {"tone": "long"}}]`, 1)})
	if code != 422 || !strings.Contains(string(out), `same lane "doing"`) {
		t.Fatalf("one command has one trigger per lane: %d %s", code, out)
	}
	code, out = e.call("deck.project_create", map[string]any{"name": "keyed", "manifest": testManifest([]string{
		agentCommand("job", `{"type": "object", "properties": {"retry_key": {"type": "string"}}}`)}, nil)})
	if code != 422 || !strings.Contains(string(out), "reserved") {
		t.Fatalf("retry_key is reserved: %d %s", code, out)
	}
	e.createProject("jobs", testManifest([]string{agentCommand("job", `{"type": "object", "properties": {}, "additionalProperties": false}`)}, nil))
	_, out = e.do("GET", "/api/commands", nil)
	if !strings.Contains(string(out), `"retry_key"`) {
		t.Fatalf("every project command takes a retry key: %s", out)
	}
}

func TestCardMovedByAnAgentContinuesItsChain(t *testing.T) {
	var card int64
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "mover" {
			c.Tool(ctx, "card_move", map[string]any{"card": card, "lane": "doing"})
		}
		return provider.Result{Text: "ok"}
	})
	e.createProject("work", strings.Replace(boardManifest, `"commands": [`, `"commands": [{"name": "mover", "title": "Mover",
    "description": "Moves a card.", "class": "internal", "executor": {"type": "agent", "prompt": "Move it."}}, `, 1))
	card = e.card(map[string]any{"project": "work", "title": "A"}).ID
	var mover struct{ Process int64 }
	e.must("work.mover", map[string]any{}, &mover)
	e.waitFor("the lane command", func() bool { return e.countOf("pack") == 1 })
	for _, pr := range e.processes() {
		if pr.Command == "pack" && (pr.Parent != mover.Process || pr.Depth != 2) {
			t.Fatalf("a lane command started by an agent's move continues that agent's chain: %+v", pr)
		}
	}
	e.settle()
}

func TestPauseBetweenReadingAndFiringATriggerHolds(t *testing.T) {
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

// TestAnAllowedRunLaterStartsThoughItsTriggerIsGone: a run later of an external command waits for the owner, and
// the yes starts it, though the trigger left the schedule when it fired.
func TestAnAllowedRunLaterStartsThoughItsTriggerIsGone(t *testing.T) {
	e := newClockEnv(t, ok)
	e.createProject("later", strings.Replace(testManifest([]string{agentCommand("remind", "")}, nil), `"class": "internal"`, `"class": "external"`, 1))
	e.must("deck.run_later", map[string]any{"command": "later.remind", "at": e.Clock.Now().Add(time.Hour).Format(time.RFC3339)}, nil)
	e.Clock.Add(time.Hour)
	e.tick()
	a := e.waitAsk()
	if a.Kind != "call" || len(e.triggers()) != 0 {
		t.Fatalf("the fired run later waits for the owner and has left the schedule: %+v", a)
	}
	e.answer(a.ID, "allow")
	e.waitFor("the allowed run", func() bool { return e.countOf("remind") == 1 })
}

// TestAnAgentSchedulesAnExternalCommandAndTheOwnerSaysYesWhenItFires: run later checks only the caller and the
// input; the class rules ask the owner when the run comes.
func TestAnAgentSchedulesAnExternalCommandAndTheOwnerSaysYesWhenItFires(t *testing.T) {
	var e *Env
	e = newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "work" {
			out, bad := c.Tool(ctx, "run_later", map[string]any{"command": "later.publish", "at": e.Clock.Now().Add(time.Hour).Format(time.RFC3339)})
			if bad {
				return provider.Result{Text: "refused: " + out}
			}
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	})
	publish := strings.Replace(agentCommand("publish", ""), `"class": "internal"`, `"class": "external"`, 1)
	p := e.createProject("later", testManifest([]string{agentCommand("work", ""), publish}, nil))
	if got := e.waitState(e.run(p, "work"), "done", "error"); got.State != "done" || len(e.triggers()) != 1 {
		t.Fatalf("an agent schedules an external command of its project: %+v %+v", got, e.triggers())
	}
	e.Clock.Add(2 * time.Hour)
	e.tick()
	if a := e.waitAsk(); a.Kind != "call" || !strings.Contains(a.Text, "later.publish") || e.countOf("publish") != 0 {
		t.Fatalf("when it fires, the external command waits for the owner: %+v", a)
	}
}

// TestScheduleShowsEveryTriggerRunsOneNowAndPausesAProject: the schedule lists the triggers of all projects;
// run now starts one and keeps its slot; a paused project runs nothing on schedule; the history filters.
func TestScheduleShowsEveryTriggerRunsOneNowAndPausesAProject(t *testing.T) {
	e := newClockEnv(t, ok)
	check := newCatalogCheck(t)
	e.createProject("alpha", testManifest([]string{agentCommand("job", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`}))
	e.createProject("beta", testManifest([]string{agentCommand("sync", "")}, []string{`{"command": "sync", "cron": "0 10 * * *"}`}))
	e.must("deck.run_later", map[string]any{"command": "beta.sync", "at": t0.Add(90 * time.Minute).UTC().Format(time.RFC3339)}, nil)
	check.messages(t, e.screenMessages("", "schedule", nil))
	rows := func() []screenRow {
		var out []screenRow
		for _, r := range blockData[[]screenRow](t, e.screenData("", "schedule", nil)["b2"]["rows"]) {
			if r.Cells[0] != "master" {
				out = append(out, r)
			}
		}
		return out
	}
	find := func(rs []screenRow, project, when string) screenRow {
		for _, r := range rs {
			if r.Cells[0] == project && r.Cells[2] == when {
				return r
			}
		}
		t.Fatalf("no trigger %s %s in %+v", project, when, rs)
		return screenRow{}
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
	job := find(rs, "alpha", "0 9 * * *")
	if len(rs) != 3 || job.Cells[1] != "job" || job.Cells[3] == "" || job.Cells[5] != "on" {
		t.Fatalf("every trigger of every project with its command, schedule and next run: %+v", rs)
	}
	next := job.Cells[3]
	find(rs, "beta", "once")

	var started struct{ Process int64 }
	press(button(job, "run_now"), &started)
	e.waitState(started.Process, "done")
	job = find(rows(), "alpha", "0 9 * * *")
	if job.Cells[4] != "done" || job.Cells[3] != next {
		t.Fatalf("run now starts the command and keeps its slot: %+v", job.Cells)
	}
	button(job, "open")

	projects := blockData[[]screenRow](t, e.screenData("", "schedule", nil)["b3"]["rows"])
	press(button(projects[0], "pause"), nil)
	if job = find(rows(), "alpha", "0 9 * * *"); job.Cells[5] != "project paused" {
		t.Fatalf("a paused project shows on its triggers: %+v", job.Cells)
	}
	e.Clock.Add(26 * time.Hour)
	e.tick()
	e.waitFor("beta on its schedule", func() bool { return e.countOf("sync") >= 1 })
	e.settle()
	if n := e.countOf("job"); n != 1 {
		t.Fatalf("a paused project runs nothing on schedule, only what the owner ran: %d", n)
	}
	projects = blockData[[]screenRow](t, e.screenData("", "schedule", nil)["b3"]["rows"])
	press(button(projects[0], "resume"), nil)
	press(button(find(rows(), "alpha", "0 9 * * *"), "pause"), nil)
	if job = find(rows(), "alpha", "0 9 * * *"); job.Cells[5] != "paused" {
		t.Fatalf("a trigger pauses alone: %+v", job.Cells)
	}
	press(button(job, "resume"), nil)

	history := e.screenData("", "schedule", map[string]any{"filter": `{"b4": {"project": "alpha"}}`})["b4"]
	hrows := blockData[[]screenRow](t, history["rows"])
	if len(hrows) != 1 || hrows[0].Cells[1] != "alpha" || !strings.Contains(string(history["filters"]), `"value":"alpha"`) {
		t.Fatalf("the history filters by project: %+v %s", hrows, history["filters"])
	}
	if states := blockData[[]screenRow](t, e.screenData("", "schedule", map[string]any{"filter": `{"b4": {"state": "error"}}`})["b4"]["rows"]); len(states) != 0 {
		t.Fatalf("the history filters by state: %+v", states)
	}
	e.refused("deck.trigger_run", map[string]any{"trigger": 999}, 404, "no trigger")
}

// TestRunNowReturnsTheRunStillGoingAndSpendsARunLater: a second run now while the first goes gives that run;
// run now on a run later starts it and takes it off the schedule.
func TestRunNowReturnsTheRunStillGoingAndSpendsARunLater(t *testing.T) {
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
	e.createProject("alpha", testManifest([]string{agentCommand("job", ""), agentCommand("later", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`}))
	e.must("deck.run_later", map[string]any{"command": "alpha.later", "at": t0.Add(90 * time.Minute).UTC().Format(time.RFC3339)}, nil)
	var job, once int64
	for _, tr := range e.triggers() {
		if tr.Kind == "once" {
			once = tr.ID
		} else {
			job = tr.ID
		}
	}
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
	var later struct{ Process int64 }
	e.must("deck.trigger_run", map[string]any{"trigger": once}, &later)
	e.waitState(later.Process, "done")
	for _, tr := range e.triggers() {
		if tr.ID == once {
			t.Fatal("a run later that ran now is off the schedule")
		}
	}
}

// TestRunNowTakesACommandOnlyTriggersCallAndTheScheduleHearsAboutRuns: run now has the trigger's rights, and
// a run of a trigger tells the schedule to read its next run and last result again.
func TestRunNowTakesACommandOnlyTriggersCallAndTheScheduleHearsAboutRuns(t *testing.T) {
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

// TestARunLaterThatFailsToStartIsKept: a run later whose start fails stays in the schedule, tries again later and
// shows the reason in the health of its project until it starts.
func TestARunLaterThatFailsToStartIsKept(t *testing.T) {
	e := newClockEnv(t, ok)
	text := testManifest([]string{agentCommand("remind", `{"type": "object", "properties": {"what": {"type": "string"}}}`)}, nil)
	e.createProject("later", text)
	e.must("deck.run_later", map[string]any{"command": "later.remind", "at": e.Clock.Now().Add(time.Hour).Format(time.RFC3339), "params": `{"what": "call"}`}, nil)
	e.must("deck.manifest_save", map[string]any{"project": "later", "manifest": strings.Replace(text, `"what": {"type": "string"}`, `"what": {"type": "integer"}`, 1)}, nil)
	e.Clock.Add(time.Hour)
	e.tick()
	if ts := e.triggers(); len(ts) != 1 || e.countOf("remind") != 0 {
		t.Fatalf("a run later that did not start stays in the schedule: %+v", ts)
	}
	if h := e.waitHealth("trigger:later.remind", "error"); !strings.Contains(h.Error, "what") {
		t.Fatalf("the health of the project says why it did not start: %+v", h)
	}
	e.must("deck.manifest_save", map[string]any{"project": "later", "manifest": text}, nil)
	e.Clock.Add(time.Hour)
	e.tick()
	e.waitFor("the run once it starts", func() bool { return e.countOf("remind") == 1 })
	if len(e.triggers()) != 0 {
		t.Fatal("a run later that started leaves the schedule")
	}
	var hs []healthRow
	e.must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if h.Subject == "trigger:later.remind" {
			t.Fatalf("the health row goes once it starts: %+v", h)
		}
	}
	var r struct{ Trigger int64 }
	e.must("deck.run_later", map[string]any{"command": "later.remind", "at": e.Clock.Now().Add(time.Hour).Format(time.RFC3339), "params": `{"what": "call"}`}, &r)
	e.must("deck.manifest_save", map[string]any{"project": "later", "manifest": strings.Replace(text, `"what": {"type": "string"}`, `"what": {"type": "integer"}`, 1)}, nil)
	e.Clock.Add(time.Hour)
	e.tick()
	e.waitHealth("trigger:later.remind", "error")
	e.must("deck.run_later_cancel", map[string]any{"trigger": r.Trigger}, nil)
	e.must("deck.health_list", nil, &hs)
	for _, h := range hs {
		if h.Subject == "trigger:later.remind" {
			t.Fatalf("the health row goes with the cancelled run later: %+v", h)
		}
	}
}
