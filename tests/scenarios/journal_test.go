package scenarios

import (
	"context"
	deckhome "deck2/internal/home"
	"encoding/json"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
)

// TestJournalShowsTheCallsOfTheGatewayTheSpendFilesAndSnapshots: the journal screen reads the gateway journal
// with its outcomes, the tokens by command and provider, the files and the snapshots; it only shows them.
func TestJournalShowsTheCallsOfTheGatewayTheSpendFilesAndSnapshots(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "tell" {
			c.Tool(ctx, "web__publish", map[string]any{})
			c.Req.OnUsage(provider.Usage{RequestModel: "big", Model: "big", Input: 1200, Output: 300})
			c.Req.OnUsage(provider.Usage{RequestModel: "big", Model: "big", Input: 800, Output: 100})
		}
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	check := newCatalogCheck(t)
	e.createProject("web", webNotes)
	e.createProject("quiet", webNotes)
	tell := e.start("web", "tell", nil)
	e.waitState(tell, "waiting_answer")
	scan := e.upload("web", "scan.txt", "text/plain", "a page")
	e.waitState(e.start("web", "score", map[string]any{"title": "x", "scan": fmt.Sprint(scan)}), "done")
	e.must("deck.snapshot_create", nil, nil)
	if err := os.WriteFile(filepath.Join(deckhome.Layout(e.App.Cfg.Home).State(), "backups", "deck-20200101T000000.tar.gz"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}

	var calls []struct {
		Tool, Actor, Project, Outcome string
	}
	e.must("deck.calls_list", map[string]any{"project": "web"}, &calls)
	waiting := false
	for _, c := range calls {
		waiting = waiting || c.Tool == "web.publish" && c.Outcome == "waiting" && c.Project == "web" && strings.HasPrefix(c.Actor, "agent:")
	}
	if !waiting {
		t.Fatalf("the journal has the external call that waits for the owner: %+v", calls)
	}
	e.answer(e.waitAsk().ID, "deny")
	e.must("deck.calls_list", map[string]any{"project": "web"}, &calls)
	outcomes := map[string]bool{}
	for _, c := range calls {
		outcomes[c.Outcome] = true
	}
	if !outcomes["rejected"] || !outcomes["waiting"] {
		t.Fatalf("the journal keeps each outcome of a call: %+v", calls)
	}
	var rejected []map[string]any
	e.must("deck.calls_list", map[string]any{"outcome": "rejected"}, &rejected)
	if len(rejected) != 1 || rejected[0]["outcome"] != "rejected" || rejected[0]["_actions"] != nil {
		t.Fatalf("the journal narrows by outcome and gives only its own fields: %+v", rejected)
	}
	var none []any
	e.must("deck.calls_list", map[string]any{"project": "quiet"}, &none)
	if len(none) != 0 {
		t.Fatalf("the journal of a project has only its calls: %+v", none)
	}

	check.messages(t, e.screenMessages("", "process", map[string]any{"record": tell}))
	page := e.screenData("", "process", map[string]any{"record": tell})
	if rows := blockData[[]screenRow](t, page["b2"]["rows"]); len(rows) != 2 || rows[1].Cells[1] != "web.publish" || rows[1].Cells[2] != "waiting" || rows[0].Cells[2] != "rejected" || rows[0].Details[2] != "the owner said no" {
		t.Fatalf("the page of a run lists the calls it made with their outcomes: %+v", rows)
	}

	check.messages(t, e.screenMessages("", "spend", nil))
	data := e.screenData("", "spend", nil)
	spend := blockData[[]screenRow](t, data["b4-2-0"]["rows"])
	if len(spend) != 1 || spend[0].Cells[0] != "web" || spend[0].Cells[2] != float64(2) || spend[0].Cells[3] != float64(2400) {
		t.Fatalf("spend sums the calls and tokens of each model of each project: %+v", spend)
	}
	var by []struct {
		Provider string
		Tokens   int64
		Share    float64
		Runs     int64
	}
	e.must("deck.spend_daily", map[string]any{"by": "provider"}, &by)
	if len(by) != 1 || by[0].Tokens != 2400 || by[0].Share != 1 || by[0].Runs != 2 {
		t.Fatalf("spend by provider sums the tokens of the days with their share and the runs of the model: %+v", by)
	}
	var days []struct {
		Day    string
		Age    int64
		Tokens int64
	}
	e.must("deck.spend_daily", map[string]any{"days": 7}, &days)
	if len(days) != 7 || days[6].Age != 0 || days[0].Age != 6 {
		t.Fatalf("spend by day has every day of the window, today last: %+v", days)
	}
	kept := e.screenData("", "files", nil)
	files := blockData[[]map[string]any](t, kept["b0-0-0"]["files"])
	if len(files) != 1 || files[0]["name"] != "scan.txt" || files[0]["title"] != "scan.txt" {
		t.Fatalf("files lists the files of the workspace: %+v", files)
	}
	snaps := blockData[[]screenRow](t, kept["b0-3-0"]["rows"])
	if len(snaps) != 2 || !strings.HasPrefix(snaps[0].Cells[0].(string), "deck-2026") || snaps[1].Cells[1] != "2020-01-01T00:00:00Z" {
		t.Fatalf("snapshots lists what backups holds: %+v", snaps)
	}
	for _, r := range append(snaps, spend...) {
		if len(r.Actions) != 0 {
			t.Fatalf("the journal shows and restores nothing; only a file downloads: %+v", r)
		}
	}
	events := e.sse(t.Context())
	e.must("deck.snapshot_create", nil, nil)
	waitTopic(t, events, "snapshots")
}

// TestTheJournalKeepsHalfAYear: the daily cleanup drops calls older than 180 days and notes of the feed older than 90.
func TestTheJournalKeepsHalfAYear(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("notes", plainManifest)
	e.must("deck.notify", map[string]any{"project": "notes", "text": "done"}, nil)
	e.createProject("old", plainManifest)
	e.must("deck.project_delete", map[string]any{"project": "old"}, nil)
	count := func(table string) (n int) {
		_ = e.App.Store.DB.QueryRow(`SELECT count(*) FROM ` + table).Scan(&n)
		return n
	}
	calls, notes := count("tool_call"), count("feed WHERE kind = 'note'")
	if calls == 0 || notes != 1 {
		t.Fatalf("the calls and the line are kept: %d %d", calls, notes)
	}
	e.App.Sweep(context.Background(), time.Now().Add(91*24*time.Hour))
	if count("tool_call") != calls || count("feed WHERE kind = 'note'") != 0 {
		t.Fatalf("after 91 days the calls stay and the note goes: %d %d", count("tool_call"), count("feed WHERE kind = 'note'"))
	}
	e.App.Sweep(context.Background(), time.Now().Add(3*181*24*time.Hour))
	if count("tool_call") != 0 {
		t.Fatalf("after 181 days the calls go: %d", count("tool_call"))
	}
}

func TestStoppingAProcessJournalsTheCallsItDropped(t *testing.T) {
	t.Parallel()
	e := newEnv(t, nil)
	e.createProject("pics", toolManifest("http://127.0.0.1:1", ""))
	id := e.start("pics", "send", map[string]any{"text": "hi"})
	e.waitState(id, "waiting_answer")
	e.must("deck.process_stop", map[string]any{"process": id}, nil)
	if j := e.calls(id); !strings.Contains(j, "pics.note rejected") || !strings.Contains(j, "stopped") {
		t.Fatalf("a call dropped by a stop is in the journal:\n%s", j)
	}
}

// TestTheJournalFollowsCallsAndSpendLive: a call of the gateway and a model call are heard live, and each
// redraws only the blocks that read them.
func TestTheJournalFollowsCallsAndSpendLive(t *testing.T) {
	t.Parallel()
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		c.Tool(ctx, "document_save", map[string]any{"name": "report.md", "kind": "artifact", "text": "done"})
		c.Req.OnUsage(provider.Usage{RequestModel: "big", Model: "big", Input: 1200, Output: 300})
		return provider.Result{Structured: json.RawMessage(`{}`)}
	})
	e.createProject("web", webNotes)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	phone := e.sse(ctx)
	score := e.start("web", "score", map[string]any{"title": "x"})
	e.waitState(score, "done")
	waitTopic(t, phone, "calls")
	waitTopic(t, phone, "usage")
	for _, at := range []struct {
		topic, screen string
		params        map[string]any
	}{{"calls", "process", map[string]any{"record": score}}, {"usage", "spend", nil}} {
		params := map[string]any{"topic": at.topic}
		maps.Copy(params, at.params)
		msgs := e.screenMessages("", at.screen, params)
		if len(msgs) == 0 || msgs[0]["createSurface"] != nil {
			t.Fatalf("%s redraws only its blocks of %s: %d messages", at.topic, at.screen, len(msgs))
		}
	}
}

