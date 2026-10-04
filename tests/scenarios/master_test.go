package scenarios

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"deck2/internal/provider"
)

// masterScript plays the master: the review suggests a card, on_error says what it got; failing makes the
// work command of the other projects fail.
func masterScript(ctx context.Context, c *Call) provider.Result {
	switch c.command() {
	case "review":
		if out, bad := c.Tool(ctx, "suggest", map[string]any{"project": "work", "title": "Unstick the queue", "reason": "the queue has not moved for three days"}); bad {
			return provider.Result{Text: "refused: " + out}
		}
		return provider.Result{Text: "one suggestion"}
	case "on_error":
		return provider.Result{Text: "looked"}
	case "work":
		return provider.Result{IsError: true, Text: "the site is down"}
	}
	return provider.Result{Text: "ok"}
}

func (e *Env) runsOf(project, command string) []procFull {
	e.T.Helper()
	var out []procFull
	for _, p := range e.processes() {
		if p.Project == project && p.Command == command {
			out = append(out, p)
		}
	}
	return out
}

func (e *Env) masterAwake() {
	e.T.Helper()
	e.must("deck.project_pause", map[string]any{"project": "master", "paused": false}, nil)
	e.must("deck.provider_set", map[string]any{"command": "master.on_error", "provider": ""}, nil)
}

func TestReviewRunsOnItsSchedule(t *testing.T) {
	e := newClockEnv(t, masterScript)
	e.masterAwake()
	e.createProject("work", boardWork)
	e.Clock.Add(2*time.Hour + 29*time.Minute)
	e.tick()
	if n := len(e.runsOf("master", "review")); n != 0 {
		t.Fatalf("not before 10:30: %d", n)
	}
	e.Clock.Add(2 * time.Minute)
	e.tick()
	e.waitFor("the review", func() bool { rs := e.runsOf("master", "review"); return len(rs) == 1 && rs[0].State == "done" })
	ss := e.suggestions()
	if len(ss) != 1 || !strings.Contains(ss[0].Text, "the queue has not moved") {
		t.Fatalf("the review makes suggestions: %+v", ss)
	}
	var ts []struct{ Command, Spec string }
	e.must("deck.triggers_list", map[string]any{"project": "master"}, &ts)
	found := false
	for _, tr := range ts {
		found = found || tr.Command == "review" && tr.Spec == "30 10 * * *"
	}
	if !found {
		t.Fatalf("the review is a trigger of the schedule screen: %+v", ts)
	}
}

func TestAnErrorReachesTheMasterOncePerDay(t *testing.T) {
	e := newClockEnv(t, masterScript)
	e.masterAwake()
	p := e.createProject("work", boardWork)
	failed := e.waitState(e.run(p, "work"), "error")
	e.waitFor("on_error", func() bool { return len(e.runsOf("master", "on_error")) == 1 })
	in := e.App.Store.Q
	got, _ := in.GetProcess(context.Background(), e.runsOf("master", "on_error")[0].ID)
	if !strings.Contains(got.Input, `"process":`) || !strings.Contains(got.Input, strings.TrimSpace(itoa(failed.ID))) {
		t.Fatalf("the master gets the failed process: %s", got.Input)
	}
	e.waitState(e.run(p, "work"), "error")
	time.Sleep(100 * time.Millisecond)
	if n := len(e.runsOf("master", "on_error")); n != 1 {
		t.Fatalf("once a day per command: %d", n)
	}
	e.Clock.Add(25 * time.Hour)
	e.waitState(e.run(p, "work"), "error")
	e.waitFor("the next day", func() bool { return len(e.runsOf("master", "on_error")) == 2 })
}

func TestErrorsStayWithAProjectThatTurnedThemOff(t *testing.T) {
	e := newEnv(t, masterScript)
	e.masterAwake()
	p := e.createProject("work", strings.Replace(boardWork, `"version": 6,`, `"version": 6, "master_errors": false,`, 1))
	e.waitState(e.run(p, "work"), "error")
	time.Sleep(200 * time.Millisecond)
	if n := len(e.runsOf("master", "on_error")); n != 0 {
		t.Fatalf("a project that turned it off keeps its errors: %d", n)
	}
}

func TestMasterOwnErrorsDoNotReachItself(t *testing.T) {
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		return provider.Result{IsError: true, Text: "the model is overloaded"}
	})
	e.masterAwake()
	var r struct{ Process int64 }
	e.must("master.review", map[string]any{}, &r)
	e.waitState(r.Process, "error")
	time.Sleep(200 * time.Millisecond)
	if n := len(e.runsOf("master", "on_error")); n != 0 {
		t.Fatalf("the master's own failure does not go to itself: %d", n)
	}
	var as []askView
	e.must("deck.asks_list", nil, &as)
	if len(as) != 1 || as[0].Kind != "warning" || !strings.Contains(as[0].Text, "review failed:") || !strings.Contains(as[0].Text, "the model is overloaded") {
		t.Fatalf("it waits for the owner instead: %+v", as)
	}
}

