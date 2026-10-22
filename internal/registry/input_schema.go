package registry

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"deck2/schemas"
)

// Param describes one input parameter. Type is string, text, json, secret, integer, number, boolean, date,
// datetime, file, a ref from Refs, "enum" with Enum set, or any of these with a "[]" suffix for a list.
type Param struct {
	Name     string
	Type     string
	Desc     string
	Optional bool
	Enum     []string
}

// Params builds a closed object schema from parameters; its properties keep their order, so a form asks in it.
func Params(ps ...Param) json.RawMessage {
	props := []byte{'{'}
	required := []string{}
	for i, p := range ps {
		if i > 0 {
			props = append(props, ',')
		}
		k, _ := json.Marshal(p.Name)
		v, _ := json.Marshal(paramSchema(p))
		props = append(append(append(props, k...), ':'), v...)
		if !p.Optional {
			required = append(required, p.Name)
		}
	}
	b, _ := json.Marshal(map[string]any{"type": "object", "properties": json.RawMessage(append(props, '}')), "required": required, "additionalProperties": false})
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
	case "text", "json":
		s["type"], s["format"] = "string", "text"
	case "secret":
		s["type"], s["writeOnly"] = "string", true
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

func fields(t reflect.Type) []Param {
	var out []Param
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			out = append(out, fields(f.Type)...)
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !f.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		_, opt := f.Tag.Lookup("opt")
		p := Param{Name: name, Type: f.Tag.Get("type"), Desc: f.Tag.Get("desc"), Optional: opt}
		if e := f.Tag.Get("enum"); e != "" {
			p.Type, p.Enum = "enum", strings.Split(e, "|")
		}
		if p.Type == "" {
			p.Type = goType(f.Type)
		}
		out = append(out, p)
	}
	return out
}

func goType(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "boolean"
	case reflect.Int, reflect.Int64:
		return "integer"
	case reflect.Float64:
		return "number"
	case reflect.Slice:
		return goType(t.Elem()) + "[]"
	case reflect.Pointer:
		return goType(t.Elem())
	}
	panic("registry: no parameter type for " + t.String())
}
