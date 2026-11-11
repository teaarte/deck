package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"deck2/internal/fault"
	"deck2/internal/process"
	"deck2/internal/provider"
	"deck2/tests/harness"
)

// waitOf is the wait process_get gives of a process, empty when it gives none.
func waitOf(e *harness.Env, id int64) harness.ProcWait {
	e.T.Helper()
	if w := e.Proc(id).Wait; w != nil {
		return *w
	}
	return harness.ProcWait{}
}

func ok(context.Context, *harness.Call) provider.Result {
	return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
}

// redProvider makes the fake provider red by its adapter, as a self-check that failed leaves it.
func redProvider(e *harness.Env) {
	// The row a failed self-check of the provider writes.
	e.SQL(`DELETE FROM health WHERE subject = 'provider:fake'`)
	// Its status red by the adapter.
	e.SQL(`INSERT INTO health (workspace_id, subject, status, error, verdict) VALUES (NULL, 'provider:fake', 'error', 'down', 'adapter')`)
}

// TestAQuestionLeftPastItsTermGoesOnWithoutTheAnswerAndFreesTheFolder: a question waits a day by default; at its term
// the agent is told the owner did not answer, finishes with what it has, and the next agent gets the folder.
func TestAQuestionLeftPastItsTermGoesOnWithoutTheAnswerAndFreesTheFolder(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var resumed []string
	e := harness.New(t, harness.WithConfig(harness.WithTTL), harness.WithClock(), harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Req.Resume {
			mu.Lock()
			resumed = append(resumed, c.Req.Prompt)
			mu.Unlock()
			return ok(ctx, c)
		}
		if c.N == 1 {
			askQuestion(ctx, c, "Which branch?")
			return provider.Result{Text: "stopped"}
		}
		return ok(ctx, c)
	}))
	p := e.Project("code", harness.TestManifest([]string{harness.Many(harness.FolderCommand("run", "true"))}, nil))
	e.Must("deck.folder_add", map[string]any{"project": "code", "path": harness.RepoFolder(t)}, nil)
	first := e.Run(p, "run")
	e.PassState(first, "waiting:answer")
	a := e.WaitAsk()
	var expires string
	// The term of the ask, which deck shows only in its own words.
	_ = e.SQLRow(`SELECT expires_at FROM ask WHERE id = ?`, a.ID).Scan(&expires)
	if w := waitOf(e, first); w.Reason != "answer" || w.On != fmt.Sprint(a.ID) || w.Silence != "go_on" || w.Until != expires ||
		expires < e.Clock.Now().Add(23*time.Hour).UTC().Format("2006-01-02T15:04:05.000Z") {
		t.Fatalf("a question waits a day, and its silence lets the agent go on: %+v, ask expires %q", w, expires)
	}
	second := e.Run(p, "run")
	e.WaitState(second, "waiting:folder")

	e.Clock.Add(25 * time.Hour)
	e.Sweep()
	e.WaitState(first, "done")
	e.WaitState(second, "done")
	mu.Lock()
	defer mu.Unlock()
	if e.AskState(a.ID) != "expired" || len(resumed) != 1 || !strings.Contains(resumed[0], process.NoAnswer) || !strings.Contains(resumed[0], "Which branch?") {
		t.Fatalf("at its term the question closes and the agent hears the owner did not answer: ask %s, %q", e.AskState(a.ID), resumed)
	}
}

// TestAWaitThatFailsAtItsTermEndsTheProcess: a lost login waits a week and a red provider a day; then the process
// ends with the failure its silence names, rather than waiting for good.
func TestAWaitThatFailsAtItsTermEndsTheProcess(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		reason string
		script harness.Script
		before func(e *harness.Env)
		term   time.Duration
		fault  string
	}{
		{"sign_in", func(context.Context, *harness.Call) provider.Result {
			return provider.Fail(fault.ModelLogin, "Not logged in")
		},
			nil, 7 * 24 * time.Hour, fault.ModelLogin},
		{"provider", ok, redProvider, 24 * time.Hour, fault.ModelUnavailable},
	} {
		t.Run(c.reason, func(t *testing.T) {
			t.Parallel()
			e := harness.New(t, harness.WithClock(), harness.WithAgent(c.script))
			if c.before != nil {
				c.before(e)
			}
			id := e.Run(e.Project("plain", harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil)), "work")
			e.WaitState(id, "waiting:"+c.reason)
			until := e.Clock.Now().Add(c.term).UTC().Format("2006-01-02T15:04:05.000Z")
			if w := waitOf(e, id); w.Reason != c.reason || w.Silence != "fail:"+c.fault || w.Until != until || w.On == "" {
				t.Fatalf("the wait says its term and that its silence fails it: %+v, want until %s", w, until)
			}
			e.Clock.Add(c.term - time.Minute)
			e.Sweep()
			if got := e.Proc(id); got.State != "waiting:"+c.reason {
				t.Fatalf("before its term the process waits: %+v", got)
			}
			e.Clock.Add(2 * time.Minute)
			e.Sweep()
			e.WaitState(id, "error")
			if f := e.Fault(id); f.ErrorType != c.fault {
				t.Fatalf("at its term the wait ends with the failure its silence names: %+v", f)
			}
		})
	}
}

