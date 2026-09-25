package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/robfig/cron/v3"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const FormatVersion = 1

type Manifest struct {
	Version     int          `json:"version"`
	Description string       `json:"description"`
	Collections []Collection `json:"collections"`
	Commands    []Command    `json:"commands"`
	Triggers    []Trigger    `json:"triggers"`
	Screens     []Screen     `json:"screens"`

	collections map[string]*Collection
	commands    map[string]*Command
}

type Collection struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Key         string          `json:"key"`
	Title       string          `json:"title"`
	Schema      json.RawMessage `json:"schema"`

	keyProg   *vm.Program
	titleProg *vm.Program
	schema    *jsonschema.Schema
}

type Command struct {
	Name         string          `json:"name"`
	Title        string          `json:"title"`
	Description  string          `json:"description"`
	Class        string          `json:"class"`
	Executor     Executor        `json:"executor"`
	ResultSchema json.RawMessage `json:"result_schema"`

	resultSchema *jsonschema.Schema
}

type Executor struct {
	Type   string `json:"type"`
	Prompt string `json:"prompt"`
}

type Trigger struct {
	Command string `json:"command"`
	Cron    string `json:"cron"`
}

type Screen struct {
	Name   string  `json:"name"`
	Title  string  `json:"title"`
	Blocks []Block `json:"blocks"`
}

type Block struct {
	Type    string   `json:"type"`
	Title   string   `json:"title"`
	Source  *Source  `json:"source,omitempty"`
	Columns []Column `json:"columns,omitempty"`
	Actions []Action `json:"actions,omitempty"`
}

