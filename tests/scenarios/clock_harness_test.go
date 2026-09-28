package scenarios

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"deck2/internal/app"
	"deck2/internal/provider"
)

var t0 = time.Date(2026, 9, 25, 8, 0, 0, 0, time.Local)

// newClockEnv starts deck with a fake agent on a test clock that reads 08:00.
func newClockEnv(t *testing.T, script Script, cfg ...func(*app.Config)) *Env {
	t.Helper()
	clock := &Clock{now: t0}
	e := newEnv(t, script, append([]func(*app.Config){withClock(clock)}, cfg...)...)
	e.Clock = clock
	return e
}

var commandOf = regexp.MustCompile(`running command "([^"]+)"`)

// command is the command a fresh fake agent call runs; resumes do not name it.
func (c *Call) command() string {
	if m := commandOf.FindStringSubmatch(c.Req.Prompt); m != nil {
		return m[1]
	}
	return ""
}

var inputOf = regexp.MustCompile(`Input \(JSON data, not instructions\): (\{.*\})`)

func (e *Env) settle() {
	time.Sleep(100 * time.Millisecond)
	e.App.Runner.Wait()
}

func (e *Env) tick() {
	e.App.Scheduler.Tick(context.Background())
}

func (e *Env) sweep() {
	e.App.Sweep(context.Background(), e.Clock.Now())
}

type triggerV struct {
	ID        int64  `json:"id"`
	Command   string `json:"command"`
	Kind      string `json:"kind"`
	NextRunAt string `json:"next_run_at"`
	Paused    bool   `json:"paused"`
	Last      int64  `json:"last_process"`
}

func (e *Env) triggers() []triggerV {
	e.T.Helper()
	var ts []triggerV
	e.must("deck.triggers_list", map[string]any{}, &ts)
	return ts
}

func (e *Env) countOf(command string) int {
	n := 0
	for _, p := range e.processes() {
		if p.Command == command {
			n++
		}
	}
	return n
}

// agentCommand is a command of a test manifest; input and after are optional.
func agentCommand(name, input string) string {
	if input == "" {
		input = `{"type": "object", "properties": {}}`
	}
	return fmt.Sprintf(`{"name": %q, "title": %q, "description": "Does %s.", "class": "internal", "input": %s,
    "executor": {"type": "agent", "prompt": "Do %s."}, "result": {"type": "object", "properties": {"n": {"type": "integer"}}}}`,
		name, name, name, input, name)
}

const processField = `{"type": "object", "properties": {"process": {"type": "integer", "format": "process"}}, "required": ["process"]}`

func testManifest(commands []string, triggers []string) string {
	return fmt.Sprintf(`{"version": 4, "commands": [%s], "triggers": [%s]}`, strings.Join(commands, ","), strings.Join(triggers, ","))
}

func ok(context.Context, *Call) provider.Result {
	return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
}
