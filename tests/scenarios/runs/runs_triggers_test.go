package runs

import (
	"context"
	"deck2/internal/events"
	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/internal/registry"
	"deck2/tests/harness"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestADraftsScheduleSaysItDoesNotRun: a trigger of a draft has no next run and says why.
func TestADraftsScheduleSaysItDoesNotRun(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	draft := strings.Replace(harness.PlainManifest, `"triggers": []`, `"triggers": [{"command": "work", "cron": "30 * * * *"}]`, 1)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), []byte(draft), 0o600); err != nil {
		t.Fatal(err)
	}
	e.Must("deck.project_import", map[string]any{"folder": dir, "name": "bench"}, nil)
	var ts []struct {
		Project   string
		NextRunAt string `json:"next_at"`
		Draft     bool
	}
	e.Must("deck.triggers_list", map[string]any{"project": "bench"}, &ts)
	if len(ts) != 1 || !ts[0].Draft || ts[0].NextRunAt != "" {
		t.Fatalf("a draft's trigger shows no next run: %+v", ts)
	}
}

func TestANewModuleWithoutCode(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		switch c.Command() {
		case "collect":
			writeIdeas(ctx, c, "x")
			return provider.Result{Structured: json.RawMessage(`{"written": 3}`)}
		default:
			var in struct{ Process int64 }
			_ = json.Unmarshal([]byte(harness.InputOf.FindStringSubmatch(c.Req.Prompt)[1]), &in)
			out, _ := c.Tool(ctx, "process_get", map[string]any{"process": in.Process})
			var p struct{ Result struct{ Written int } }
			_ = json.Unmarshal([]byte(out), &p)
			return provider.Result{Structured: json.RawMessage(fmt.Sprintf(`{"seen": %d}`, p.Result.Written))}
		}
	}), harness.WithClock())

	m := strings.Replace(harness.IdeasManifest, `"triggers": [{"command": "collect", "cron": "0 9 * * *"}]`,
		`"triggers": [{"command": "collect", "cron": "0 9 * * *"}, {"command": "digest", "after": "collect"}]`, 1)
	m = strings.Replace(m, `"commands": [{`, `"commands": [{"name": "digest", "title": "Digest", "description": "Sums up a collection run", "class": "internal",
    "input": `+harness.ProcessField+`, "executor": {"type": "agent", "prompt": "Sum up."},
    "result": {"type": "object", "properties": {"seen": {"type": "integer"}}, "required": ["seen"]}}, {`, 1)
	p := e.Project("ideas", m)
	e.Clock.Add(time.Hour)
	e.Tick()
	var digest harness.Proc
	e.Until("the digest after the scheduled collection to end", func() bool {
		for _, pr := range e.Procs() {
			if pr.Command == "digest" {
				digest = pr
			}
		}
		return e.CountOf("digest") == 1 && (digest.State == "done" || digest.State == "error")
	})
	collect := e.Proc(digest.Parent)
	if digest.State != "done" || string(digest.Result) != `{"seen":3}` || collect.Command != "collect" || collect.Trigger == 0 || digest.Depth != 2 {
		t.Fatalf("the finish of collect must start digest with the finished process: %+v %+v", digest, collect)
	}
	if rows := e.ScreenRows(p, "main"); len(rows) != 2 {
		t.Fatalf("the screen shows the written records, got %v", rows)
	}
	e.WaitState(e.Run(p, "collect"), "done")
	e.Until("the second digest after the button", func() bool { return e.CountOf("digest") == 2 })
}

