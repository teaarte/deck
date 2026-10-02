package manifest

import (
	"bytes"
	"deck2/internal/registry"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"

	"github.com/expr-lang/expr"
	"github.com/expr-lang/expr/vm"
	"github.com/robfig/cron/v3"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const FormatVersion = 5

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
	Reads           []Read       `json:"reads,omitempty"`
	NoNetwork       bool         `json:"no_network,omitempty"`
	// MasterErrors off keeps the failed processes of the project from the master; absent is on.
	MasterErrors *bool `json:"master_errors,omitempty"`

	// Revision is the stored revision the manifest was read from; 0 when it was not read from the base.
	Revision int64 `json:"-"`

	collections map[string]*Collection
	commands    map[string]*Command
	scripts     map[string]*Script
	// text is the manifest as Parse read it, upgraded to the current format.
	text []byte
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
	// Calls, when set, are the registry commands an agent may call, as names or prefixes such as deck.*.
	Calls  []string `json:"calls,omitempty"`
	Script string   `json:"script,omitempty"`
	Tool   string   `json:"tool,omitempty"`
	Folder Folder   `json:"folder,omitzero"`
}

// Folder names the project folder an agent works in; Only is the one folder of a project with a single folder.
type Folder struct {
	Only bool
	Name string
}

func (f Folder) IsZero() bool { return !f.Only && f.Name == "" }

func (f Folder) MarshalJSON() ([]byte, error) {
	if f.Name != "" {
		return json.Marshal(f.Name)
	}
	return json.Marshal(f.Only)
}

func (f *Folder) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch v := v.(type) {
	case bool:
		*f = Folder{Only: v}
	case string:
		*f = Folder{Name: v}
	default:
		return fmt.Errorf("folder is true or the name of a project folder")
	}
	return nil
}

type Script struct {
	Name           string   `json:"name"`
	File           string   `json:"file"`
	Runtime        string   `json:"runtime"`
	Programs       []string `json:"programs,omitempty"`
	TimeoutMinutes int      `json:"timeout_minutes,omitempty"`
}

type Trigger struct {
	Command string `json:"command"`
	Cron    string `json:"cron,omitempty"`
	Lane    string `json:"lane,omitempty"`
	After   string `json:"after,omitempty"`
	// On is an event of deck only the built-in project answers: OnMessage or OnFailure.
	On         string         `json:"on,omitempty"`
	Params     map[string]any `json:"params,omitempty"`
	SkipMissed bool           `json:"skip_missed,omitempty"`
}

// Events of deck a trigger of the built-in project answers: the owner wrote in a chat topic, a process of another
// project failed.
const (
	OnMessage = "message"
	OnFailure = "failure"
)

// EventCommand is the command a trigger of the manifest runs on an event, or empty.
func (m *Manifest) EventCommand(on string) string {
	for _, t := range m.Triggers {
		if t.On == on {
			return t.Command
		}
	}
	return ""
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
	Name  string `json:"name"`
	Title string `json:"title"`
	// Record is the collection whose record the screen shows; the client opens it with the record id.
	Record string  `json:"record,omitempty"`
	Blocks []Block `json:"blocks"`
}

type Block struct {
	Type    string   `json:"type"`
	Title   string   `json:"title,omitempty"`
	Source  *Source  `json:"source,omitempty"`
	Columns []Column `json:"columns,omitempty"`
	Fields  []Column `json:"fields,omitempty"`
	Item    *Item    `json:"item,omitempty"`
	// Value is the expression of a stat block over rows, the items of its source.
	Value   string   `json:"value,omitempty"`
	Actions []Action `json:"actions,omitempty"`
	// ActionsFrom is a field of an item with buttons of its own: label, command with its project, params.
	ActionsFrom string  `json:"actions_from,omitempty"`
	Blocks      []Block `json:"blocks,omitempty"`
	Tabs        []Tab   `json:"tabs,omitempty"`
	// Filter names the fields of a table or list the owner narrows it by.
	Filter []string `json:"filter,omitempty"`
	// Command is the command of a form; Save a collection its result can be kept in.
	Command string `json:"command,omitempty"`
	Save    string `json:"save,omitempty"`
	// Choices offer the values of fields of a form from a source.
	Choices map[string]Choice `json:"choices,omitempty"`
	// Document is the custom/ document with the HTML of a custom block, or the document a document block shows.
	Document string `json:"document,omitempty"`
}

