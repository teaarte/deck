package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strconv"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/robfig/cron/v3"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"deck2/schemas"
)

const FormatVersion = 4

type Manifest struct {
	Version         int          `json:"version"`
	Collections     []Collection `json:"collections,omitempty"`
	Commands        []Command    `json:"commands,omitempty"`
	Scripts         []Script     `json:"scripts,omitempty"`
	Triggers        []Trigger    `json:"triggers,omitempty"`
	Tools           Tools        `json:"tools,omitzero"`
	AllowedExternal []string     `json:"allowed_external,omitempty"`
	Domains         Domains      `json:"domains,omitzero"`
	Board           *Board       `json:"board,omitempty"`
	Screens         []Screen     `json:"screens,omitempty"`
	Menu            []string     `json:"menu,omitempty"`
	Autonomy        string       `json:"autonomy,omitempty"`
	MasterEvents    bool         `json:"master_events,omitempty"`
	Reads           []Read       `json:"reads,omitempty"`

	// Revision is the stored revision the manifest was read from; 0 when it was not read from the base.
	Revision int64 `json:"-"`

	collections map[string]*Collection
	commands    map[string]*Command
	scripts     map[string]*Script
}

type Collection struct {
	Name              string          `json:"name"`
	Description       string          `json:"description"`
	Schema            json.RawMessage `json:"schema"`
	Key               string          `json:"key"`
	Display           string          `json:"display,omitempty"`
	Immutable         []string        `json:"immutable,omitempty"`
	RetentionDays     int             `json:"retention_days,omitempty"`
	FileRetentionDays int             `json:"file_retention_days,omitempty"`
	Migration         string          `json:"migration,omitempty"`

	fields      []string
	keyFields   []string
	keyProg     *vm.Program
	displayProg *vm.Program
	schema      *jsonschema.Schema
}

type Command struct {
	Name           string          `json:"name"`
	Title          string          `json:"title"`
	Description    string          `json:"description"`
	Class          string          `json:"class"`
	Input          json.RawMessage `json:"input,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"`
	Callers        []string        `json:"callers,omitempty"`
	Requires       []string        `json:"requires,omitempty"`
	Provider       string          `json:"provider,omitempty"`
	TimeoutMinutes int             `json:"timeout_minutes,omitempty"`
	Executor       Executor        `json:"executor"`

	input  *jsonschema.Schema
	result *jsonschema.Schema
}

type Executor struct {
	Type       string   `json:"type"`
	Prompt     string   `json:"prompt,omitempty"`
	PromptFile string   `json:"prompt_file,omitempty"`
	Skills     []string `json:"skills,omitempty"`
	Script     string   `json:"script,omitempty"`
	Tool       string   `json:"tool,omitempty"`
}

type Script struct {
	Name           string   `json:"name"`
	File           string   `json:"file"`
	Runtime        string   `json:"runtime"`
	Packages       []string `json:"packages,omitempty"`
	Programs       []string `json:"programs,omitempty"`
	TimeoutMinutes int      `json:"timeout_minutes,omitempty"`
}

type Trigger struct {
	Command    string         `json:"command"`
	Cron       string         `json:"cron,omitempty"`
	Lane       string         `json:"lane,omitempty"`
	After      string         `json:"after,omitempty"`
	Params     map[string]any `json:"params,omitempty"`
	SkipMissed bool           `json:"skip_missed,omitempty"`
}

type Tools struct {
	HTTP []HTTPTool `json:"http,omitempty"`
	MCP  struct {
		Servers map[string]MCPServer `json:"servers,omitempty"`
	} `json:"mcp,omitzero"`
}

type HTTPTool struct {
	Name              string            `json:"name"`
	Title             string            `json:"title,omitempty"`
	Description       string            `json:"description"`
	Method            string            `json:"method"`
	URL               string            `json:"url"`
	Headers           map[string]string `json:"headers,omitempty"`
	Body              json.RawMessage   `json:"body,omitempty"`
	Input             json.RawMessage   `json:"input,omitempty"`
	Result            json.RawMessage   `json:"result,omitempty"`
	Class             string            `json:"class"`
	Auth              json.RawMessage   `json:"auth,omitempty"`
	ConfirmTTLMinutes int               `json:"confirm_ttl_minutes,omitempty"`
}

