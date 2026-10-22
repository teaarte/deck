package manifest

import (
	"bytes"
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
}

// Kind is the one type a property names, empty for none or a list.
func (p Prop) Kind() string {
	s, _ := p.Type.(string)
	return s
}

// Props reads an object schema; a schema that does not read has no properties.
func Props(raw json.RawMessage) Object {
	var o Object
	_ = json.Unmarshal(raw, &o)
	return o
}

// schemaFields lists top-level properties of an object schema; nil when it declares none.
func schemaFields(raw json.RawMessage) []string {
	props := Props(raw).Properties
	if len(props) == 0 {
		return nil
	}
	return slices.Collect(maps.Keys(props))
}

// Lanes are the values of an enum field of a schema in order, none when the field is not an enum.
func Lanes(raw json.RawMessage, field string) []string {
	return texts(Props(raw).Properties[field].Enum)
}

// Enum are the values of an enum field of a schema, or of the items of a list field, in order.
func Enum(raw json.RawMessage, field string) []string {
	p := Props(raw).Properties[field]
	if len(p.Enum) == 0 && p.Items != nil {
		return texts(p.Items.Enum)
	}
	return texts(p.Enum)
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
