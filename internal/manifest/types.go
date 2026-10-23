package manifest

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/expr-lang/expr/vm"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const FormatVersion = 13

type Manifest struct {
	Version         int          `json:"version"`
	Collections     []Collection `json:"collections,omitempty"`
	Commands        []Command    `json:"commands,omitempty"`
	Scripts         []Script     `json:"scripts,omitempty"`
	Triggers        []Trigger    `json:"triggers,omitempty"`
	Tools           Tools        `json:"tools,omitzero"`
	AllowedExternal []string     `json:"allowed_external,omitempty"`
	Domains         Domains      `json:"domains,omitzero"`
	Screens         []Screen     `json:"screens,omitempty"`
	Menu            []string     `json:"menu,omitempty"`
	Icon            string       `json:"icon,omitempty"`
	Title           string       `json:"title,omitempty"`
	// I18n translates the texts the owner sees, by language and then by the text as the manifest writes it.
	I18n      map[string]map[string]string `json:"i18n,omitempty"`
	Reads     []Read                       `json:"reads,omitempty"`
	NoNetwork bool                         `json:"no_network,omitempty"`
	// Models are the connections, models and @roles the project calls models through, besides its commands of
	// type model.
	Models []string `json:"models,omitempty"`
	// Writes are the documents of the project its agents and scripts write besides artifacts and memory: a name, or a
	// folder ending in "/" for all under it.
	Writes []string `json:"writes,omitempty"`

	// Revision is the stored revision the manifest was read from; 0 when it was not read from the base.
	Revision int64 `json:"-"`

	collections map[string]*Collection
	commands    map[string]*Command
	scripts     map[string]*Script
	// text is the manifest as Parse read it, upgraded to the current format.
	text []byte
	// warnings are problems a read lets pass and a save refuses.
	warnings []string
	// granted, once set by Clip, are the only model uses in force.
	granted *[]string
}

// Uses are the connections, models and @roles the project calls models through: its models and what its
// commands of type model name, the models assigned to their kind when they name none.
func (m *Manifest) Uses() []string {
	if m.granted != nil {
		return *m.granted
	}
	out := slices.Clone(m.Models)
	for _, c := range m.Commands {
		if c.Executor.Type != "model" {
			continue
		}
		use := c.Executor.Model
		if use == "" {
			use = "@" + c.Executor.Kind
		}
		if !slices.Contains(out, use) {
			out = append(out, use)
		}
	}
	return out
}

type Collection struct {
	Name          string          `json:"name"`
	Description   string          `json:"description"`
	Schema        json.RawMessage `json:"schema"`
	Key           string          `json:"key"`
	Display       string          `json:"display,omitempty"`
	Immutable     []string        `json:"immutable,omitempty"`
	RetentionDays int             `json:"retention_days,omitempty"`
	Migration     string          `json:"migration,omitempty"`
	// Folder is the folder of a record in the data folder, an expression such as 'videos/' + slug; it goes with
	// the record. Parent names the collection a record belongs to and the key of its record there.
	Folder string  `json:"folder,omitempty"`
	Parent *Parent `json:"parent,omitempty"`

	fields      []string
	keyFields   []string
	keyProg     *vm.Program
	displayProg *vm.Program
	folderProg  *vm.Program
	parentProg  *vm.Program
	schema      *jsonschema.Schema
}

// Parent is the collection the records of a collection belong to, and the expression of the key of the record
// of each there; a record goes with its parent.
type Parent struct {
	Collection string `json:"collection"`
	Key        string `json:"key"`
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
	// Kind is what a model executor makes; Model an @role or a model of the owner, the kind's assignment when empty;
	// Options are fields deck adds to its request as they are.
	Kind    string         `json:"kind,omitempty"`
	Model   string         `json:"model,omitempty"`
	Options map[string]any `json:"options,omitempty"`
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
	Name           string    `json:"name"`
	File           string    `json:"file"`
	Runtime        string    `json:"runtime"`
	Programs       []Program `json:"programs,omitempty"`
	TimeoutMinutes int       `json:"timeout_minutes,omitempty"`
}

// Program is a program a script needs on the machine; Install says how the owner installs it.
type Program struct {
	Name    string `json:"name"`
	Install string `json:"install,omitempty"`
}

func (p *Program) UnmarshalJSON(b []byte) error {
	if json.Unmarshal(b, &p.Name) == nil {
		return nil
	}
	type plain Program
	return json.Unmarshal(b, (*plain)(p))
}

