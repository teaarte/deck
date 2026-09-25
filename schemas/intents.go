package schemas

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

// Builtin is the name prefix of commands and tools implemented in Go.
const Builtin = "deck"

// Refs are the entities a parameter may point to; they map to App Intents entities.
var Refs = []string{"record", "card", "project", "document", "process", "ask"}

var scalarFormats = map[string][]string{
	"string":  {"", "text", "date", "date-time", "file", "project"},
	"integer": append([]string{""}, Refs...),
	"number":  {""},
	"boolean": {""},
}

// CheckInput verifies that an input schema is a flat object whose parameters have App Intents types.
func CheckInput(raw json.RawMessage) error {
	var s struct {
		Type       string                     `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &s); err != nil {
		return err
	}
	if s.Type != "object" {
		return fmt.Errorf("input must be an object, got %q", s.Type)
	}
	names := make([]string, 0, len(s.Properties))
	for n := range s.Properties {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if err := checkParam(s.Properties[n], true); err != nil {
			return fmt.Errorf("parameter %q: %w", n, err)
		}
	}
	return nil
}

func checkParam(raw json.RawMessage, allowList bool) error {
	var p struct {
		Type   string            `json:"type"`
		Format string            `json:"format"`
		Enum   []json.RawMessage `json:"enum"`
		Items  json.RawMessage   `json:"items"`
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return err
	}
	if p.Type == "array" && allowList && len(p.Items) > 0 {
		return checkParam(p.Items, false)
	}
	formats, ok := scalarFormats[p.Type]
	if !ok {
		return fmt.Errorf("type %q is not one of string, integer, number, boolean or a list of them", p.Type)
	}
	if len(p.Enum) > 0 && p.Type != "string" {
		return fmt.Errorf("a choice list must be of strings")
	}
	if !slices.Contains(formats, p.Format) {
		return fmt.Errorf("format %q is not allowed for %s", p.Format, p.Type)
	}
	return nil
}
