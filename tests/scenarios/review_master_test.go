package scenarios

import (
	"context"
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
		if p.Project == e.projectIDOr(project) && p.Command == command {
			out = append(out, p)
		}
	}
	return out
}

// projectIDOr is the id of a project by name, the built-in master too.
func (e *Env) projectIDOr(name string) int64 {
	var ps []struct {
		ID   int64
		Name string
	}
	e.must("deck.projects_list", nil, &ps)
	for _, p := range ps {
		if p.Name == name {
			return p.ID
		}
	}
	return 0
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
	p := e.createProject("work", strings.Replace(boardWork, `"version": 4,`, `"version": 4, "master_errors": false,`, 1))
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