func TestTriggerChainStopsAtNinthStep(t *testing.T) {
	t.Parallel()
	var cmds, trigs []string
	for i := 1; i <= 9; i++ {
		in := harness.ProcessField
		if i == 1 {
			in = ""
		} else {
			trigs = append(trigs, fmt.Sprintf(`{"command": "c%d", "after": "c%d"}`, i, i-1))
		}
		cmds = append(cmds, harness.AgentCommand(fmt.Sprintf("c%d", i), in))
	}
	e := harness.New(t, harness.WithAgent(harness.Ok))
	p := e.Project("chain", harness.TestManifest(cmds, trigs))
	e.Run(p, "c1")
	n := e.WaitNote("chain.c9")
	e.Settle()
	if !n.CanRead || !strings.Contains(n.Text, "c1 → c2 → c3 → c4 → c5 → c6 → c7 → c8") {
		t.Fatalf("the ninth step is refused with a note naming the chain, which the owner marks read: %+v", n)
	}
	var as []harness.Ask
	if e.Must("deck.asks_list", nil, &as); len(as) != 0 {
		t.Fatalf("a warning waits for no answer, so it is not among the asks: %+v", as)
	}
	ps := e.Procs()
	if len(ps) != 8 || e.CountOf("c9") != 0 {
		t.Fatalf("eight steps run, the ninth does not: %+v", ps)
	}
	for _, pr := range ps {
		if want := int64(pr.Command[1] - '0'); pr.Depth != want || pr.State != "done" {
			t.Fatalf("depth of %s must be %d: %+v", pr.Command, want, pr)
		}
	}
	e.Must("deck.feed_read", map[string]any{"entry": n.ID}, nil)
	if e.Warnings("chain.c9") != 0 {
		t.Fatalf("a note marked read stays read: %+v", e.Notes())
	}
}

