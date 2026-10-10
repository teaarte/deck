package scenarios

import (
	"context"
	"deck2/internal/app"
	"deck2/internal/provider"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
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

func (e *Env) tick() {
	e.App.Scheduler.Tick(context.Background())
}

func (e *Env) sweep() {
	e.App.Sweep(context.Background(), e.Clock.Now())
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

// nullable lets the results of the agent commands of a manifest be null, nothing to do.
func nullable(m string) string {
	return strings.ReplaceAll(m, `"result": {"type": "object"`, `"result": {"type": ["object", "null"]`)
}

func testManifest(commands []string, triggers []string) string {
	return fmt.Sprintf(`{"version": 11, "commands": [%s], "triggers": [%s]}`, strings.Join(commands, ","), strings.Join(triggers, ","))
}

func ok(context.Context, *Call) provider.Result {
	return provider.Result{Structured: json.RawMessage(`{"n": 1}`)}
}

// Clock is the deck clock of a test: it stands still until the test moves it.
type Clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *Clock) Add(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	return c.now
}

// withClock runs deck on a test clock that starts at start.
func withClock(clock *Clock) func(*app.Config) {
	return func(c *app.Config) { c.Now = clock.Now }
}
