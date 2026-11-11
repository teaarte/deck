package manifest

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

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
func (c *Command) InputSchema() json.RawMessage {
	if len(c.Input) == 0 {
		return registry.EmptyInput
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

// writing are deck's commands an agent changes records, documents or files with.
var writing = []string{"record_upsert", "record_update", "record_delete", "document_save", "document_delete",
	"file_upload", "file_delete"}

// Unreported are the warnings of the agent commands that may change records, documents or files — they work in a
// folder or may call a command of writing — without `changes` in their result schema.
func (m *Manifest) Unreported() []string {
	var out []string
	for _, c := range m.Commands {
		if _, ok := ObjectOf(c.Result).Properties["changes"]; ok || c.Executor.Type != "agent" {
			continue
		}
		writes := !c.Executor.Folder.IsZero() || c.Executor.Calls == nil
		for _, n := range writing {
			writes = writes || registry.Permits(c.Executor.Calls, registry.Deck(n))
		}
		if writes {
			out = append(out, fmt.Sprintf("command %s: its agent may change records, documents or files, and its result schema has no changes: add changes, an array of strings, a line for each change", c.Name))
		}
	}
	return out
}

// Unshaped are the warnings of the agent commands whose calls name a command of the module an agent may call without a
// result schema, as <project>.<name>; any project counts when project is empty.
func (m *Manifest) Unshaped(project string) []string {
	var out []string
	for _, c := range m.Commands {
		if c.Executor.Type != "agent" {
			continue
		}
		seen := map[string]bool{}
		for _, call := range c.Executor.Calls {
			p, name, ok := strings.Cut(call, ".")
			o := m.Command(name)
			if !ok || p == registry.Builtin || project != "" && p != project || o == nil || len(o.Result) > 0 ||
				seen[name] || !slices.Contains(o.Callable(), registry.Agent) {
				continue
			}
			seen[name] = true
			out = append(out, fmt.Sprintf("command %s: its agent calls %s, which has no result schema: give %s a result schema, so its tool tells the agent the shape of its answer", c.Name, name, name))
		}
	}
	return out
}
