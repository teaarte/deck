package manifest

import (
	"bytes"
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func compileSchema(name string, raw json.RawMessage) (*jsonschema.Schema, error) {
	if len(raw) == 0 {
		return nil, errors.New("schema is required")
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	url := "mem://" + name + ".json"
	if err := c.AddResource(url, doc); err != nil {
		return nil, err
	}
	return c.Compile(url)
}

// Object is an object schema as deck reads it: its properties and those it requires.
type Object struct {
	Properties map[string]Prop `json:"properties"`
	Required   []string        `json:"required"`
}

// Prop is a property of an object schema; Type is a name or a list of names.
type Prop struct {
	Type   any    `json:"type"`
	Format string `json:"format"`
	Enum   []any  `json:"enum"`
	Items  *Prop  `json:"items"`
	// Properties are those of an object property.
	Properties map[string]Prop `json:"properties"`
}

// Kind is the one type a property names, empty for none or a list.
func (p Prop) Kind() string {
	s, _ := p.Type.(string)
	return s
}

// Values are the values of the enum of the property in order, none when it is no enum.
func (p Prop) Values() []string { return texts(p.Enum) }

// ObjectOf reads an object schema; a schema that does not read has no properties.
func ObjectOf(raw json.RawMessage) Object {
	var o Object
	_ = json.Unmarshal(raw, &o)
	return o
}

// Fields are the properties of the object, none when it declares none.
func (o Object) Fields() []string {
	if len(o.Properties) == 0 {
		return nil
	}
	return slices.Collect(maps.Keys(o.Properties))
}

// Types are the JSON types of the properties, "any" for one without.
func (o Object) Types() map[string]string {
	out := map[string]string{}
	for k, p := range o.Properties {
		out[k] = cmp.Or(p.Kind(), "any")
	}
	return out
}

// Enum are the values of an enum property, or of the items of a list property, in order.
func (o Object) Enum(field string) []string {
	p := o.Properties[field]
	if len(p.Enum) == 0 && p.Items != nil {
		return p.Items.Values()
	}
	return p.Values()
}

// Format are the properties of a format, such as json or file, sorted.
func (o Object) Format(format string) []string {
	var out []string
	for _, name := range slices.Sorted(maps.Keys(o.Properties)) {
		if o.Properties[name].Format == format {
			out = append(out, name)
		}
	}
	return out
}

func texts(vs []any) []string {
	var out []string
	for _, v := range vs {
		out = append(out, fmt.Sprint(v))
	}
	return out
}

// checked are the compiled schemas CheckValue has seen, by their text.
var checked sync.Map

// CheckValue validates a value against a JSON Schema, compiled once per schema.
func CheckValue(schema json.RawMessage, v any) error {
	s, ok := checked.Load(string(schema))
	if !ok {
		c, err := compileSchema("value", schema)
		if err != nil {
			return err
		}
		s, _ = checked.LoadOrStore(string(schema), c)
	}
	return s.(*jsonschema.Schema).Validate(normalize(v))
}
