package registry

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
)

// Def declares a builtin command of class internal; its input schema is the fields of In in order: json name,
// `desc`, `type` past the Go type, `enum` joined by "|", `opt` when optional; embedded structs add theirs.
func Def[In any](name, title, desc string, callers []string, h func(ctx context.Context, a Actor, in In) (any, error)) *Command {
	ps := fields(reflect.TypeFor[In]())
	return &Command{Name: Deck(name), Title: title, Description: desc, Class: Internal, Callers: callers,
		Input: Params(ps...), Handler: Typed(h), params: ps}
}

// Static is the topic of a read whose result no event changes.
const Static = "static"

// On names the event topics after which a read gives another result, so a screen reads it again.
func (c *Command) On(topics ...string) *Command {
	c.Topics = topics
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
	c.Readable = true
	if slices.ContainsFunc(c.params, func(p Param) bool { return p.Name == "fields" }) {
		return c
	}
	c.params = append(c.params, Param{Name: "fields", Type: "string[]", Optional: true,
		Desc: "Only these fields of the result, of each of its rows; a.b is the field b of a, also inside a JSON text a holds, " +
			"where a.<name> of a list is its item of that name"})
	c.Input = Params(c.params...)
	whole := c.Handler
	sh := shapeOf(reflect.TypeFor[Out]())
	c.Handler = func(ctx context.Context, a Actor, in json.RawMessage) (any, error) {
		out, err := whole(ctx, a, in)
		var f struct{ Fields []string }
		if err != nil || json.Unmarshal(in, &f) != nil || len(f.Fields) == 0 {
			return out, err
		}
		return cut(out, sh.fields(f.Fields))
	}
	return c
}