func TestScheduledRunDoesNotOverlapItsPrevious(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		<-release
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}), harness.WithClock())

	e.Project("daily", harness.TestManifest([]string{harness.AgentCommand("job", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`}))
	e.Clock.Add(time.Hour)
	e.Tick()
	e.Until("the first run", func() bool { return e.CountOf("job") == 1 })
	e.Clock.Add(24 * time.Hour)
	e.Tick()
	if n := e.CountOf("job"); n != 1 {
		t.Fatalf("a trigger does not start while its previous run goes on, got %d runs", n)
	}
	close(release)
	e.Settle()
	e.Clock.Add(24 * time.Hour)
	e.Tick()
	e.Until("the next run once the previous finished", func() bool { return e.CountOf("job") == 2 })
}

func TestEmptyResultStartsNothingAndIsPurgedAfterADay(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		return provider.Result{Structured: json.RawMessage(`null`)}
	}), harness.WithClock())

	p := e.Project("quiet", harness.Nullable(harness.TestManifest([]string{harness.AgentCommand("check", ""), harness.AgentCommand("notify", harness.ProcessField)},
		[]string{`{"command": "notify", "after": "check"}`})))
	id := e.Run(p, "check")
	e.WaitState(id, "done")
	e.Settle()
	if got := e.Proc(id); !got.Empty || string(got.Result) != "null" || e.CountOf("notify") != 0 {
		t.Fatalf("an empty result is done, collapsed and starts no after trigger: %+v, %d", got, e.CountOf("notify"))
	}
	e.Clock.Add(23 * time.Hour)
	e.Sweep()
	if e.Proc(id).ID != id {
		t.Fatal("an empty process stays for a day")
	}
	e.Clock.Add(2 * time.Hour)
	e.Sweep()
	e.Refused("deck.process_get", map[string]any{"process": id}, 404, "no process")
	if _, err := os.Stat(filepath.Join(deckhome.Layout(e.App.Cfg.Home).Work(), "runs", fmt.Sprint(id))); !os.IsNotExist(err) {
		t.Fatalf("the folder of a purged process goes with it: %v", err)
	}
	kept := e.Run(p, "check")
	e.WaitState(kept, "done")
	e.Settle()
	e.Clock.Add(8 * 24 * time.Hour)
	e.Sweep()
	if _, err := os.Stat(filepath.Join(deckhome.Layout(e.App.Cfg.Home).Work(), "runs", fmt.Sprint(kept))); !os.IsNotExist(err) {
		t.Fatalf("the run folder and log of a process finished a week ago are cleared: %v", err)
	}
}

func TestMissedRunsAreSpreadAfterSleep(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok), harness.WithClock())
	e.Project("many", harness.TestManifest([]string{harness.AgentCommand("a", ""), harness.AgentCommand("b", ""), harness.AgentCommand("c", ""), harness.AgentCommand("d", "")},
		[]string{`{"command": "a", "cron": "0 9 * * *"}`, `{"command": "b", "cron": "0 9 * * *"}`, `{"command": "c", "cron": "0 9 * * *"}`,
			`{"command": "d", "cron": "0 9 * * *", "skip_missed": true}`}))
	wake := e.Clock.Add(72 * time.Hour)
	e.Tick()
	e.Settle()
	if n := len(e.Procs()); n != 1 {
		t.Fatalf("after a sleep the missed runs start one at a time, got %d", n)
	}
	next := map[string]string{}
	for _, tr := range e.Triggers() {
		next[tr.Command] = tr.NextRunAt
	}
	slot := func(d time.Duration) string { return wake.Add(d).UTC().Format("2006-01-02T15:04:05.000Z") }
	if next["b"] != slot(time.Minute) || next["c"] != slot(2*time.Minute) || next["d"] != slot(time.Hour) {
		t.Fatalf("missed runs get slots a minute apart; a trigger that skips missed runs moves on: %v", next)
	}
	for i := 2; i <= 3; i++ {
		e.Clock.Add(time.Minute)
		e.Tick()
		e.Settle()
		if n := len(e.Procs()); n != i {
			t.Fatalf("slot %d: want %d runs, got %d", i, i, n)
		}
	}
	e.Tick()
	if e.CountOf("d") != 0 || len(e.Procs()) != 3 {
		t.Fatal("each missed trigger runs exactly once, a skipping one not at all")
	}
}

func TestPausedTriggersAndProjectsDoNotRun(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok), harness.WithClock())
	m := harness.TestManifest([]string{harness.AgentCommand("job", ""), harness.AgentCommand("other", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`})
	e.Project("paused", m)
	tr := e.Triggers()[0]
	e.Must("deck.trigger_pause", map[string]any{"trigger": tr.ID, "paused": true}, nil)
	e.Clock.Add(4 * time.Hour)
	e.Tick()
	e.Must("deck.trigger_pause", map[string]any{"trigger": tr.ID, "paused": false}, nil)
	e.Tick()
	if e.CountOf("job") != 0 {
		t.Fatal("a paused trigger does not run, and resuming it does not catch up the pause")
	}
	if tomorrow := e.Triggers()[0].NextRunAt; tomorrow != time.Date(2026, 9, 26, 9, 0, 0, 0, time.Local).UTC().Format("2006-01-02T15:04:05.000Z") {
		t.Fatalf("a resumed trigger waits for its next slot: %s", tomorrow)
	}
	e.Must("deck.project_pause", map[string]any{"project": "paused", "paused": true}, nil)
	e.Must("deck.manifest_save", map[string]any{"project": "paused", "manifest": strings.Replace(m, `"triggers": [`,
		`"triggers": [{"command": "other", "cron": "0 9 * * *"}, `, 1)}, nil)
	e.Clock.Add(24 * time.Hour)
	e.Tick()
	if ts := e.Triggers(); len(e.Procs()) != 0 || len(ts) != 2 || !ts[0].Paused || !ts[1].Paused {
		t.Fatalf("a paused project runs no trigger, also one added later: %+v", ts)
	}
	e.Must("deck.project_pause", map[string]any{"project": "paused", "paused": false}, nil)
	e.Clock.Add(21 * time.Hour)
	e.Tick()
	e.Until("both triggers after the project goes on", func() bool { return len(e.Procs()) == 2 })
}

func TestPausedTriggersStartNoAfterTriggers(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	p := e.Project("chain", harness.TestManifest([]string{harness.AgentCommand("a", ""), harness.AgentCommand("b", harness.ProcessField)}, []string{`{"command": "b", "after": "a"}`}))
	for _, tr := range e.Triggers() {
		if tr.Command == "b" {
			e.Must("deck.trigger_pause", map[string]any{"trigger": tr.ID, "paused": true}, nil)
		}
	}
	e.WaitState(e.Run(p, "a"), "done")
	e.Settle()
	if e.CountOf("b") != 0 {
		t.Fatal("a paused after trigger starts nothing")
	}
}

func TestTriggerIdentityAndReservedRetryKey(t *testing.T) {
	t.Parallel()
	e := harness.New(t)
	code, out := e.Call("deck.project_create", map[string]any{"name": "twice", "manifest": harness.TestManifest(
		[]string{harness.AgentCommand("pack", `{"type": "object", "properties": {"tone": {"type": "string"}}}`)},
		[]string{`{"command": "pack", "cron": "0 9 * * *", "params": {"tone": "short"}}`, `{"command": "pack", "cron": "0 9 * * *", "params": {"tone": "long"}}`})})
	if code != 422 || !strings.Contains(string(out), `same cron "0 9 * * *"`) {
		t.Fatalf("one command has one trigger per schedule: %d %s", code, out)
	}
	code, out = e.Call("deck.project_create", map[string]any{"name": "keyed", "manifest": harness.TestManifest([]string{
		harness.AgentCommand("job", `{"type": "object", "properties": {"retry_key": {"type": "string"}}}`)}, nil)})
	if code != 422 || !strings.Contains(string(out), "reserved") {
		t.Fatalf("retry_key is reserved: %d %s", code, out)
	}
	e.Project("jobs", harness.TestManifest([]string{harness.AgentCommand("job", `{"type": "object", "properties": {}, "additionalProperties": false}`)}, nil))
	var job harness.CommandView
	e.Must("deck.command_describe", map[string]any{"command": "jobs.job"}, &job)
	if !strings.Contains(string(job.Input), `"retry_key"`) {
		t.Fatalf("every project command takes a retry key: %s", job.Input)
	}
}

func TestPauseBetweenReadingAndFiringATriggerHolds(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok), harness.WithClock())
	pid := e.Project("race", harness.TestManifest([]string{harness.AgentCommand("job", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`}))
	rows, err := e.App.Store.Q.ListProjectTriggers(context.Background(), pid)
	if err != nil || len(rows) != 1 {
		t.Fatal(err)
	}
	e.Must("deck.project_pause", map[string]any{"project": "race", "paused": true}, nil)
	if _, err := e.App.Triggers.Fire(context.Background(), rows[0], map[string]any{}); err == nil || !strings.Contains(err.Error(), "paused") {
		t.Fatalf("a trigger read before its project paused does not start: %v", err)
	}
	e.Must("deck.project_pause", map[string]any{"project": "race", "paused": false}, nil)
	e.Must("deck.trigger_pause", map[string]any{"trigger": rows[0].ID, "paused": true}, nil)
	if _, err := e.App.Triggers.Fire(context.Background(), rows[0], map[string]any{}); err == nil {
		t.Fatal("a trigger read before it paused does not start")
	}
	if n := e.CountOf("job"); n != 0 {
		t.Fatalf("no process started: %d", n)
	}
}

func TestAScheduledCallTheOwnerAllowedIsTheLastRunOfItsTrigger(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}), harness.WithClock())

	defer close(release)
	pub := strings.Replace(harness.AgentCommand("publish", ""), `"class": "internal"`, `"class": "external"`, 1)
	e.Project("daily", harness.TestManifest([]string{pub}, []string{`{"command": "publish", "cron": "0 9 * * *"}`}))
	e.Clock.Add(time.Hour)
	e.Tick()
	a := e.WaitAsk()
	if a.Kind != "call" || e.CountOf("publish") != 0 {
		t.Fatalf("an external command of a schedule waits for the owner: %+v", a)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := e.Events(ctx)
	e.Must("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}, nil)
	harness.WaitEvent(t, stream, events.Event{Topic: "triggers"})
	e.Until("the allowed run", func() bool { return e.CountOf("publish") == 1 })
	ps := e.Procs()
	if ts := e.Triggers(); len(ts) != 1 || ts[0].Last == 0 || ts[0].Last != ps[0].ID {
		t.Fatalf("the run the owner allowed is the last run of its trigger: %+v %+v", ts, ps)
	}
}

func TestAnAfterTriggerCallTheOwnerAllowedKeepsItsParent(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok))
	pub := strings.Replace(harness.AgentCommand("publish", `{"type": "object", "properties": {"process": {"type": "integer"}}}`), `"class": "internal"`, `"class": "external"`, 1)
	e.Project("chain", harness.TestManifest([]string{harness.AgentCommand("first", ""), pub}, []string{`{"command": "publish", "after": "first"}`}))
	first := e.Start("chain", "first", nil)
	e.WaitState(first, "done")
	a := e.WaitAsk()
	e.Must("deck.ask_answer", map[string]any{"ask": a.ID, "answer": "allow"}, nil)
	e.Until("the allowed run", func() bool { return e.CountOf("publish") == 1 })
	for _, p := range e.Procs() {
		if p.Command == "publish" && (p.Parent != first || p.Depth != 2) {
			t.Fatalf("the run the owner allowed is the child of the run that fired it: %+v", p)
		}
	}
}

// TestScheduleShowsEveryTriggerRunsOneNowAndPausesAProject: the schedule lists the triggers of all projects;
// run now starts one and keeps its slot; a paused project runs nothing on schedule; the history filters.
func TestScheduleShowsEveryTriggerRunsOneNowAndPausesAProject(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok), harness.WithClock())
	check := harness.NewCatalogCheck(t)
	e.Project("alpha", harness.TestManifest([]string{harness.AgentCommand("job", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`}))
	e.Project("beta", harness.TestManifest([]string{harness.AgentCommand("sync", "")}, []string{`{"command": "sync", "cron": "0 10 * * *"}`}))
	check.Messages(t, e.ScreenMessages("", "schedule", nil))
	rows := func() []harness.ScreenRow {
		var out []harness.ScreenRow
		for _, r := range harness.BlockData[[]harness.ScreenRow](t, e.ScreenData("", "schedule", nil)["b0-0-1"]["rows"]) {
			if !strings.HasSuffix(fmt.Sprint(r.Cells[0]), " · master") {
				out = append(out, r)
			}
		}
		return out
	}
	find := func(rs []harness.ScreenRow, project string) harness.ScreenRow {
		for _, r := range rs {
			if strings.HasSuffix(fmt.Sprint(r.Cells[0]), " · "+project) {
				return r
			}
		}
		t.Fatalf("no trigger of %s in %+v", project, rs)
		return harness.ScreenRow{}
	}
	state := func(project string) (word, last, next string) {
		var ts []struct {
			Word      string `json:"word"`
			LastWord  string `json:"last_word"`
			NextRunAt string `json:"next_at"`
		}
		e.Must("deck.triggers_list", map[string]any{"project": project}, &ts)
		return ts[0].Word, ts[0].LastWord, ts[0].NextRunAt
	}
	button := func(r harness.ScreenRow, verb string) map[string]any {
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
		e.Must(a["command"].(string), a["params"], out)
	}
	rs := rows()
	job := find(rs, "alpha")
	if len(rs) != 2 || job.Cells[0] != "job · alpha" || job.Cells[1] != "0 9 * * *" || job.Cells[4] != "fake" {
		t.Fatalf("every trigger of every project with its command, how often and its model: %+v", rs)
	}
	if m := button(job, "model"); m["pick"] != "model" || m["params"].(map[string]any)["command"] != "job" || m["params"].(map[string]any)["project"] != "alpha" {
		t.Fatalf("the model of a trigger is picked in its row: %+v", m)
	}
	_, _, next := state("alpha")

	var started struct{ Process int64 }
	press(button(job, "run_now"), &started)
	e.WaitState(started.Process, "done")
	if _, last, now := state("alpha"); last != "done" || now != next {
		t.Fatalf("run now starts the command and keeps its slot: %s %s", last, now)
	}
	button(find(rows(), "alpha"), "open")

	e.Must("deck.project_pause", map[string]any{"project": "alpha", "paused": true}, nil)
	if word, _, _ := state("alpha"); word != "module paused" || fmt.Sprint(find(rows(), "alpha").Tones) != "[muted muted muted ok <nil> <nil>]" {
		t.Fatalf("a paused project shows on its triggers: %s %+v", word, find(rows(), "alpha"))
	}
	e.Clock.Add(26 * time.Hour)
	e.Tick()
	e.Until("beta on its schedule", func() bool { return e.CountOf("sync") >= 1 })
	e.Settle()
	if n := e.CountOf("job"); n != 1 {
		t.Fatalf("a paused project runs nothing on schedule, only what the owner ran: %d", n)
	}
	e.Must("deck.project_pause", map[string]any{"project": "alpha", "paused": false}, nil)
	press(button(find(rows(), "alpha"), "pause"), nil)
	if word, _, _ := state("alpha"); word != "paused" {
		t.Fatalf("a trigger pauses alone: %s", word)
	}
	press(button(find(rows(), "alpha"), "resume"), nil)

	history := e.ScreenData("", "schedule", map[string]any{"filter": `{"b0-1-0": {"project": "alpha"}}`})["b0-1-0"]
	hrows := harness.BlockData[[]harness.ScreenRow](t, history["rows"])
	if len(hrows) != 1 || hrows[0].Cells[1] != "alpha" || !strings.Contains(string(history["filters"]), `"value":"alpha"`) {
		t.Fatalf("the history filters by project: %+v %s", hrows, history["filters"])
	}
	if states := harness.BlockData[[]harness.ScreenRow](t, e.ScreenData("", "schedule", map[string]any{"filter": `{"b0-1-0": {"state": "error"}}`})["b0-1-0"]["rows"]); len(states) != 0 {
		t.Fatalf("the history filters by state: %+v", states)
	}
	e.Refused("deck.trigger_run", map[string]any{"trigger": 999}, 404, "no trigger")
}