// TestTheSpendOfAPurgedProcessStays: a process with nothing to do goes after a day, and what its model cost
// stays in the spend of its project and command.
func TestTheSpendOfAPurgedProcessStays(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		cost := 0.5
		c.Req.OnUsage(provider.Usage{RequestModel: "big", Model: "big", Input: 1200, Output: 300, CostUSD: &cost})
		return provider.Result{Structured: json.RawMessage(`null`)}
	})
	p := e.createProject("quiet", nullable(testManifest([]string{agentCommand("check", "")}, nil)))
	id := e.run(p, "check")
	e.waitState(id, "done")
	e.settle()
	e.Clock.Add(25 * time.Hour)
	e.sweep()
	e.refused("deck.process_get", map[string]any{"process": id}, 404, "no process")
	var spend []struct {
		Project, Command string
		Calls            int64
		CostUSD          float64 `json:"cost_usd"`
	}
	e.must("deck.spend_list", nil, &spend)
	if len(spend) != 1 || spend[0].Project != "quiet" || spend[0].Command != "check" || spend[0].Calls != 1 || spend[0].CostUSD != 0.5 {
		t.Fatalf("the spend of a purged process stays with its project and command: %+v", spend)
	}
	var calls []map[string]any
	e.modelCalls("quiet", 0, &calls)
	if len(calls) != 1 {
		t.Fatalf("its model call stays in the journal: %+v", calls)
	}
	e.refused("deck.llm_calls_list", nil, 404, "unknown command")
}

