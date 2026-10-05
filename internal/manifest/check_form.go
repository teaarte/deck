package manifest

import (
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"
)

// form checks a form: one command the owner may call, or a choice of them from the commands source.
func (c *screenCheck) form(where string, b Block) {
	switch {
	case b.Source != nil && b.Source.From != CommandsSource:
		c.fail("%s: a form picks its command from %s", where, CommandsSource)
	case b.Source != nil:
		c.source(where, b.Type, *b.Source)
	case b.Command == RecordUpdate:
		if c.screen.Record == "" {
			c.fail("%s: %s edits the record of its page; the screen needs record", where, RecordUpdate)
		}
	case c.deck:
		if !strings.HasPrefix(b.Command, "deck.") {
			c.fail("%s: a form of a deck screen calls a deck command", where)
		}
	default:
		cmd := c.m.commands[b.Command]
		switch {
		case cmd == nil:
			c.fail("%s: unknown command %q", where, b.Command)
		case !slices.Contains(cmd.Callable(), "owner"):
			c.fail("%s: command %q cannot be called by the owner", where, b.Command)
		}
	}
	c.choices(where, b)
	c.values(where, b)
	if b.Save != "" && (c.deck || c.m.collections[b.Save] == nil) {
		c.fail("%s save: unknown collection %q", where, b.Save)
	}
	if cmd := c.m.commands[b.Command]; b.Save != "" && cmd != nil && !recordShaped(cmd.Result) {
		c.fail("%s save: the result of %q is not an object or a list of objects, so it cannot be kept as records", where, b.Command)
	}
}

// values checks the values of a form: fields of its input, filled from fields of the record of the page.
func (c *screenCheck) values(where string, b Block) {
	var input json.RawMessage
	switch cmd := c.m.commands[b.Command]; {
	case b.Command == RecordUpdate:
		if col := c.m.collections[c.screen.Record]; col != nil {
			input = col.Schema
		}
	case cmd != nil:
		input = cmd.InputSchema()
	}
	props := PropertyOrder(input)
	record := c.recordFields()
	for _, field := range slices.Sorted(maps.Keys(b.Values)) {
		at := fmt.Sprintf("%s values %q", where, field)
		if input != nil && !slices.Contains(props, field) {
			c.fail("%s: the input has no field %q", at, field)
		}
		if e, ok := strings.CutPrefix(b.Values[field], "="); ok {
			env := record
			if env == nil && c.screen.Record == "" {
				env = c.withParam([]string{})
			}
			if _, err := compileField(e, env); err != nil {
				c.fail("%s: %v", at, err)
			}
			continue
		}
		if f, ok := strings.CutPrefix(b.Values[field], "$record."); ok {
			c.fail("%s: %v", at, gone(f))
		}
	}
}

// choices checks the choices of a form: fields of its input, read from a source, with expressions over its items.
func (c *screenCheck) choices(where string, b Block) {
	var input json.RawMessage
	switch cmd := c.m.commands[b.Command]; {
	case len(b.Choices) == 0:
		return
	case b.Source != nil:
		c.fail("%s choices: a form that picks its command has no choices", where)
		return
	case b.Command == RecordUpdate:
		if col := c.m.collections[c.screen.Record]; col != nil {
			input = col.Schema
		}
	case cmd != nil:
		input = cmd.InputSchema()
	}
	props := PropertyOrder(input)
	for _, field := range slices.Sorted(maps.Keys(b.Choices)) {
		ch := b.Choices[field]
		at := fmt.Sprintf("%s choices %q", where, field)
		if input != nil && !slices.Contains(props, field) {
			c.fail("%s: the input has no field %q", at, field)
			continue
		}
		fields, ok := c.source(at, "choices", ch.Source)
		if !ok {
			continue
		}
		for _, e := range []string{ch.Value, ch.Label} {
			if _, err := compileField(e, fields); e != "" && err != nil {
				c.fail("%s: %v", at, err)
			}
		}
	}
}

// recordShaped says a result schema gives objects: one, or a list of them.
func recordShaped(schema json.RawMessage) bool {
	var r struct {
		Type  any `json:"type"`
		Items struct {
			Type any `json:"type"`
		} `json:"items"`
	}
	_ = json.Unmarshal(schema, &r)
	t := valueType(r.Type)
	return t == "object" || (t == "array" && valueType(r.Items.Type) == "object")
}

// valueType is the type of a schema other than null: "object" for ["object", "null"].
func valueType(t any) string {
	switch t := t.(type) {
	case string:
		return t
	case []any:
		for _, x := range t {
			if s, _ := x.(string); s != "null" {
				return s
			}
		}
	}
	return ""
}
