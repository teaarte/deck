package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

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

// schemaFields lists top-level properties of an object schema; nil when it declares none.
func schemaFields(raw json.RawMessage) []string {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	if json.Unmarshal(raw, &s) != nil || len(s.Properties) == 0 {
		return nil
	}
	out := make([]string, 0, len(s.Properties))
	for k := range s.Properties {
		out = append(out, k)
	}
	return out
}

// Lanes are the values of an enum field of a schema in order, none when the field is not an enum.
func Lanes(raw json.RawMessage, field string) []string {
	var s struct {
		Properties map[string]struct {
			Enum []any `json:"enum"`
		} `json:"properties"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	var out []string
	for _, v := range s.Properties[field].Enum {
		out = append(out, fmt.Sprint(v))
	}
	return out
}

// Enum are the values of an enum field of a schema, or of the items of a list field, in order.
func Enum(raw json.RawMessage, field string) []string {
	if out := Lanes(raw, field); len(out) > 0 {
		return out
	}
	var s struct {
		Properties map[string]struct {
			Items struct {
				Enum []any `json:"enum"`
			} `json:"items"`
		} `json:"properties"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return nil
	}
	var out []string
	for _, v := range s.Properties[field].Items.Enum {
		out = append(out, fmt.Sprint(v))
	}
	return out
}

// CheckValue validates a value against a JSON Schema.
func CheckValue(schema json.RawMessage, v any) error {
	s, err := compileSchema("value", schema)
	if err != nil {
		return err
	}
	return s.Validate(normalize(v))
}
