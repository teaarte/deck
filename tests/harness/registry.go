package harness

import (
	"encoding/json"
)

type CommandView struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Input       json.RawMessage `json:"input"`
	Class       string          `json:"class"`
}

// Commands are what the owner may call, each as command_describe tells it.
func (e *Env) Commands() []CommandView {
	e.T.Helper()
	var rows []struct{ Name string }
	e.Must("deck.commands_list", nil, &rows)
	out := make([]CommandView, 0, len(rows))
	for _, r := range rows {
		var c CommandView
		e.Must("deck.command_describe", map[string]any{"command": r.Name}, &c)
		out = append(out, c)
	}
	return out
}
