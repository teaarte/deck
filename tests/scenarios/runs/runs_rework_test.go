package runs

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	deckhome "deck2/internal/home"
	"deck2/internal/provider"
	"deck2/tests/harness"
)

// TestProcessPageSendsAFinishedAgentBackForReworkWithARemark: a finished agent process has a rework button that
// asks for the remark; it resumes the same session with it. Open processes and scripts have none.
func TestProcessPageSendsAFinishedAgentBackForReworkWithARemark(t *testing.T) {
	t.Parallel()
	release := make(chan struct{}, 1)
	e := harness.New(t, harness.WithAgent(func(ctx context.Context, c *harness.Call) provider.Result {
		if c.Command() == "hold" {
			select {
			case <-release:
			case <-ctx.Done():
			}
		}
		return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
	}))

	p := e.Project("jobs", harness.TestManifest([]string{harness.AgentCommand("job", ""), harness.AgentCommand("hold", "")}, nil))
	cardActions := func(id int64) []map[string]any {
		t.Helper()
		v := harness.BlockData[struct{ Actions []map[string]any }](t, e.Screen("", "process", map[string]any{"record": id})["b0"]["value"])
		return v.Actions
	}
	held := e.Run(p, "hold")
	e.Until("the held process to run", func() bool { return e.Proc(held).State == "running" })
	if harness.HasVerb(cardActions(held), "rework") || !harness.HasVerb(cardActions(held), "stop") {
		t.Fatalf("an open process is stopped, not sent back: %+v", cardActions(held))
	}
	release <- struct{}{}
	e.WaitState(held, "done")

	job := e.Run(p, "job")
	e.WaitState(job, "done")
	rework := harness.Verb(t, cardActions(job), "rework")
	if rework["input"] != "remark" || rework["command"] != "deck.process_rework" || rework["primary"] != true {
		t.Fatalf("rework asks for the remark and sends it back: %+v", rework)
	}
	params := rework["params"].(map[string]any)
	params["remark"] = "shorter, please"
	e.Must(rework["command"].(string), params, nil)
	e.Until("the second turn", func() bool { return len(e.Fake.Calls()) == 3 })
	e.WaitState(job, "done")
	last := e.Fake.Calls()[2]
	if !last.Resume || !strings.Contains(last.Prompt, "shorter, please") {
		t.Fatalf("rework resumes the session with the remark: %+v", last)
	}

	e.Project("tools", harness.ScriptManifest("take", "", ""))
	e.SaveScript("tools", "scripts/main.py", "import json\nprint(json.dumps({}))\n")
	s := e.Start("tools", "take", nil)
	e.WaitState(s, "done")
	if harness.HasVerb(cardActions(s), "rework") {
		t.Fatalf("a script has no session to send back: %+v", cardActions(s))
	}
}

func TestWorkFolderOfAFinishedAgentGoesAfterAWeek(t *testing.T) {
	t.Parallel()
	e := harness.New(t, harness.WithAgent(harness.Ok), harness.WithClock())
	p := e.Project("plain", harness.TestManifest([]string{harness.AgentCommand("work", "")}, nil))
	id := e.Run(p, "work")
	e.WaitState(id, "done")
	work := filepath.Join(deckhome.Layout(e.App.Cfg.Home).Work(), "runs", fmt.Sprint(id))
	e.Clock.Add(6 * 24 * time.Hour)
	e.Sweep()
	if _, err := os.Stat(work); err != nil {
		t.Fatalf("the work folder stays for rework during the week: %v", err)
	}
	e.Clock.Add(2 * 24 * time.Hour)
	e.Sweep()
	if _, err := os.Stat(work); !os.IsNotExist(err) {
		t.Fatalf("after a week the work folder goes: %v", err)
	}
}