// TestReviewCallsOnlyWhatItsCommandLists: the daily review reads and suggests; changing a project is not among
// its tools, and a call by name is refused.
func TestReviewCallsOnlyWhatItsCommandLists(t *testing.T) {
	var tools []string
	e := newEnv(t, func(ctx context.Context, c *Call) provider.Result {
		tools = c.Req.AllowedTools
		out, bad := c.Tool(ctx, "manifest_save", map[string]any{"project": "work", "manifest": boardWork})
		return provider.Result{Text: fmt.Sprintf("%v %s", bad, out)}
	})
	e.masterAwake()
	e.createProject("work", boardWork)
	var r struct{ Process int64 }
	e.must("master.review", map[string]any{}, &r)
	got := e.waitState(r.Process, "done", "error")
	if !strings.HasPrefix(string(got.Result), `"true`) || !slices.Contains(tools, "mcp__deck__suggest") ||
		slices.Contains(tools, "mcp__deck__manifest_save") || slices.Contains(tools, "mcp__deck__card_move") {
		t.Fatalf("the review gets the tools it lists and no other: %s %v", got.Result, tools)
	}
}

// TestADeletedProjectTakesItsErrorDayWithIt: a new project under the name of a deleted one does not inherit its
// day of silence towards the master.
func TestADeletedProjectTakesItsErrorDayWithIt(t *testing.T) {
	e := newClockEnv(t, masterScript)
	e.masterAwake()
	e.waitState(e.run(e.createProject("work", boardWork), "work"), "error")
	e.waitFor("on_error", func() bool { rs := e.runsOf("master", "on_error"); return len(rs) == 1 && rs[0].State == "done" })
	e.must("deck.project_delete", map[string]any{"project": "work"}, nil)
	e.waitState(e.run(e.createProject("work", boardWork), "work"), "error")
	e.waitFor("on_error of the new project", func() bool { return len(e.runsOf("master", "on_error")) == 2 })
}

// TestABlockThatFailsOnAScreenReachesTheMaster: a block of a module screen that fails on the records there are
// is in the health of the module and goes to the master once per block and revision of the manifest.
func TestABlockThatFailsOnAScreenReachesTheMaster(t *testing.T) {
	e := newEnv(t, masterScript)
	e.masterAwake()
	broken := strings.Replace(boardWebManifest, `"columns": [{"label": "Title", "bind": "title"}`, `"columns": [{"label": "Title", "bind": "title"}, {"label": "Score", "bind": "score > 0 ? score : nil"}`, 1)
	if broken == boardWebManifest {
		t.Fatal("fixture replacement did not apply")
	}
	e.createProject("web", broken)
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>x</p>"}, nil)
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": jsonl(map[string]any{"title": "b"})}, nil)
	e.screenMessages("web", "main", nil)
	e.waitFor("on_error", func() bool { return len(e.runsOf("master", "on_error")) == 1 })
	got, _ := e.App.Store.Q.GetProcess(context.Background(), e.runsOf("master", "on_error")[0].ID)
	if !strings.Contains(got.Input, `"screen":"main"`) || !strings.Contains(got.Input, `"project":"web"`) || !strings.Contains(got.Input, "Score") {
		t.Fatalf("the master gets the screen, the block and the error: %s", got.Input)
	}
	var hs []struct{ Subject, Status string }
	e.must("deck.health_list", map[string]any{"project": "web"}, &hs)
	found := false
	for _, h := range hs {
		found = found || h.Subject == "screen:main" && h.Status == "error"
	}
	if !found {
		t.Fatalf("the health of the module names the screen: %+v", hs)
	}
	e.screenMessages("web", "main", nil)
	time.Sleep(100 * time.Millisecond)
	if n := len(e.runsOf("master", "on_error")); n != 1 {
		t.Fatalf("once per block and revision: %d", n)
	}
	e.must("deck.manifest_save", map[string]any{"project": "web", "manifest": boardWebManifest}, nil)
	e.must("deck.health_list", map[string]any{"project": "web"}, &hs)
	for _, h := range hs {
		if h.Subject == "screen:main" {
			t.Fatalf("a new revision starts the screen over: %+v", hs)
		}
	}
}

// TestABlockThatFailsIsInTheHealthOfItsModuleWithoutTheMaster: a module that keeps its errors from the master still
// shows a failing block in its health.
func TestABlockThatFailsIsInTheHealthOfItsModuleWithoutTheMaster(t *testing.T) {
	e := newEnv(t, masterScript)
	e.masterAwake()
	broken := strings.Replace(boardWebManifest, `"columns": [{"label": "Title", "bind": "title"}`, `"columns": [{"label": "Title", "bind": "title"}, {"label": "Score", "bind": "score > 0 ? score : nil"}`, 1)
	e.createProject("web", strings.Replace(broken, `"version": 6,`, `"version": 6, "master_errors": false,`, 1))
	e.must("deck.document_save", map[string]any{"project": "web", "name": "custom/chart.html", "kind": "custom", "text": "<p>x</p>"}, nil)
	e.must("deck.records_upsert", map[string]any{"project": "web", "collection": "notes", "records": jsonl(map[string]any{"title": "b"})}, nil)
	e.screenMessages("web", "main", nil)
	var hs []struct{ Subject, Status string }
	e.must("deck.health_list", map[string]any{"project": "web"}, &hs)
	if !slices.ContainsFunc(hs, func(h struct{ Subject, Status string }) bool {
		return h.Subject == "screen:main" && h.Status == "error"
	}) {
		t.Fatalf("the health of the module names the screen: %+v", hs)
	}
	if n := len(e.runsOf("master", "on_error")); n != 0 {
		t.Fatalf("the master does not get it: %d", n)
	}
}