// TestRunNowReturnsTheRunStillGoing: a second run now while the first goes gives that run.
func TestRunNowReturnsTheRunStillGoing(t *testing.T) {
	t.Parallel()
	gate := make(chan struct{})
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "job" {
			select {
			case <-gate:
			case <-ctx.Done():
			}
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}), harness.WithClock())

	e.Project("alpha", harness.TestManifest([]string{harness.AgentCommand("job", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`}))
	job := e.Triggers()[0].ID
	var first, second struct{ Process int64 }
	e.Must("deck.trigger_run", map[string]any{"trigger": job}, &first)
	e.Must("deck.trigger_run", map[string]any{"trigger": job}, &second)
	if first.Process == 0 || second.Process != first.Process || e.CountOf("job") != 1 {
		t.Fatalf("run now while the run goes gives that run: %d %d", first.Process, second.Process)
	}
	for _, tr := range e.Triggers() {
		if tr.ID == job && tr.Last != first.Process {
			t.Fatalf("a run now is the last run of its trigger: %+v", tr)
		}
	}
	close(gate)
	e.WaitState(first.Process, "done")
}

// TestRunNowTakesACommandOnlyTriggersCallAndTheScheduleHearsAboutRuns: run now has the trigger's rights, and
// a run of a trigger tells the schedule to read its next run and last result again.
func TestRunNowTakesACommandOnlyTriggersCallAndTheScheduleHearsAboutRuns(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok), harness.WithClock())
	own := strings.Replace(harness.AgentCommand("digest", ""), `"class": "internal"`, `"class": "internal", "callers": ["trigger"]`, 1)
	e.Project("alpha", harness.TestManifest([]string{own}, []string{`{"command": "digest", "cron": "0 9 * * *"}`}))
	tr := e.Triggers()[0]
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream := e.Events(ctx)
	var started struct{ Process int64 }
	e.Must("deck.trigger_run", map[string]any{"trigger": tr.ID}, &started)
	e.WaitState(started.Process, "done")
	harness.WaitEvent(t, stream, events.Event{Topic: "triggers"})
	e.Clock.Add(2 * time.Hour)
	e.Tick()
	e.Until("the scheduled run", func() bool { return e.CountOf("digest") == 2 })
	harness.WaitEvent(t, stream, events.Event{Topic: "triggers"})
}

func TestTriggerParamsKeepLargeIntegers(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Idle), harness.WithClock())
	m := strings.Replace(harness.CalcManifest, `"executor": {"type": "agent", "prompt": "OLD PROMPT"}}]`,
		`"executor": {"type": "agent", "prompt": "OLD PROMPT"}}],
  "triggers": [{"command": "calc", "cron": "0 9 * * *", "params": {"n": 9007199254740993}}]`, 1)
	e.Project("nums", m)
	e.Clock.Add(48 * time.Hour)
	e.App.Scheduler.Tick(context.Background())
	e.App.Runner.Wait()
	ps := e.Procs()
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
	e := harness.New(t, harness.WithAgent(harness.Ok), harness.WithClock())
	text := strings.Replace(harness.TestManifest([]string{harness.AgentCommand("remind", "")}, []string{`{"command": "remind", "cron": "0 * * * *"}`}),
		`"prompt": "Do remind."`, `"prompt_file": "prompts/remind.md"`, 1)
	e.Project("later", text)
	e.Clock.Add(time.Hour)
	e.Tick()
	if h := e.WaitHealth("trigger:later.remind", "error"); !strings.Contains(h.Error, "prompts/remind.md") || e.CountOf("remind") != 0 {
		t.Fatalf("the health of the project says why it did not start: %+v", h)
	}
	e.Must("deck.document_save", map[string]any{"project": "later", "name": "prompts/remind.md", "kind": "prompt", "text": "Remind."}, nil)
	e.Clock.Add(time.Hour)
	e.Tick()
	e.Until("the run once it starts", func() bool { return e.CountOf("remind") == 1 })
	for _, h := range e.Health() {
		if h.Subject == "trigger:later.remind" {
			t.Fatalf("the health row goes once it starts: %+v", h)
		}
	}
}

// TestEnablingADraftClearsWhyItsTriggersDidNotStart: a schedule of a draft does not start and says why in health;
// once the owner enables the project the reason is gone, and so is the red row.
func TestEnablingADraftClearsWhyItsTriggersDidNotStart(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok), harness.WithClock())
	_, tok := e.Token("terminal", "deck.*")
	m := harness.TestManifest([]string{harness.AgentCommand("remind", "")}, []string{`{"command": "remind", "cron": "0 * * * *"}`})
	if code, out := e.As(tok).Call("deck.project_create", map[string]any{"name": "later", "manifest": m}); code != 200 {
		t.Fatalf("project_create: %d %s", code, out)
	}
	e.Clock.Add(time.Hour)
	e.Tick()
	if h := e.WaitHealth("trigger:later.remind", "error"); !strings.Contains(h.Error, "draft") {
		t.Fatalf("a schedule of a draft says why it did not start: %+v", h)
	}
	e.Must("deck.project_enable", map[string]any{"project": "later", "revision": e.ProjectRow("later").Revision}, nil)
	for _, h := range e.Health() {
		if h.Subject == "trigger:later.remind" {
			t.Fatalf("enabling the project clears why its schedule did not start: %+v", h)
		}
	}
}

// TestAHungToolIsSeenAndStopsAtItsCeiling: a tool call that does not answer holds a ceiling like a script; a cron slot
// it swallows is a warning in health until a run starts, and the stop at the ceiling is an error.
func TestAHungToolIsSeenAndStopsAtItsCeiling(t *testing.T) {
	t.Parallel()
	slow := harness.NewService(t, func(w http.ResponseWriter, r *http.Request, n int) { <-r.Context().Done() })
	e := harness.New(t, harness.WithClock())
	e.Project("feed", fmt.Sprintf(`{"version": 13, "commands": [{"name": "fetch", "title": "Fetch", "description": "Fetches the feed.",
	  "class": "internal", "executor": {"type": "tool", "tool": "pull"}}],
	  "tools": {"http": [{"name": "pull", "description": "Pulls the feed", "method": "GET", "url": "%s/feed", "class": "internal"}]},
	  "triggers": [{"command": "fetch", "cron": "0 9 * * *"}]}`, slow.URL))
	e.Clock.Add(time.Hour)
	e.Tick()
	e.Until("the first run", func() bool { return e.CountOf("fetch") == 1 })
	id := e.Procs()[0].ID
	e.Until("the call at the service", func() bool { return len(slow.Requests()) == 1 })
	if e.Proc(id).Deadline == "" {
		t.Fatalf("a tool call holds a ceiling: %+v", e.Proc(id))
	}
	e.Clock.Add(24 * time.Hour)
	e.Tick()
	if h := e.WaitHealth("trigger:feed.fetch", "warning"); !strings.Contains(h.Error, fmt.Sprint(id)) || e.CountOf("fetch") != 1 {
		t.Fatalf("a slot the run still going swallowed is a warning naming that run: %+v", h)
	}
	e.Sweep()
	if got := e.WaitState(id, "error"); !strings.Contains(got.Error, "ceiling") {
		t.Fatalf("a stop at the ceiling is an error, not a stop of the owner: %+v", got)
	}
	e.Clock.Add(24 * time.Hour)
	e.Tick()
	e.Until("the next run", func() bool { return e.CountOf("fetch") == 2 })
	for _, h := range e.Health() {
		if h.Subject == "trigger:feed.fetch" {
			t.Fatalf("the warning goes once a run starts: %+v", h)
		}
	}
}

// TestRunNowKeepsTheRightsOfItsCaller: a token runs now only a command it may call itself, and only the owner
// runs a command of a paused project now.
func TestRunNowKeepsTheRightsOfItsCaller(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok), harness.WithClock())
	e.Project("alpha", harness.TestManifest([]string{harness.AgentCommand("c1", "")}, []string{`{"command": "c1", "cron": "0 9 * * *"}`}))
	job := e.Triggers()[0].ID
	_, narrow := e.Token("narrow", "deck.trigger_run")
	if code, out := e.As(narrow).Call("deck.trigger_run", map[string]any{"trigger": job}); code != 403 || e.CountOf("c1") != 0 {
		t.Fatalf("a token that may not call the command does not run it now: %d %s, %d runs", code, out, e.CountOf("c1"))
	}
	_, wide := e.Token("wide", "deck.trigger_run", "alpha.*")
	if code, out := e.As(wide).Call("deck.trigger_run", map[string]any{"trigger": job}); code != 200 || e.CountOf("c1") != 1 {
		t.Fatalf("a token that may call the command runs it now: %d %s", code, out)
	}
	e.Must("deck.project_pause", map[string]any{"project": "alpha", "paused": true}, nil)
	master := registry.Actor{Kind: registry.Master, Workspace: 1}
	if _, err := e.App.Gateway.CallAs(context.Background(), master, "deck.trigger_run", []byte(fmt.Sprintf(`{"trigger": %d}`, job))); err == nil ||
		!strings.Contains(err.Error(), "paused") || e.CountOf("c1") != 1 {
		t.Fatalf("the master does not run a command of a paused project now: %v, %d runs", err, e.CountOf("c1"))
	}
	e.Settle()
	var started struct{ Process int64 }
	e.Must("deck.trigger_run", map[string]any{"trigger": job}, &started)
	if e.WaitState(started.Process, "done"); e.CountOf("c1") != 2 {
		t.Fatalf("the owner runs it now in a pause: %d runs", e.CountOf("c1"))
	}
}

// TestAScheduleThatFailedIsFixedByALaterRunOfTheSameWork: the last run of a trigger that failed stops being red once
// a run of the same command with the same input works, as in the cockpit.
func TestAScheduleThatFailedIsFixedByALaterRunOfTheSameWork(t *testing.T) {
	t.Parallel()
	var fail atomic.Bool
	fail.Store(true)
	e := harness.New(t, harness.WithAgent(func(context.Context, *harness.Call) provider.Result {
		if fail.Load() {
			return provider.Result{Outcome: provider.Failed, Text: "down"}
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}), harness.WithClock())

	p := e.Project("alpha", harness.TestManifest([]string{harness.AgentCommand("job", "")}, []string{`{"command": "job", "cron": "0 9 * * *"}`}))
	last := func() (string, string) {
		var ts []struct {
			LastWord string `json:"last_word"`
			LastTone string `json:"last_tone"`
		}
		e.Must("deck.triggers_list", map[string]any{"project": "alpha"}, &ts)
		return ts[0].LastWord, ts[0].LastTone
	}
	var started struct{ Process int64 }
	e.Must("deck.trigger_run", map[string]any{"trigger": e.Triggers()[0].ID}, &started)
	e.WaitState(started.Process, "error")
	if _, tone := last(); tone != "error" {
		t.Fatalf("a schedule whose last run failed is red: %s", tone)
	}
	fail.Store(false)
	e.WaitState(e.Run(p, "job"), "done")
	if word, tone := last(); tone != "muted" || word != "failed, fixed since" {
		t.Fatalf("a later run of the same work that went well fixes the schedule too: %s %s", word, tone)
	}
}
