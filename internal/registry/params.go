package registry

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"

	"deck2/schemas"
)

// Param describes one input parameter. Type is string, text, secret, integer, number, boolean, date, datetime,
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
	case "text":
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

// Def declares a builtin command of class internal; its input schema is the fields of In in order: json name,
// `desc`, `type` past the Go type, `enum` joined by "|", `opt` when optional; embedded structs add theirs.
func Def[In any](name, title, desc string, callers []string, h func(ctx context.Context, a Actor, in In) (any, error)) *Command {
	ps := fields(reflect.TypeFor[In]())
	return &Command{Name: Builtin + "." + name, Title: title, Description: desc, Class: Internal, Callers: callers,
		Input: Params(ps...), Handler: Typed(h), params: ps}
}

// On names the event topic after which a read gives another result, so a screen reads it again.
func (c *Command) On(topic string) *Command {
	c.Topic = topic
	return c
}

// Changes names the event topics a call of the command changes, published once it went through.
func (c *Command) Changes(topics ...string) *Command {
	c.changes = append(c.changes, topics...)
	return c
}

// As gives a declared command another class.
func (c *Command) As(class string) *Command {
	c.Class = class
	return c
}

// Enum sets the values of a parameter that a tag cannot name, such as a list kept in Go.
func (c *Command) Enum(param string, values ...string) *Command {
	for i := range c.params {
		if c.params[i].Name == param {
			c.params[i].Type, c.params[i].Enum = "enum", values
		}
	}
	c.Input = Params(c.params...)
	return c
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
	}
	panic("registry: no parameter type for " + t.String())
}

// Limit is n within 1..most, or def when it is not given or out of range.
func Limit(n, def, most int64) int64 {
	if n <= 0 || n > most {
		return def
	}
	return n
}

// Read declares a builtin command like Def that only reads, so a screen may show its result; the result schema
// is the json fields of Out, and a nil list comes back empty.
func Read[In, Out any](name, title, desc string, callers []string, h func(ctx context.Context, a Actor, in In) (Out, error)) *Command {
	c := Def(name, title, desc, callers, func(ctx context.Context, a Actor, in In) (any, error) {
		out, err := h(ctx, a, in)
		if err != nil {
			return nil, err
		}
		if v := reflect.ValueOf(out); v.Kind() == reflect.Slice && v.IsNil() {
			return reflect.MakeSlice(v.Type(), 0, 0).Interface(), nil
		}
		return out, nil
	})
	c.Result, _ = json.Marshal(resultSchema(reflect.TypeFor[Out]()))
	c.Reads = true
	return c
}

var rawType = reflect.TypeFor[json.RawMessage]()

func resultSchema(t reflect.Type) map[string]any {
	if t == rawType {
		return map[string]any{}
	}
	switch t.Kind() {
	case reflect.Pointer:
		return resultSchema(t.Elem())
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int64, reflect.Int32:
		return map[string]any{"type": "integer"}
	case reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice:
		return map[string]any{"type": "array", "items": resultSchema(t.Elem())}
	case reflect.Map:
		return map[string]any{"type": "object"}
	case reflect.Struct:
		props := map[string]any{}
		resultFields(t, props)
		return map[string]any{"type": "object", "properties": props}
	}
	return map[string]any{}
}

func resultFields(t reflect.Type, props map[string]any) {
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Anonymous && f.Type.Kind() == reflect.Struct {
			resultFields(f.Type, props)
			continue
		}
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !f.IsExported() || name == "-" {
			continue
		}
		if name == "" {
			name = f.Name
		}
		props[name] = resultSchema(f.Type)
	}
}
