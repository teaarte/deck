package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/robfig/cron/v3"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var CronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

var formatSchema, screenSchema = compile(Format)

func compile(format []byte) (*jsonschema.Schema, *jsonschema.Schema) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(format))
	if err != nil {
		panic(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("mem://deck-manifest.json", doc); err != nil {
		panic(err)
	}
	return c.MustCompile("mem://deck-manifest.json"), c.MustCompile("mem://deck-manifest.json#/$defs/screen")
}

// Parse checks the format, the manifest schema, then references, expressions and schemas.
func Parse(raw []byte) (*Manifest, error) {
	if err := checkVersion(raw); err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	var m Manifest
	if err := formatSchema.Validate(doc); err != nil {
		return nil, fmt.Errorf("manifest does not match the format: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if err := m.compile(); err != nil {
		return nil, err
	}
	m.text = raw
	return &m, nil
}

// Warnings are the problems a stored manifest is read with; a save refuses them.
func (m *Manifest) Warnings() []string { return m.warnings }

// Text is the manifest as it was parsed.
func (m *Manifest) Text() []byte { return m.text }
