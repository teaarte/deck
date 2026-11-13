package registry

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
)

// Def declares a builtin command, which then says the rights its calls need or that it stays in deck; its input schema is the fields of In in order: json name,
// `desc`, `type` past the Go type, `enum` joined by "|", `opt` when optional; embedded structs add theirs.
func Def[In any](name, title, desc string, callers []string, h func(ctx context.Context, a Actor, in In) (any, error)) *Command {
	ps := fields(reflect.TypeFor[In]())
	return &Command{Name: Deck(name), Title: title, Description: desc, Callers: callers,
		Input: Params(ps...), Handler: Typed(h), params: ps}
}

// Static is the topic of a read whose result no event changes.
const Static = "static"

// On names the event topics after which a read gives another result, so a screen reads it again.
func (c *Command) On(topics ...string) *Command {
	c.Topics = topics
	return c
}

// Follows makes a read follow the topics of other reads of deck too, such as those of what it counts.
func (c *Command) Follows(reads ...string) *Command {
	c.follows = reads
	return c
}

// Brief makes a read give only these fields of each row unless the call asks for fields, the rest of them too.
func (c *Command) Brief(fields ...string) *Command {
	c.brief = fields
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
	c.Readable, c.stays = true, true
	// A read gives the data of deck it reads, unless it declares it gives only deck's words.
	c.Gate.Brings = FromData
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
		if err != nil || json.Unmarshal(in, &f) != nil {
			return out, err
		}
		if len(f.Fields) == 0 {
			if len(c.brief) == 0 {
				return out, nil
			}
			f.Fields = c.brief
		}
		v, err := plain(out)
		if err != nil {
			return nil, err
		}
		if bad, names := sh.unknown(v, f.Fields); len(bad) > 0 {
			return nil, Invalid("%s has no field %s; its fields are %s", c.Name, strings.Join(bad, ", "), strings.Join(names, ", "))
		}
		if p, ok := out.(Pictured); ok && len(p.Pictures()) > 0 {
			return pictured{cut(v, sh.fields(f.Fields)), p.Pictures()}, nil
		}
		return cut(v, sh.fields(f.Fields)), nil
	}
	return c
}