// Choice is where a field of a form takes its values: Value and Label are expressions over each item.
type Choice struct {
	Source Source `json:"source"`
	Value  string `json:"value"`
	Label  string `json:"label,omitempty"`
}

// Item binds the lines of a list block.
type Item struct {
	Title string `json:"title"`
	Text  string `json:"text,omitempty"`
	Meta  string `json:"meta,omitempty"`
	// Details is text shown in full on demand; Questions are questions the owner answers in a picker.
	Details   string `json:"details,omitempty"`
	Questions string `json:"questions,omitempty"`
	// Markdown says the text of each line is markdown.
	Markdown bool `json:"markdown,omitempty"`
}

type Tab struct {
	Title  string  `json:"title"`
	Blocks []Block `json:"blocks"`
}

type Source struct {
	// From is what the block reads: a collection (the default) or a command that only reads.
	From string `json:"from,omitempty"`
	// Input is the input of the command; "$record.<field>" is a field of the record of the page.
	Input map[string]any `json:"input,omitempty"`
	// Rows names the list in the result of a command that gives an object.
	Rows string `json:"rows,omitempty"`
	// Project is another project the manifest reads; empty is the project itself.
	Project    string `json:"project,omitempty"`
	Collection string `json:"collection,omitempty"`
	Where      string `json:"where,omitempty"`
	Sort       string `json:"sort,omitempty"`
	Limit      int    `json:"limit,omitempty"`
}

type Column struct {
	Label string `json:"label"`
	Bind  string `json:"bind"`
	// Markdown says the value of a card field is markdown.
	Markdown bool `json:"markdown,omitempty"`
}

// Action is a button. Params, Screen, Record, Project, File and Until take a fixed value, "$record.<field>"
// or "=<expression>" over the item the button is on.
type Action struct {
	// Label is the text of the button; Verb names a built-in label the client translates.
	Label   string            `json:"label,omitempty"`
	Verb    string            `json:"verb,omitempty"`
	Command string            `json:"command,omitempty"`
	Screen  string            `json:"screen,omitempty"`
	Params  map[string]string `json:"params,omitempty"`
	// Record is the record a move to a screen opens, the item by default; Project the project of that screen.
	Record  string `json:"record,omitempty"`
	Project string `json:"project,omitempty"`
	// File is a stored file the button downloads instead.
	File string `json:"file,omitempty"`
	// When shows the button only on the items it holds for; Until is the time after which it is gone.
	When  string `json:"when,omitempty"`
	Until string `json:"until,omitempty"`
	// Input is the param the owner types before the call, or picks from Options; Confirm asks the owner first.
	Input   string `json:"input,omitempty"`
	Options string `json:"options,omitempty"`
	// Chat opens the chat topic the result of the call names.
	Chat    bool `json:"chat,omitempty"`
	Confirm bool `json:"confirm,omitempty"`
	Primary bool `json:"primary,omitempty"`
	// Undo is the call that takes the button's call back, offered for a moment after it went through.
	Undo *Action `json:"undo,omitempty"`
}

var CronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)

var formatSchema, screenSchema = func() (*jsonschema.Schema, *jsonschema.Schema) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(Format))
	if err != nil {
		panic(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("mem://deck-manifest.json", doc); err != nil {
		panic(err)
	}
	return c.MustCompile("mem://deck-manifest.json"), c.MustCompile("mem://deck-manifest.json#/$defs/screen")
}()

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
	m.text = up
	return &m, nil
}

// Text is the manifest in the current format.
func (m *Manifest) Text() []byte { return m.text }

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
		return registry.AnyCaller
	}
	return c.Callers
}