// TestModelCallsOfABaseFromBeforeKeepTheirProject: the model calls of an older base keep their project and
// command when it learns to keep them without the process.
func TestModelCallsOfABaseFromBeforeKeepTheirProject(t *testing.T) {
	t.Parallel()
	home := t.TempDir()
	e := newEnvWith(t, home, &Fake{script: func(ctx context.Context, c *Call) provider.Result {
		c.Req.OnUsage(provider.Usage{Model: "big", Input: 10, Output: 5})
		return provider.Result{Structured: json.RawMessage(`{}`)}
	}})
	p := e.createProject("loud", testManifest([]string{agentCommand("check", "")}, nil))
	e.waitState(e.run(p, "check"), "done")
	e.olderBase(2)
	_ = e.App.Close()
	e = newEnvWith(t, home, &Fake{})
	var spend []struct{ Project, Command string }
	e.must("deck.spend_list", nil, &spend)
	if len(spend) != 1 || spend[0].Project != "loud" || spend[0].Command != "check" {
		t.Fatalf("the model calls of an older base keep their project and command: %+v", spend)
	}
}

// TestTheJournalCountsTheDaysOfTheOwner: the days of Spend and Runs are those of the owner's clock, not of UTC, half
// an hour off included: a call and a run ten minutes past the owner's midnight count on that day.
func TestTheJournalCountsTheDaysOfTheOwner(t *testing.T) {
	local := time.Local
	time.Local = time.FixedZone("owner", 5*3600+30*60)
	t.Cleanup(func() { time.Local = local })
	clock := &Clock{now: time.Date(2026, 10, 6, 22, 30, 0, 0, time.UTC)}
	e := newEnvWith(t, t.TempDir(), &Fake{}, withClock(clock))
	p := e.createProject("work", plainManifest)
	for _, q := range []string{
		`INSERT INTO llm_call (workspace_id, gen_ai_provider_name, gen_ai_usage_input_tokens, created_at) VALUES
		   (1, 'm', 10, '2026-10-05T18:20:00Z'), (1, 'm', 20, '2026-10-05T18:40:00Z')`,
		fmt.Sprintf(`INSERT INTO process (project_id, command, kind, state, started_at, finished_at) VALUES
		   (%d, 'work', 'agent', 'done', '2026-10-05T18:20:00.000Z', '2026-10-05T18:21:00.000Z'),
		   (%d, 'work', 'agent', 'done', '2026-10-05T18:40:00.000Z', '2026-10-05T18:41:00.000Z')`, p, p),
	} {
		if _, err := e.App.Store.DB.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	var spend []struct {
		Day    string
		Tokens int64
	}
	e.must("deck.spend_daily", map[string]any{"days": 3}, &spend)
	var runs []struct {
		Day  string
		Runs int64
	}
	e.must("deck.runs_daily", map[string]any{"days": 3}, &runs)
	if len(spend) != 3 || spend[0].Day != "2026-10-05" || spend[0].Tokens != 10 || spend[1].Day != "2026-10-06" || spend[2].Day != "2026-10-07" || spend[1].Tokens != 20 {
		t.Fatalf("a call just past the owner's midnight counts on that day: %+v", spend)
	}
	if len(runs) != 3 || runs[0].Runs != 1 || runs[1].Runs != 1 {
		t.Fatalf("a run just past the owner's midnight counts on that day: %+v", runs)
	}
}

// TestTheJournalKeepsTimeByTheClockOfDeck: a note and a call are written at the time of deck's clock, the one their
// cleanup compares with, so a clock of a test or a sleep of the machine ages them alike.
func TestTheJournalKeepsTimeByTheClockOfDeck(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, nil)
	e.createProject("notes", plainManifest)
	e.must("deck.notify", map[string]any{"project": "notes", "text": "done"}, nil)
	var at string
	_ = e.App.Store.DB.QueryRow(`SELECT created_at FROM feed WHERE kind = 'note' ORDER BY id DESC LIMIT 1`).Scan(&at)
	if !strings.HasPrefix(at, t0.UTC().Format("2006-01-02")) {
		t.Fatalf("a note is written at the time of deck's clock: %s", at)
	}
	_ = e.App.Store.DB.QueryRow(`SELECT created_at FROM tool_call ORDER BY id DESC LIMIT 1`).Scan(&at)
	if !strings.HasPrefix(at, t0.UTC().Format("2006-01-02")) {
		t.Fatalf("a call is journaled at the time of deck's clock: %s", at)
	}
}

// TestAnEmptyProcessThatWroteRecordsStays: a process with nothing to show that wrote records stays past the day of
// empty ones, so the master still knows which records its own processes wrote.
func TestAnEmptyProcessThatWroteRecordsStays(t *testing.T) {
	t.Parallel()
	e := newClockEnv(t, func(ctx context.Context, c *Call) provider.Result {
		if c.command() == "write" {
			c.Tool(ctx, "records_upsert", map[string]any{"collection": "ideas", "records": `{"title": "kept", "score": 1}`})
		}
		return provider.Result{Structured: json.RawMessage(`null`)}
	})
	m := strings.Replace(ideasManifest, `"commands": [{`, `"commands": [`+agentCommand("write", "")+`, `+agentCommand("idle", "")+`, {`, 1)
	p := e.createProject("notes", nullable(m))
	wrote, idle := e.run(p, "write"), e.run(p, "idle")
	e.waitState(wrote, "done")
	e.waitState(idle, "done")
	e.settle()
	e.Clock.Add(25 * time.Hour)
	e.sweep()
	e.refused("deck.process_get", map[string]any{"process": idle}, 404, "no process")
	e.must("deck.process_get", map[string]any{"process": wrote}, nil)
}
