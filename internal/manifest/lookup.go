package manifest

import (
	"encoding/json"

	"deck2/internal/registry"
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

func (m *Manifest) Screen(name string) *Screen {
	for i := range m.Screens {
		if m.Screens[i].Name == name {
			return &m.Screens[i]
		}
	}
	return nil
}

// TakesNull reports whether the command may finish with null, nothing to do: no result schema, or one that takes null.
func (c *Command) TakesNull() bool { return c.result == nil || c.result.Validate(nil) == nil }

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
