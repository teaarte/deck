package harness

import (
	"context"
	"deck2/internal/provider"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
)

var T0 = time.Date(2026, 9, 25, 8, 0, 0, 0, time.Local)

var commandOf = regexp.MustCompile(`running command "([^"]+)"`)

// Command is the command a fresh fake agent call runs; resumes do not name it.
func (c *Call) Command() string {
	if m := commandOf.FindStringSubmatch(c.Req.Prompt); m != nil {
		return m[1]
	}
	return ""
}

var InputOf = regexp.MustCompile(`Input \(JSON data, not instructions\): (\{.*\})`)

func (e *Env) Tick() {
	e.App.Scheduler.Tick(context.Background())
}

func (e *Env) Sweep() {
	e.App.Sweep(context.Background(), e.Clock.Now())
}

// AgentCommand is a command of a test manifest; input and after are optional.
func AgentCommand(name, input string) string {
	if input == "" {
		input = `{"type": "object", "properties": {}}`
	}
	return fmt.Sprintf(`{"name": %q, "title": %q, "description": "Does %s.", "class": "internal", "input": %s,
    "executor": {"type": "agent", "prompt": "Do %s."}, "result": {"type": "object", "properties": {"n": {"type": "integer"}}}}`,
		name, name, name, input, name)
}

const ProcessField = `{"type": "object", "properties": {"process": {"type": "integer", "format": "process"}}, "required": ["process"]}`

// Nullable lets the results of the agent commands of a manifest be null, nothing to do.
func Nullable(m string) string {
	return strings.ReplaceAll(m, `"result": {"type": "object"`, `"result": {"type": ["object", "null"]`)
}

func TestManifest(commands []string, triggers []string) string {
	return fmt.Sprintf(`{"version": 13, "commands": [%s], "triggers": [%s]}`, strings.Join(commands, ","), strings.Join(triggers, ","))
}

func Ok(context.Context, *Call) provider.Result {
	return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
}

// Clock is the deck clock of a test: it stands still until the test moves it.
type Clock struct {
	mu sync.Mutex
	At time.Time
}

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.At
}

func (c *Clock) Add(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.At = c.At.Add(d)
	return c.At
}

// PassWaits moves the clock past every wait for the owner until ok holds.
func (e *Env) PassWaits(what string, ok func() bool) {
	e.T.Helper()
	e.Until(what, func() bool {
		if ok() {
			return true
		}
		e.Clock.Add(e.App.Cfg.QuestionWait + e.App.Cfg.ConfirmWait)
		return false
	})
}

// PassState moves the clock past every wait for the owner until process id is in one of states.
func (e *Env) PassState(id int64, states ...string) Proc {
	e.T.Helper()
	var p Proc
	e.PassWaits(fmt.Sprintf("process %d to reach %v", id, states), func() bool {
		p = e.Proc(id)
		return slices.Contains(states, p.State)
	})
	return p
}