type Trigger struct {
	Command string `json:"command"`
	Cron    string `json:"cron,omitempty"`
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

type Read struct {
	Project     string   `json:"project"`
	Collections []string `json:"collections,omitempty"`
	Documents   []string `json:"documents,omitempty"`
	// Models reads which models do the work of the project, or of every one under AnyProject.
	Models bool `json:"models,omitempty"`
	// Runs reads the runs of the project in brief, or of every one under AnyProject: command, state, failure, model,
	// times; never input, result or log.
	Runs bool `json:"runs,omitempty"`
}

// Data says the entry reads data of its project, not only which models do its work or its runs.
func (r Read) Data() bool { return !r.Models && !r.Runs || len(r.Collections)+len(r.Documents) > 0 }

// AnyProject in reads names every project; it reads only which models do their work and their runs.
const AnyProject = "*"

type Screen struct {
	Name  string `json:"name"`
	Title string `json:"title"`
	Icon  string `json:"icon,omitempty"`
	// Record is the collection whose record the screen shows; the client opens it with the record id.
	Record string `json:"record,omitempty"`
	// Params are the parameters of its address by name and JSON type, param.<name> in its expressions.
	Params map[string]string `json:"params,omitempty"`
	Blocks []Block           `json:"blocks"`
}

type Block struct {
	Type  string `json:"type"`
	Title string `json:"title,omitempty"`
	Icon  string `json:"icon,omitempty"`
	// Description is a line under the title of the block.
	Description string `json:"description,omitempty"`
	// When is an expression over the record of the page and rows; where it does not hold the block is left out.
	When    string   `json:"when,omitempty"`
	Source  *Source  `json:"source,omitempty"`
	Columns []Column `json:"columns,omitempty"`
	Fields  []Column `json:"fields,omitempty"`
	Item    *Item    `json:"item,omitempty"`
	// Kind, X and Series draw a chart: x and each y are expressions over an item.
	Kind   string   `json:"kind,omitempty"`
	X      string   `json:"x,omitempty"`
	Series []Series `json:"series,omitempty"`
	// Value is the expression of a stat block over rows, the items of its source.
	Value string `json:"value,omitempty"`
	// Format and Currency say how the client shows the value of a stat block.
	Format   string `json:"format,omitempty"`
	Currency string `json:"currency,omitempty"`
	// Empty is the text a block with a source shows when it has no items.
	Empty string `json:"empty,omitempty"`
	// Tone is the expression of a stat block over rows: ok, warn, error or muted.
	Tone    string   `json:"tone,omitempty"`
	Actions []Action `json:"actions,omitempty"`
	Blocks  []Block  `json:"blocks,omitempty"`
	Tabs    []Tab    `json:"tabs,omitempty"`
	// Span is how many cells of the grid of the screen the block takes: 1, 2 or "full".
	Span Span `json:"span,omitzero"`
	// Part is the part of the device an appearance block shows: its look or its pushes.
	Part string `json:"part,omitempty"`
	// Name opens a dialog from the address of its screen; Primary paints its button in the accent.
	Name    string `json:"name,omitempty"`
	Primary bool   `json:"primary,omitempty"`
	// Filter names the fields of a table or list the owner narrows it by.
	Filter []string `json:"filter,omitempty"`
	// Board is the enum field of a list whose values are the lanes its items stand in.
	Board string `json:"board,omitempty"`
	// Cards lays the items of a list side by side as cards.
	Cards bool `json:"cards,omitempty"`
	// Command is the command of a form.
	Command string `json:"command,omitempty"`
	// Choices offer the values of fields of a form from a source.
	Choices map[string]Choice `json:"choices,omitempty"`
	// Values fill fields of a form before the owner types: fixed text or =<expression> over the record of the page.
	Values map[string]string `json:"values,omitempty"`
	// Fixed are the fields of a form whose values are not shown and go with the call as they are.
	Fixed []string `json:"fixed,omitempty"`
	// KeepOpen keeps the dialog of a form open after its call, to show the result.
	KeepOpen bool `json:"keep_open,omitempty"`
	// Document is the custom/ document with the HTML of a custom block, or the document a document block shows.
	Document string `json:"document,omitempty"`
	// Params are what a custom block is given beside its items: fixed text or =<expression> over the record of the page.
	Params map[string]string `json:"params,omitempty"`
}

// Span is the cells of the grid a block takes: N of them, or the whole width.
type Span struct {
	N    int
	Full bool
}

func (s Span) IsZero() bool { return s.N == 0 && !s.Full }

func (s Span) MarshalJSON() ([]byte, error) {
	if s.Full {
		return json.Marshal("full")
	}
	return json.Marshal(s.N)
}

func (s *Span) UnmarshalJSON(b []byte) error {
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return err
	}
	switch v := v.(type) {
	case float64:
		*s = Span{N: int(v)}
	case string:
		if v != "full" {
			return fmt.Errorf("span is 1, 2 or \"full\"")
		}
		*s = Span{Full: true}
	default:
		return fmt.Errorf("span is 1, 2 or \"full\"")
	}
	return nil
}

