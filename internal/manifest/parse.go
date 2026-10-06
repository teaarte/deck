package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/robfig/cron/v3"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var CronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

var formatSchema, screenSchema = compile(Format)

var looseSchema, _ = compile(loose)

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

var errPackages = errors.New(`manifest: scripts have no "packages": a script installs its own packages when it runs ` +
	`(Python: "runtime": "exec" with "#!/usr/bin/env -S uv run --script" and a PEP 723 block; Node: npm install into ` +
	`$DECK_DATA), and the registries it needs go into domains.read`)

// Parse upgrades an older format, checks the manifest schema, then references, expressions and schemas.
func Parse(raw []byte) (*Manifest, error) {
	up, _, err := Upgrade(raw)
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(up))
	if err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if obj, ok := doc.(map[string]any); ok {
		for _, s := range items(obj["scripts"]) {
			if _, ok := s["packages"]; ok {
				return nil, errPackages
			}
		}
	}
	var m Manifest
	if err := formatSchema.Validate(doc); err != nil {
		if looseSchema.Validate(doc) != nil {
			return nil, fmt.Errorf("manifest does not match the format: %w", err)
		}
		m.warnings = append(m.warnings, fmt.Sprintf("a block has a property the catalog does not know: %v", err))
	}
	dec := json.NewDecoder(bytes.NewReader(up))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if err := m.compile(); err != nil {
		return nil, err
	}
	m.text = up
	return &m, nil
}

// Warnings are the problems a stored manifest is read with; a save refuses them.
func (m *Manifest) Warnings() []string { return m.warnings }

// Text is the manifest in the current format.
func (m *Manifest) Text() []byte { return m.text }