// TestEveryWaitHoldsThroughARestartAndWakesOnItsCondition: a wait is in the base, so a restarted deck keeps it as it
// was — reason, what, term and silence — and wakes the process when what it waits for comes.
func TestEveryWaitHoldsThroughARestartAndWakesOnItsCondition(t *testing.T) {
	t.Parallel()
	hang := func(ctx context.Context, c *harness.Call) provider.Result {
		<-ctx.Done()
		return provider.Result{Text: "cut"}
	}
	for _, c := range []struct {
		reason string
		before harness.Script
		// setup starts the processes and gives the one that waits.
		setup func(e *harness.Env) int64
		// wake brings what the process waits for; the agents after the restart go on as they are let.
		wake func(e *harness.Env)
	}{
		{"answer", func(ctx context.Context, c *harness.Call) provider.Result {
			askQuestion(ctx, c, "Which branch?")
			return provider.Result{Text: "stopped"}
		}, func(e *harness.Env) int64 {
			id := e.Run(e.Project("plain", harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil)), "work")
			e.PassState(id, "waiting:answer")
			return id
		}, func(e *harness.Env) { e.Answer(e.WaitAsk().ID, "main") }},
		{"sign_in", func(context.Context, *harness.Call) provider.Result {
			return provider.Fail(fault.ModelLogin, "Not logged in")
		},
			func(e *harness.Env) int64 {
				id := e.Run(e.Project("plain", harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil)), "work")
				e.WaitState(id, "waiting:sign_in")
				return id
			}, func(e *harness.Env) { e.Answer(e.WaitAsk().ID, "retry") }},
		{"limit", func(context.Context, *harness.Call) provider.Result {
			return harness.Limited(harness.T0.Add(2 * time.Hour))
		},
			func(e *harness.Env) int64 {
				id := e.Run(e.Project("plain", harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil)), "work")
				e.WaitState(id, "waiting:limit")
				return id
			}, func(e *harness.Env) {
				e.Clock.Add(2*time.Hour + time.Minute)
				e.Sweep()
			}},
		{"provider", ok, func(e *harness.Env) int64 {
			redProvider(e)
			id := e.Run(e.Project("plain", harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil)), "work")
			e.WaitState(id, "waiting:provider")
			return id
		}, func(e *harness.Env) {
			// The row a passed self-check of the provider writes.
			e.SQL(`UPDATE health SET status = 'ok', verdict = '' WHERE subject = 'provider:fake'`)
			e.Sweep()
		}},
		{"slot", hang, func(e *harness.Env) int64 {
			e.Must("deck.setting_set", map[string]any{"name": "agent_limit", "value": "1"}, nil)
			p := e.Project("jobs", harness.TestManifest([]string{harness.Many(harness.AgentCommand("job", ""))}, nil))
			e.Run(p, "job")
			e.Until("the first at its provider", func() bool { return len(e.Fake.Calls()) == 1 })
			id := e.Run(p, "job")
			e.WaitState(id, "waiting:slot")
			return id
		}, nil},
		{"folder", hang, func(e *harness.Env) int64 {
			p := e.Project("code", harness.TestManifest([]string{harness.Many(harness.FolderCommand("run", "true"))}, nil))
			e.Must("deck.folder_add", map[string]any{"project": "code", "path": harness.RepoFolder(e.T)}, nil)
			e.Run(p, "run")
			e.Until("the first at its provider", func() bool { return len(e.Fake.Calls()) == 1 })
			id := e.Run(p, "run")
			e.WaitState(id, "waiting:folder")
			return id
		}, nil},
		{"run", hang, func(e *harness.Env) int64 {
			p := e.Project("jobs", harness.TestManifest([]string{harness.AgentCommand("job", "")}, nil))
			e.Run(p, "job")
			e.Until("the first at its provider", func() bool { return len(e.Fake.Calls()) == 1 })
			id := e.Run(p, "job")
			e.WaitState(id, "waiting:run")
			return id
		}, nil},
	} {
		t.Run(c.reason, func(t *testing.T) {
			t.Parallel()
			clock := &harness.Clock{At: harness.T0}
			e := harness.New(t, harness.WithHome(t.TempDir()), harness.WithProvider(&harness.Fake{Script: c.before}), harness.WithClock(clock))
			id := c.setup(e)
			before := waitOf(e, id)
			if before.Reason != c.reason {
				t.Fatalf("the process waits for %s: %+v", c.reason, before)
			}

			release := make(chan struct{}, 4)
			e2 := e.Restart(harness.WithProvider(&harness.Fake{Script: held(release)}))
			e2.Resume()
			if got := e2.Proc(id); got.State != "waiting:"+c.reason || *got.Wait != before {
				t.Fatalf("a restart keeps the wait as it was: %+v, was %+v", got.Wait, before)
			}
			if c.wake != nil {
				c.wake(e2)
			}
			release <- struct{}{}
			release <- struct{}{}
			if got := e2.WaitState(id, "done", "error"); got.State != "done" {
				t.Fatalf("the process goes on when what it waits for comes: %+v", got)
			}
		})
	}
}

