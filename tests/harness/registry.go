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
