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

// TestProcessPageSendsAFinishedAgentBackForReworkWithARemark: under an agent's log the owner writes a message with files
// while it works, a rework with a remark and files once it ended, which resumes the session; a script has no box.
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
	type box struct {
		Say    map[string]any
		Attach string
	}
	say := func(id int64) box {
		t.Helper()
		d := e.Screen("", "process", map[string]any{"record": id})["b2"]
		var b box
		_ = json.Unmarshal(d["say"], &b.Say)
		_ = json.Unmarshal(d["attach"], &b.Attach)
		return b
	}
	held := e.Run(p, "hold")
	e.Until("the held process to run", func() bool { return e.Proc(held).State == "running" })
	if b := say(held); b.Say["command"] != "deck.process_tell" || b.Attach != "jobs" {
		t.Fatalf("an agent at work takes a message with files of its project: %+v", b)
	}
	note := e.Upload("jobs", "note.txt", "text/plain", "keep it short")
	e.Must("deck.process_tell", map[string]any{"process": held, "text": "one more thing", "files": []string{fmt.Sprint(note)}}, nil)
	e.Must("deck.process_tell", map[string]any{"process": held, "text": "", "files": []string{fmt.Sprint(note)}}, nil)
	release <- struct{}{}
	e.WaitState(held, "done")

	job := e.Run(p, "job")
	e.WaitState(job, "done")
	b := say(job)
	if b.Say["input"] != "remark" || b.Say["command"] != "deck.process_rework" {
		t.Fatalf("a finished agent takes a rework with the remark: %+v", b)
	}
	params := b.Say["params"].(map[string]any)
	params["remark"], params["files"] = "shorter, please", []string{fmt.Sprint(note)}
	e.Must(b.Say["command"].(string), params, nil)
	e.Until("the turn of the rework", func() bool { return e.Proc(job).State == "done" && len(e.Fake.Calls()) >= 3 })
	e.WaitState(job, "done")
	calls := e.Fake.Calls()
	last := calls[len(calls)-1]
	if !last.Resume || !strings.Contains(last.Prompt, "shorter, please") || !strings.Contains(last.Prompt, "note.txt") {
		t.Fatalf("rework resumes the session with the remark and the file given: %+v", last)
	}

	e.Project("tools", harness.ScriptManifest("take", "", ""))
	e.SaveScript("tools", "scripts/main.py", "import json\nprint(json.dumps({}))\n")
	s := e.Start("tools", "take", nil)
	e.WaitState(s, "done")
	if b := say(s); b.Say != nil {
		t.Fatalf("a script has no session to write to: %+v", b)
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