// TestAProcessWaitingForAFolderSaysWhoHoldsItAndLeadsToTheQuestion: a folder's waiter names its holder, what and until
// when that one waits, and opens its question by Answer; an agent of another module learns only that one holds it.
func TestAProcessWaitingForAFolderSaysWhoHoldsItAndLeadsToTheQuestion(t *testing.T) {
	t.Parallel()
	var waiter atomic.Int64
	peeked := make(chan string, 1)
	e := harness.New(t, harness.WithConfig(harness.WithTTL), harness.WithClock(), harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "peek" {
			out, _ := c.Tool(ctx, "process_get", map[string]any{"process": waiter.Load()})
			peeked <- out
			return ok(ctx, c)
		}
		if c.N == 1 {
			askQuestion(ctx, c, "Which branch?")
			return provider.Result{Text: "stopped"}
		}
		return ok(ctx, c)
	}))
	m := harness.TestManifest([]string{harness.Many(harness.FolderCommand("run", "true")), harness.AgentCommand("peek", "")}, nil)
	p, other := e.Project("code", m), e.Project("other", m)
	repo := harness.RepoFolder(t)
	e.Must("deck.folder_add", map[string]any{"project": "code", "path": repo}, nil)
	e.Must("deck.folder_add", map[string]any{"project": "other", "path": repo}, nil)
	first := e.Run(p, "run")
	e.PassState(first, "waiting:answer")
	a := e.WaitAsk()
	second := e.Run(p, "run")
	e.WaitState(second, "waiting:folder")

	var row harness.ListItem
	e.Until("Running now names the holder of the folder", func() bool {
		for _, it := range e.CockpitRunning() {
			if strings.Contains(it.Text, fmt.Sprintf("which process #%d holds", first)) {
				row = it
				return true
			}
		}
		return false
	})
	if !strings.Contains(row.Text, "waits for folder "+repo) || !strings.Contains(row.Text, "waits for your answer: Which branch?") {
		t.Fatalf("the waiting one says what the holder waits for: %q", row.Text)
	}
	var answer map[string]any
	for _, act := range row.Actions {
		if act["verb"] == "answer" {
			answer = act
		}
	}
	if params, _ := answer["params"].(map[string]any); answer == nil || answer["screen"] != "cockpit" || params["focus"] != fmt.Sprint(a.ID) {
		t.Fatalf("an Answer button opens the question of the holder in Today: %+v", row.Actions)
	}
	var v struct {
		HeldBy    int64  `json:"held_by"`
		HeldAsk   int64  `json:"held_ask"`
		HeldUntil string `json:"held_until"`
	}
	e.Must("deck.process_get", map[string]any{"process": second}, &v)
	var expires string
	// The term of the ask, which deck shows only in its own words.
	_ = e.SQLRow(`SELECT expires_at FROM ask WHERE id = ?`, a.ID).Scan(&expires)
	if v.HeldBy != first || v.HeldAsk != a.ID || v.HeldUntil != expires {
		t.Fatalf("process_get names the holder, its ask and the term of its wait: %+v, ask expires %q", v, expires)
	}

	theirs := e.Run(other, "run")
	e.WaitState(theirs, "waiting:folder")
	waiter.Store(theirs)
	e.Run(other, "peek")
	if got := <-peeked; strings.Contains(got, "Which branch?") || strings.Contains(got, "held_ask") || strings.Contains(got, "held_by") ||
		!strings.Contains(got, "which another module holds") {
		t.Fatalf("an agent of another module learns only that another module holds the folder: %s", got)
	}

	e.Answer(a.ID, "main")
	e.WaitState(first, "done")
	e.WaitState(second, "done")
	e.WaitState(theirs, "done")
}