type MCPServer struct {
	Command    string            `json:"command,omitempty"`
	Args       []string          `json:"args,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	URL        string            `json:"url,omitempty"`
	ToolFilter []string          `json:"toolFilter,omitempty"`
	Classes    map[string]string `json:"classes,omitempty"`
}

type Domains struct {
	Read []string `json:"read,omitempty"`
	Act  []string `json:"act,omitempty"`
}

type Board struct {
	Lanes []Lane `json:"lanes"`
}

type Lane struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Role  string `json:"role,omitempty"`
}

type Read struct {
	Project     string   `json:"project"`
	Collections []string `json:"collections,omitempty"`
	Documents   []string `json:"documents,omitempty"`
}

type Screen struct {
	Name   string  `json:"name"`
	Title  string  `json:"title"`
	Blocks []Block `json:"blocks"`
}

type Block struct {
	Type    string   `json:"type"`
	Title   string   `json:"title,omitempty"`
	Source  *Source  `json:"source,omitempty"`
	Columns []Column `json:"columns,omitempty"`
	Actions []Action `json:"actions,omitempty"`
}

type Source struct {
	// Project is another project the manifest reads; empty is the project itself.
	Project    string `json:"project,omitempty"`
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

var formatSchema = func() *jsonschema.Schema {
	s, err := compileSchema("deck-manifest", schemas.Manifest)
	if err != nil {
		panic(err)
	}
	return s
}()

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
	if err := formatSchema.Validate(doc); err != nil {
		return nil, fmt.Errorf("manifest does not match the format: %w", err)
	}
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(up))
	dec.UseNumber()
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("manifest: %w", err)
	}
	if err := m.compile(); err != nil {
		return nil, err
	}
	return &m, nil
}

func (m *Manifest) Collection(name string) *Collection { return m.collections[name] }
func (m *Manifest) Command(name string) *Command       { return m.commands[name] }
func (m *Manifest) Script(name string) *Script         { return m.scripts[name] }

// Lane returns a board lane by name; nil without a board or such lane.
func (m *Manifest) Lane(name string) *Lane {
	if m.Board == nil {
		return nil
	}
	for i := range m.Board.Lanes {
		if m.Board.Lanes[i].Name == name {
			return &m.Board.Lanes[i]
		}
	}
	return nil
}

// RoleLane returns the lane with a role; nil when no lane has it.
func (m *Manifest) RoleLane(role string) *Lane {
	if m.Board == nil {
		return nil
	}
	for i := range m.Board.Lanes {
		if m.Board.Lanes[i].Role == role {
			return &m.Board.Lanes[i]
		}
	}
	return nil
}

// RunRoles are the lane roles code runs need together.
var RunRoles = []string{"ready", "running", "review"}

// Runs reports whether the board has every lane role code runs need.
func (m *Manifest) Runs() bool {
	for _, r := range RunRoles {
		if m.RoleLane(r) == nil {
			return false
		}
	}
	return true
}

func (m *Manifest) Screen(name string) *Screen {
	for i := range m.Screens {
		if m.Screens[i].Name == name {
			return &m.Screens[i]
		}
	}
	return nil
}

// Validate checks a record against the collection schema.
func (c *Collection) Validate(data map[string]any) error {
	if err := c.schema.Validate(normalize(data)); err != nil {
		return fmt.Errorf("record does not match schema of %q: %w", c.Name, err)
	}
	return nil
}

// RecordKey computes the uniqueness key of a record after validating it against the collection schema.
func (c *Collection) RecordKey(data map[string]any) (string, error) {
	if err := c.Validate(data); err != nil {
		return "", err
	}
	return c.KeyOf(data)
}

// KeyOf computes the key without schema validation.
func (c *Collection) KeyOf(data map[string]any) (string, error) {
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
	if c.displayProg == nil {
		return ""
	}
	out, err := expr.Run(c.displayProg, data)
	if err != nil || out == nil {
		return ""
	}
	return fmt.Sprint(out)
}

// CheckImmutable refuses changes of immutable and key fields between the stored and the new record.
func (c *Collection) CheckImmutable(old, next map[string]any) error {
	for _, f := range append(slices.Clone(c.Immutable), c.keyFields...) {
		if !reflect.DeepEqual(normalize(old[f]), normalize(next[f])) {
			return fmt.Errorf("field %q of %q cannot change after the record is created", f, c.Name)
		}
	}
	return nil
}

func (c *Command) ValidateResult(v any) error {
	if c.result == nil {
		return nil
	}
	return c.result.Validate(normalize(v))
}

// InputSchema is the input schema of the command; an empty object when it declares none.
// RetryKey is the optional input field of every command: a call with the key of a running process returns it.
const RetryKey = "retry_key"

func (c *Command) InputSchema() json.RawMessage {
	if len(c.Input) == 0 {
		return json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	}
	return c.Input
}

// Callable reports who may call the command; everyone when it declares nothing.
func (c *Command) Callable() []string {
	if len(c.Callers) == 0 {
		return []string{"owner", "master", "agent", "trigger"}
	}
	return c.Callers
}
