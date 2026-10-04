package manifest

import (
	"deck2/internal/registry"
	"encoding/json"
)

// EventCommand is the command a trigger of the manifest runs on an event, or empty.
func (m *Manifest) EventCommand(on string) string {
	for _, t := range m.Triggers {
		if t.On == on {
			return t.Command
		}
	}
	return ""
}

func (m *Manifest) Collection(name string) *Collection { return m.collections[name] }

func (m *Manifest) Command(name string) *Command { return m.commands[name] }

func (m *Manifest) Script(name string) *Script { return m.scripts[name] }

// Lane returns a board lane by name; nil without a board or such lane.
func (m *Manifest) Lane(name string) *Lane {
	if m.Board == nil {
		return nil
	}
	for i := range m.Board.Lanes {
		if m.Board.Lanes[i].Name == name {
			return &m.Board.Lanes[i]
		}
	}
	return nil
}

// RoleLane returns the lane with a role; nil when no lane has it.
func (m *Manifest) RoleLane(role string) *Lane {
	if m.Board == nil {
		return nil
	}
	for i := range m.Board.Lanes {
		if m.Board.Lanes[i].Role == role {
			return &m.Board.Lanes[i]
		}
	}
	return nil
}

// RunRoles are the lane roles code runs need together.
var RunRoles = []string{"ready", "running", "review"}

// Runs reports whether the board has every lane role code runs need.
func (m *Manifest) Runs() bool {
	for _, r := range RunRoles {
		if m.RoleLane(r) == nil {
			return false
		}
	}
	return true
}

func (m *Manifest) Screen(name string) *Screen {
	for i := range m.Screens {
		if m.Screens[i].Name == name {
			return &m.Screens[i]
		}
	}
	return nil
}

func (c *Command) ValidateResult(v any) error {
	if c.result == nil {
		return nil
	}
	return c.result.Validate(normalize(v))
}

// InputSchema is the input schema of the command; an empty object when it declares none.
// RetryKey is the optional input field of every command: a call with the key of a running process returns it.
const RetryKey = "retry_key"

func (c *Command) InputSchema() json.RawMessage {
	if len(c.Input) == 0 {
		return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	}
	return c.Input
}

// Callable reports who may call the command; everyone when it declares nothing.
func (c *Command) Callable() []string {
	if len(c.Callers) == 0 {
		return registry.AnyCaller
	}
	return c.Callers
}
