package manifest

import (
	"bytes"
	"cmp"
	"deck2/internal/registry"
	"deck2/schemas"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"golang.org/x/text/language"
	"golang.org/x/text/message"

	"github.com/robfig/cron/v3"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

var CronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

var formatSchema, screenSchema = compile(full)

// moduleSchema is full without what only deck's own screens use.
var moduleSchema, _ = compile(Taught)

func compile(format []byte) (*jsonschema.Schema, *jsonschema.Schema) {
	const url = "mem://deck-manifest.json"
	whole, err := schemas.Compile(format, url)
	if err != nil {
		panic(err)
	}
	screen, err := schemas.Compile(format, url+"#/$defs/screen")
	if err != nil {
		panic(err)
	}
	return whole, screen
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
	m.defaults()
	if err := m.compile(); err != nil {
		return nil, err
	}
	if err := moduleSchema.Validate(doc); err != nil {
		for _, at := range leaves(err) {
			m.warnings = append(m.warnings, fmt.Sprintf("manifest %s: only on deck's own screens; take it out", at))
		}
	}
	m.text = raw
	return &m, nil
}

// defaults fills what a manifest leaves out and writes its domains one way, before any check reads it: an HTTP tool
// is external, a domain lower case with "*.x" as x, which already takes the subdomains of x.
func (m *Manifest) defaults() {
	for i := range m.Tools.HTTP {
		m.Tools.HTTP[i].Class = cmp.Or(m.Tools.HTTP[i].Class, registry.External)
	}
	for _, ds := range []*[]string{&m.Domains.Read, &m.Domains.Act} {
		out := make([]string, 0, len(*ds))
		for _, d := range *ds {
			out = append(out, strings.TrimPrefix(strings.ToLower(strings.TrimSpace(d)), "*."))
		}
		*ds = out
	}
}

// Warnings are the problems a stored manifest is read with; a save refuses them.
func (m *Manifest) Warnings() []string { return m.warnings }

// Text is the manifest as it was parsed.
func (m *Manifest) Text() []byte { return m.text }

// leaves are the places in a document where a validation failed, without the places that hold them.
func leaves(err error) []string {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return []string{err.Error()}
	}
	var out []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			if at := "/" + strings.Join(e.InstanceLocation, "/") + " (" + e.ErrorKind.LocalizedString(message.NewPrinter(language.English)) + ")"; !slices.Contains(out, at) {
				out = append(out, at)
			}
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	return out
}