// Series is a line of a chart: its label and the expression of its value over an item.
type Series struct {
	Label string `json:"label"`
	Y     string `json:"y"`
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
	// Since is an expression giving the time a line counts from, shown as how long ago.
	Since string `json:"since,omitempty"`
	// Icon is an expression giving the icon of a line.
	Icon string `json:"icon,omitempty"`
	// Details is text shown in full on demand; Questions are questions the owner answers in a picker.
	Details   string `json:"details,omitempty"`
	Questions string `json:"questions,omitempty"`
	// Tone is an expression over the line: ok, warn, error or muted.
	Tone string `json:"tone,omitempty"`
	// File is the file of a line of a media block: a stored file id or a path in the data folder.
	File string `json:"file,omitempty"`
}

type Tab struct {
	Title  string  `json:"title"`
	Icon   string  `json:"icon,omitempty"`
	Blocks []Block `json:"blocks"`
}

type Source struct {
	// From is what the block reads: a collection (the default) or a command that only reads.
	From string `json:"from,omitempty"`
	// Input is the input of the command; a text "=<expression>" is read over the record of the page.
	Input map[string]any `json:"input,omitempty"`
	// Rows names the list in the result of a command that gives an object.
	Rows       string `json:"rows,omitempty"`
	Collection string `json:"collection,omitempty"`
	Where      string `json:"where,omitempty"`
	Sort       string `json:"sort,omitempty"`
	Limit      int    `json:"limit,omitempty"`
	// Fields are the only fields of each item a custom block gets, with _id; where and sort read them all.
	Fields []string `json:"fields,omitempty"`
}

type Column struct {
	Label string `json:"label"`
	Bind  string `json:"bind"`
	// Markdown says the value of a card field is markdown.
	Markdown bool `json:"markdown,omitempty"`
	// Format says how the client shows the value; Currency is the ISO 4217 code of money.
	Format   string `json:"format,omitempty"`
	Currency string `json:"currency,omitempty"`
	// Tone is an expression over the item: ok, warn, error or muted.
	Tone string `json:"tone,omitempty"`
	// Details is an expression over the item giving text the cell shows in full on demand, its value in one line.
	Details string `json:"details,omitempty"`
	// Pick is the verb of the button of a table row whose input the cell picks from the button's options.
	Pick string `json:"pick,omitempty"`
}

// Action is a button. Params, Screen, Record, Project and Until take a fixed value or "=<expression>" over
// the item the button is on.
type Action struct {
	// Label is the text of the button; Verb names a built-in label the client translates.
	Label   string            `json:"label,omitempty"`
	Verb    string            `json:"verb,omitempty"`
	Icon    string            `json:"icon,omitempty"`
	Command string            `json:"command,omitempty"`
	Screen  string            `json:"screen,omitempty"`
	Params  map[string]string `json:"params,omitempty"`
	// FromBlock are the fields of the command a custom block fills itself; the call takes no other but Params.
	FromBlock []string `json:"from_block,omitempty"`
	// Record is the record a move to a screen opens, the item by default; Project the project of that screen.
	Record  string `json:"record,omitempty"`
	Project string `json:"project,omitempty"`
	// When shows the button only on the items it holds for; Until is the time after which it is gone.
	When  string `json:"when,omitempty"`
	Until string `json:"until,omitempty"`
	// Input is the param the owner types before the call, or picks from Options; Confirm asks the owner first.
	Input   string `json:"input,omitempty"`
	Options string `json:"options,omitempty"`
	// Chat opens the chat topic the result of the call names.
	Chat bool `json:"chat,omitempty"`
	// Download downloads the stored file the result of the call names.
	Download bool `json:"download,omitempty"`
	Confirm  bool `json:"confirm,omitempty"`
	Primary  bool `json:"primary,omitempty"`
	// Undo is the call that takes the button's call back, offered for a moment after it went through.
	Undo *Action `json:"undo,omitempty"`
}
