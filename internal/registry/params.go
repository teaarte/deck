package registry

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strings"
)

// Refs are the entities a parameter may point to; they map to App Intents entities.
var Refs = []string{"record", "card", "project", "document", "process", "ask"}

// Param describes one input parameter. Type is string, text, integer, number, boolean, date, datetime,
// file, a ref from Refs, "enum" with Enum set, or any of these with a "[]" suffix for a list.
type Param struct {
	Name     string
	Type     string
	Desc     string
	Optional bool
	Enum     []string
}

func P(name, typ, desc string) Param { return Param{Name: name, Type: typ, Desc: desc} }
func Opt(name, typ, desc string) Param {
	return Param{Name: name, Type: typ, Desc: desc, Optional: true}
}

func OneOf(name, desc string, values ...string) Param {
	return Param{Name: name, Type: "enum", Desc: desc, Enum: values}
}

// Params builds a closed object schema from parameters.
func Params(ps ...Param) json.RawMessage {
	props := map[string]any{}
	required := []string{}
	for _, p := range ps {
		props[p.Name] = paramSchema(p)
		if !p.Optional {
			required = append(required, p.Name)
		}
	}
	b, _ := json.Marshal(map[string]any{"type": "object", "properties": props, "required": required, "additionalProperties": false})
	return b
}

func paramSchema(p Param) map[string]any {
	if base, ok := strings.CutSuffix(p.Type, "[]"); ok {
		item := paramSchema(Param{Type: base, Enum: p.Enum})
		return map[string]any{"type": "array", "items": item, "description": p.Desc}
	}
	s := map[string]any{}
	switch p.Type {
	case "string", "integer", "number", "boolean":
		s["type"] = p.Type
	case "text":
		s["type"], s["format"] = "string", "text"
	case "date":
		s["type"], s["format"] = "string", "date"
	case "datetime":
		s["type"], s["format"] = "string", "date-time"
	case "file":
		s["type"], s["format"] = "string", "file"
	case "project":
		s["type"], s["format"] = "string", "project"
	case "enum":
		s["type"], s["enum"] = "string", p.Enum
	default:
		if !slices.Contains(Refs, p.Type) {
			panic("registry: unknown parameter type " + p.Type)
		}
		s["type"], s["format"] = "integer", p.Type
	}
	if p.Desc != "" {
		s["description"] = p.Desc
	}
	return s
}

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
