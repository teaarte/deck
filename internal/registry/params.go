package registry

import (
	"encoding/json"
	"slices"
	"strings"

	"deck2/schemas"
)

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
		if !slices.Contains(schemas.Refs, p.Type) {
			panic("registry: unknown parameter type " + p.Type)
		}
		s["type"], s["format"] = "integer", p.Type
	}
	if p.Desc != "" {
		s["description"] = p.Desc
	}
	return s
}