type Source struct {
	Collection string `json:"collection"`
	Where      string `json:"where,omitempty"`
	Sort       string `json:"sort,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type Column struct {
	Label string `json:"label"`
	Bind  string `json:"bind"`
}

type Action struct {
	Label   string            `json:"label"`
	Command string            `json:"command"`
	Params  map[string]string `json:"params,omitempty"`
}

var CronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

var classes = map[string]bool{"internal": true, "reversible": true, "external": true}

// Parse decodes and validates a manifest: schema shape, references, expressions, JSON schemas, cron.
func Parse(raw []byte) (*Manifest, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if err := m.compile(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) compile() error {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	if m.Version != FormatVersion {
		fail("version: want %d, got %d", FormatVersion, m.Version)
	}
	m.collections = map[string]*Collection{}
	for i := range m.Collections {
		c := &m.Collections[i]
		if c.Name == "" {
			fail("collections[%d]: name is required", i)
			continue
		}
		if m.collections[c.Name] != nil {
			fail("collection %q: duplicate", c.Name)
		}
		m.collections[c.Name] = c
		var err error
		if c.keyProg, err = compileExpr(c.Key); err != nil {
			fail("collection %q key: %v", c.Name, err)
		}
		if c.Title != "" {
			if c.titleProg, err = compileExpr(c.Title); err != nil {
				fail("collection %q title: %v", c.Name, err)
			}
		}
		if c.schema, err = compileSchema("collection-"+c.Name, c.Schema); err != nil {
			fail("collection %q schema: %v", c.Name, err)
		}
	}
	m.commands = map[string]*Command{}
	for i := range m.Commands {
		c := &m.Commands[i]
		if c.Name == "" || strings.Contains(c.Name, ".") {
			fail("commands[%d]: name is required and must not contain '.'", i)
			continue
		}
		if m.commands[c.Name] != nil {
			fail("command %q: duplicate", c.Name)
		}
		m.commands[c.Name] = c
		if strings.TrimSpace(c.Title) == "" || strings.TrimSpace(c.Description) == "" {
			fail("command %q: title and description are required", c.Name)
		}
		if !classes[c.Class] {
			fail("command %q: class must be internal, reversible or external", c.Name)
		}
		if c.Executor.Type != "agent" {
			fail("command %q: executor type %q is not supported", c.Name, c.Executor.Type)
		}
		if strings.TrimSpace(c.Executor.Prompt) == "" {
			fail("command %q: executor prompt is required", c.Name)
		}
		if len(c.ResultSchema) > 0 {
			var err error
			if c.resultSchema, err = compileSchema("result-"+c.Name, c.ResultSchema); err != nil {
				fail("command %q result_schema: %v", c.Name, err)
			}
		}
	}
	seenTriggers := map[Trigger]bool{}
	for i, t := range m.Triggers {
		if seenTriggers[t] {
			fail("triggers[%d]: duplicate trigger for %q", i, t.Command)
		}
		seenTriggers[t] = true
		if m.commands[t.Command] == nil {
			fail("triggers[%d]: unknown command %q", i, t.Command)
		}
		if _, err := CronParser.Parse(t.Cron); err != nil {
			fail("triggers[%d]: cron %q: %v", i, t.Cron, err)
		}
	}
	for _, s := range m.Screens {
		for j, b := range s.Blocks {
			where := fmt.Sprintf("screen %q block %d", s.Name, j)
			switch b.Type {
			case "table":
				if b.Source == nil || m.collections[b.Source.Collection] == nil {
					fail("%s: unknown collection", where)
				} else if b.Source.Where != "" {
					if _, err := compileExpr(b.Source.Where); err != nil {
						fail("%s where: %v", where, err)
					}
				}
				for _, c := range b.Columns {
					if _, err := compileExpr(c.Bind); err != nil {
						fail("%s column %q: %v", where, c.Label, err)
					}
				}
			case "actions":
			default:
				fail("%s: unknown block type %q", where, b.Type)
			}
			for _, a := range b.Actions {
				if m.commands[a.Command] == nil {
					fail("%s action %q: unknown command %q", where, a.Label, a.Command)
				}
			}
		}
	}
	return errors.Join(errs...)
}

func (m *Manifest) Collection(name string) *Collection { return m.collections[name] }
func (m *Manifest) Command(name string) *Command       { return m.commands[name] }

func (m *Manifest) Screen(name string) *Screen {
	for i := range m.Screens {
		if m.Screens[i].Name == name {
			return &m.Screens[i]
		}
	}
	return nil
}

// Key computes the uniqueness key of a record after validating it against the collection schema.
func (c *Collection) RecordKey(data map[string]any) (string, error) {
	if err := c.schema.Validate(normalize(data)); err != nil {
		return "", fmt.Errorf("record does not match schema of %q: %w", c.Name, err)
	}
	out, err := expr.Run(c.keyProg, data)
	if err != nil {
		return "", fmt.Errorf("key of %q: %w", c.Name, err)
	}
	var key string
	switch v := out.(type) {
	case string:
		key = v
	case int, int64, bool:
		key = fmt.Sprint(v)
	case float64:
		key = strconv.FormatFloat(v, 'f', -1, 64)
	default:
		return "", fmt.Errorf("key of %q must be a string or number, got %T", c.Name, out)
	}
	if key == "" {
		return "", fmt.Errorf("key of %q is empty", c.Name)
	}
	return key, nil
}

// RecordTitle is the display name of a record; empty when the collection declares none.
func (c *Collection) RecordTitle(data map[string]any) string {
	if c.titleProg == nil {
		return ""
	}
	out, err := expr.Run(c.titleProg, data)
	if err != nil || out == nil {
		return ""
	}
	return fmt.Sprint(out)
}

func (c *Command) ValidateResult(v any) error {
	if c.resultSchema == nil {
		return nil
	}
	return c.resultSchema.Validate(normalize(v))
}

func compileExpr(src string) (*vm.Program, error) {
	if strings.TrimSpace(src) == "" {
		return nil, errors.New("expression is empty")
	}
	return expr.Compile(src, expr.AllowUndefinedVariables())
}

func Eval(src string, env map[string]any) (any, error) {
	p, err := compileExpr(src)
	if err != nil {
		return nil, err
	}
	return expr.Run(p, env)
}

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

// DecodeObject parses one JSON object, keeping integers exact as int64.
func DecodeObject(b []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	if m == nil {
		return nil, errors.New("not a JSON object")
	}
	if dec.More() {
		return nil, errors.New("extra data after the JSON object")
	}
	return exactNumbers(m).(map[string]any), nil
}

func exactNumbers(v any) any {
	switch x := v.(type) {
	case json.Number:
		if i, err := x.Int64(); err == nil {
			return i
		}
		f, _ := x.Float64()
		return f
	case map[string]any:
		for k, e := range x {
			x[k] = exactNumbers(e)
		}
	case []any:
		for i, e := range x {
			x[i] = exactNumbers(e)
		}
	}
	return v
}

// normalize round-trips a value through JSON so numbers and maps have the types the validator expects.
func normalize(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	out, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return v
	}
	return out
}
