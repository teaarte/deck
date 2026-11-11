package registry

import (
	"bytes"
	"cmp"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"deck2/schemas"
)

// Who may call a command: any caller, which is what a project command declares by default; the owner, the
// master and agents, which builtins and tools take; or the owner side; or the owner alone.
var (
	AnyCaller = []string{Owner, Master, Agent, Trigger}
	Everyone  = []string{Owner, Master, Agent}
	OwnerSide = []string{Owner, Master}
	OwnerOnly = []string{Owner}
)

const (
	Internal   = "internal"
	Reversible = "reversible"
	External   = "external"
)

// Builtin is the name prefix of commands implemented in Go.
const Builtin = schemas.Builtin

// Deck is the name of deck's command name: deck.<name>.
func Deck(name string) string { return Builtin + "." + name }

// IsBuiltin says a command name is one of deck's.
func IsBuiltin(name string) bool { return strings.HasPrefix(name, Builtin+".") }

// ClassRank orders classes from the one that asks least: internal, reversible, external.
func ClassRank(class string) int {
	switch class {
	case Reversible:
		return 1
	case External:
		return 2
	}
	return 0
}

// EmptyInput is the input schema of a command that takes nothing.
var EmptyInput = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)

type Handler func(ctx context.Context, a Actor, in json.RawMessage) (any, error)

type Command struct {
	Name        string          `json:"name"`
	Title       string          `json:"title"`
	Description string          `json:"description"`
	Input       json.RawMessage `json:"input"`
	Result      json.RawMessage `json:"result,omitempty"`
	Class       string          `json:"class"`
	Callers     []string        `json:"callers"`
	ProjectID   int64           `json:"-"`
	// Words are the translations of the texts of a command of a module by language, from its manifest.
	Words map[string]map[string]string `json:"-"`
	// Readable marks a builtin command that changes nothing; only such a command fills a screen block. Topics are
	// the events after which its result may differ.
	Readable bool     `json:"-"`
	Topics   []string `json:"-"`
	// Gate is how the gateway lets a call that is not internal through.
	Gate Gate `json:"-"`
	// Answer is what the tool of a builtin tells an agent beyond its description: its answer and an example input.
	Answer string `json:"-"`

	Handler Handler `json:"-"`
	schema  *jsonschema.Schema
	// params are the parameters Def read from the input struct; json the fields of the input that hold a JSON text.
	params []Param
	json   []string
	// brief are the fields of each row a read gives when the call asks for none.
	brief []string
	// follows are the reads of deck whose topics this read follows too.
	follows []string
	// foreign marks an input schema from elsewhere, not held to App Intents types; retries one that takes a retry key;
	// untokened one no automation token calls; page one a page reads that is not a read of its own.
	foreign, retries, keyed, untokened, page bool
	// asked marks a schema the registry gave the field why, owns one with a why of its own.
	asked, owns bool
}

// Loose declares a command not written in Go: of a module, an HTTP tool, an MCP server or the browser, its input
// schema given whole; Prepare checks it.
func Loose(name, title, desc, class string, callers []string, input json.RawMessage, h Handler) *Command {
	return &Command{Name: name, Title: title, Description: desc, Class: class, Callers: callers,
		Input: input, Handler: h}
}

// Foreign marks a command whose input schema comes from elsewhere: it is not held to App Intents types.
func (c *Command) Foreign() *Command {
	c.foreign = true
	return c
}

// NoTokens keeps a command from automation tokens, whatever commands they list.
func (c *Command) NoTokens() *Command {
	c.untokened = true
	return c
}

// Answers gives a builtin the shape of its answer and an example input its tool tells an agent.
func (c *Command) Answers(text string) *Command {
	c.Answer = text
	return c
}

// Page marks a command a page of the client reads that changes nothing, though not a read with topics.
func (c *Command) Page() *Command {
	c.page = true
	return c
}

// Reads says a caller that only reads may call the command.
func (c *Command) Reads() bool { return c.Readable || c.page }

// Retried gives a command of a module the retry key in its input; the registry hands it to the handler as Actor.Key.
func (c *Command) Retried() *Command {
	c.retries = true
	return c
}

// RetryKey is the optional input field of every command of a module: a call with the key of a running process
// returns it.
const RetryKey = "retry_key"

// Gate is the policy of the gateway for the calls of a command.
type Gate struct {
	// Journals marks a command that writes its own line to the gateway journal.
	Journals bool
	// Fingerprint is what a call would act on beside its input, a tool's request or what it changes, as it is now, so
	// an old yes does not make a changed call.
	Fingerprint func(ctx context.Context, workspace, project int64, input json.RawMessage) string
	// Target says where a call goes, from its fingerprint, for the owner who decides on it.
	Target func(print string, input json.RawMessage) string
	// ConfirmTTL is the tool's own term for its confirmations.
	ConfirmTTL time.Duration
	// Allowed decides whether an external call with this input runs without the owner; listed says the project
	// allows it.
	Allowed func(ctx context.Context, a Actor, input json.RawMessage, listed bool) (bool, error)
	// Free says a call costs nothing and stays on the owner's machine, so it runs in a trial or a draft too; an
	// error is a call that cannot run at all, so it fails rather than waiting for the owner.
	Free func(ctx context.Context, a Actor, input json.RawMessage) (bool, error)
	// Probes marks a Fingerprint that reaches out, such as to the page of a browser: deck checks it only when the
	// owner answers, not in the background.
	Probes bool
	// Holds says what a call would make is so already: a call waiting for the owner is then done without making it.
	Holds func(ctx context.Context, workspace int64, input json.RawMessage) bool
	// Undo is the call that takes a call back, as things stand before it is made; false when deck cannot.
	Undo func(ctx context.Context, workspace int64, input json.RawMessage) (string, json.RawMessage, bool)
	// OwnYes marks a command only the owner's answer to its ask allows, money or a skill's text: a press never does.
	OwnYes bool
	// Changes are what a call would change, a line a place as the owner reads it, leaving out the places it leaves
	// as they are; none when it changes nothing. print is the Fingerprint.
	Changes func(print string, input json.RawMessage) []string
	// Sets names the places a call sets: a later call of the same caller that sets all of them replaces it while it waits.
	Sets func(input json.RawMessage) []string
}

// Modules yields the commands of a workspace not written in Go: those of module manifests, and deck's tools whose
// descriptions come from a running server under Builtin; project "" means all of them.
type Modules func(ctx context.Context, workspace int64, project string) ([]*Command, error)

type Registry struct {
	builtins map[string]*Command
	modules  Modules
	issued   bool
	// key signs the calls a button deck drew allows; it is kept in deck's folder, so a restart keeps it.
	key []byte
}

// New is a registry of the modules' commands beside deck's own; key signs the calls of buttons.
func New(modules Modules, key []byte) *Registry {
	return &Registry{builtins: map[string]*Command{}, modules: modules, key: key}
}

// Add registers a builtin command; it panics on a malformed description.
func (r *Registry) Add(c *Command) {
	if !IsBuiltin(c.Name) || r.builtins[c.Name] != nil {
		panic("registry: bad or duplicate builtin " + c.Name)
	}
	if err := Prepare(c); err != nil {
		panic(err)
	}
	if c.Readable && ReadOf(c.Name) == nil {
		panic("registry: read " + c.Name + " is not declared")
	}
	r.builtins[c.Name] = c
}

// reads are the builtin reads of deck by name: the same commands in every registry, so screens check against them
// without one.
var reads sync.Map

// Declare makes the reads among deck's commands known before any registry is built; a read follows only topics deck
// publishes.
func Declare(cs []*Command, topics []string) {
	for _, c := range cs {
		for _, name := range c.follows {
			i := slices.IndexFunc(cs, func(o *Command) bool { return o.Name == name && o.Readable })
			if i < 0 {
				panic("registry: read " + c.Name + " follows " + name + ", which is no read of deck")
			}
			for _, t := range cs[i].Topics {
				if !slices.Contains(c.Topics, t) {
					c.Topics = append(c.Topics, t)
				}
			}
		}
	}
	for _, c := range cs {
		if !c.Readable {
			continue
		}
		if err := Prepare(c); err != nil {
			panic(err)
		}
		if len(c.Topics) == 0 {
			panic("registry: read " + c.Name + " names no topic it follows")
		}
		for _, t := range c.Topics {
			if t != Static && !slices.Contains(topics, t) {
				panic("registry: read " + c.Name + " follows " + t + ", which deck does not publish")
			}
		}
		reads.Store(c.Name, c)
	}
}

// ReadOf is the builtin read command name; nil when there is none.
func ReadOf(name string) *Command {
	if c, ok := reads.Load(name); ok {
		return c.(*Command)
	}
	return nil
}

// Prepare checks a command description and compiles its input schema.
func Prepare(c *Command) error {
	if c.Title == "" || c.Description == "" {
		return fmt.Errorf("command %q: title and description are required", c.Name)
	}
	if c.Class != Internal && c.Class != Reversible && c.Class != External {
		return fmt.Errorf("command %q: unknown class %q", c.Name, c.Class)
	}
	if len(c.Input) == 0 {
		c.Input = EmptyInput
	}
	if err := schemas.CheckInput(c.Input); err != nil && !c.foreign {
		return fmt.Errorf("command %q input: %w", c.Name, err)
	}
	if c.retries && !c.keyed {
		in, err := withField(c.Input, RetryKey, "Optional: while a process of this command with the same key is still going, the call returns it instead of starting another")
		if err != nil {
			return fmt.Errorf("command %q input: %w", c.Name, err)
		}
		c.Input, c.Handler, c.keyed = in, keyed(c.Handler), true
	}
	if !c.asked {
		c.owns = hasField(c.Input, WhyKey)
	}
	if c.Class != Internal && !c.asked && !c.owns {
		in, err := withField(c.Input, WhyKey, whyField)
		if err != nil {
			return fmt.Errorf("command %q input: %w", c.Name, err)
		}
		c.Input, c.asked = in, true
	}
	s, err := compile(c.Name, c.Input)
	if err != nil {
		return fmt.Errorf("command %q input: %w", c.Name, err)
	}
	c.schema = s
	c.json = jsonFields(c.Input)
	return nil
}

// WhyKey is the field of the input of a call that may wait for the owner's yes: why the caller asks, in its words.
const WhyKey = "why"

const whyField = "Why you call it: one short sentence for the owner, in the language deck gives; a call that waits for the owner's yes is refused without it"

// hasField says an input schema has a property of the name.
func hasField(in json.RawMessage, name string) bool {
	var s struct {
		Properties map[string]json.RawMessage `json:"properties"`
	}
	return json.Unmarshal(in, &s) == nil && s.Properties[name] != nil
}

// WhyOf is why a call asks, as its input says.
func WhyOf(input json.RawMessage) string {
	var in struct {
		Why json.RawMessage `json:"why"`
	}
	var why string
	if json.Unmarshal(input, &in) == nil && json.Unmarshal(in.Why, &why) != nil {
		why = string(in.Why)
	}
	return strings.TrimSpace(why)
}

// unasked is an input without the why the registry takes, which no command of its own reads.
func (c *Command) unasked(input json.RawMessage) json.RawMessage {
	if c.owns || !bytes.Contains(input, []byte(`"`+WhyKey+`"`)) {
		return input
	}
	var in map[string]json.RawMessage
	if json.Unmarshal(input, &in) != nil || in[WhyKey] == nil {
		return input
	}
	delete(in, WhyKey)
	out, _ := json.Marshal(in)
	return out
}

// withField is an input schema with an optional string field among its properties.
func withField(in json.RawMessage, name, desc string) (json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(in))
	d.UseNumber()
	var s map[string]any
	if err := d.Decode(&s); err != nil {
		return nil, err
	}
	props, _ := s["properties"].(map[string]any)
	if props == nil {
		props = map[string]any{}
	}
	props[name] = map[string]any{"type": "string", "description": desc}
	s["properties"] = props
	return json.Marshal(s)
}

// keyed is a handler that gets the retry key of its input as Actor.Key and the input without it or the calls a press
// allows, which a call that skips Invoke may still carry.
func keyed(h Handler) Handler {
	return func(ctx context.Context, a Actor, input json.RawMessage) (any, error) {
		var in map[string]json.RawMessage
		if json.Unmarshal(input, &in) == nil && (in[RetryKey] != nil || in[AllowsKey] != nil) {
			if in[RetryKey] != nil {
				_ = json.Unmarshal(in[RetryKey], &a.Key)
			}
			delete(in, RetryKey)
			delete(in, AllowsKey)
			input, _ = json.Marshal(in)
		}
		return h(ctx, a, input)
	}
}

// Reserved says a field of an input is the registry's own, not of the command: the retry key or the calls a press
// allows.
func Reserved(field string) bool { return field == RetryKey || field == AllowsKey }

// Added says a field of the input schema of the command is one the registry added: reserved, or the why it asks.
func (c *Command) Added(field string) bool { return Reserved(field) || field == WhyKey && c.asked }

// jsonFields are the properties of an input schema of format json.
func jsonFields(input json.RawMessage) []string {
	var s struct {
		Properties map[string]struct {
			Format string `json:"format"`
		} `json:"properties"`
	}
	_ = json.Unmarshal(input, &s)
	var out []string
	for name, p := range s.Properties {
		if p.Format == "json" {
			out = append(out, name)
		}
	}
	return out
}

// List returns the commands the actor may call, sorted by name.
func (r *Registry) List(ctx context.Context, a Actor) ([]*Command, error) {
	var out []*Command
	for _, c := range r.builtins {
		if r.reachable(a, c) == nil {
			out = append(out, c)
		}
	}
	pcs, err := r.modules(ctx, a.Workspace, "")
	if err != nil {
		return nil, err
	}
	for _, c := range pcs {
		if r.reachable(a, c) == nil {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// Tr is a text of the command in a language: by the words of its module, else deck's, else as written.
func (c *Command) Tr(deck map[string]string, lang, text string) string {
	if w := c.Words[lang][text]; w != "" {
		return w
	}
	if w := deck[text]; w != "" {
		return w
	}
	return text
}

func (r *Registry) Lookup(ctx context.Context, workspace int64, name string) (*Command, error) {
	if c := r.builtins[name]; c != nil {
		return c, nil
	}
	project, _, ok := strings.Cut(name, ".")
	if ok {
		pcs, err := r.modules(ctx, workspace, project)
		if err != nil {
			return nil, err
		}
		for _, c := range pcs {
			if c.Name == name {
				return c, nil
			}
		}
	}
	if hint := CommandName(name); hint != "" {
		if _, err := r.Lookup(ctx, workspace, hint); err == nil {
			return nil, NotFound("unknown command %q; commands are named by project, as %q", name, hint)
		}
	}
	return nil, NotFound("unknown command %q", name)
}

// CommandName is the command name a tool name stands for, as an agent sees it: records_list, code__run.
func CommandName(tool string) string {
	switch {
	case strings.Contains(tool, "."):
		return ""
	case strings.Contains(tool, "__"):
		return strings.Replace(tool, "__", ".", 1)
	}
	return Deck(tool)
}

// AllowsKey is the field of an input a press of the owner gives the calls its button allows in; the registry takes it
// out into the actor, so no schema has it.
const AllowsKey = "allows"

// Invoke checks the caller, validates the input against the command schema and runs the command.
func (r *Registry) Invoke(ctx context.Context, a Actor, name string, input json.RawMessage) (any, error) {
	a.Why = cmp.Or(a.Why, WhyOf(input))
	signed, input := allows(input)
	switch {
	case signed == "":
	case a.Kind != Owner:
		return nil, Invalid("%s comes only with a press of the owner", AllowsKey)
	default:
		calls, ok := r.verified(name, signed)
		if !ok {
			return nil, Refuse("%s comes only with a button deck drew for %s; press it again", AllowsKey, name)
		}
		a.Allows = calls
	}
	c, input, err := r.Check(ctx, a, name, input)
	if err != nil {
		return nil, err
	}
	if a.ReadOnly && !c.Reads() {
		return nil, Refuse("%s changes deck; this caller only reads", c.Name)
	}
	return c.Handler(ctx, a, input)
}

// SignAllows is the field of the calls a button of command allows, as deck draws it: a client cannot make or move one.
func (r *Registry) SignAllows(command, calls string) string {
	return hex.EncodeToString(r.allowsMAC(command, calls)) + "." + calls
}

// verified are the calls of a signed field when deck signed them for command.
func (r *Registry) verified(command, signed string) (string, bool) {
	mac, calls, ok := strings.Cut(signed, ".")
	got, err := hex.DecodeString(mac)
	return calls, ok && err == nil && hmac.Equal(got, r.allowsMAC(command, calls))
}

func (r *Registry) allowsMAC(command, calls string) []byte {
	m := hmac.New(sha256.New, r.key)
	m.Write([]byte(command + "\x00" + calls))
	return m.Sum(nil)
}

// allows takes the field of the calls a press allows out of an input.
func allows(input json.RawMessage) (string, json.RawMessage) {
	var in map[string]json.RawMessage
	if json.Unmarshal(input, &in) != nil || in[AllowsKey] == nil {
		return "", input
	}
	var out string
	_ = json.Unmarshal(in[AllowsKey], &out)
	delete(in, AllowsKey)
	b, _ := json.Marshal(in)
	return out, b
}

// Check does everything Invoke does except running the command.
func (r *Registry) Check(ctx context.Context, a Actor, name string, input json.RawMessage) (*Command, json.RawMessage, error) {
	c, input, err := r.Resolve(ctx, a, name, input)
	if err == nil && a.Kind != Owner && c.Class != Internal && !a.Cleared(c.Name) {
		err = Refuse("%s is %s; only the gateway calls it for %s", c.Name, c.Class, a.Kind)
	}
	return c, input, err
}

// Resolve checks the caller and the input but not the class rules, which the gateway applies.
func (r *Registry) Resolve(ctx context.Context, a Actor, name string, input json.RawMessage) (*Command, json.RawMessage, error) {
	c, err := r.Lookup(ctx, a.Workspace, name)
	if err != nil {
		return nil, nil, err
	}
	if err := r.reachable(a, c); err != nil {
		return nil, nil, err
	}
	input, err = c.Validate(input)
	return c, input, err
}

// Validate checks an input against the command schema; an empty or null input is an empty object.
func (c *Command) Validate(input json.RawMessage) (json.RawMessage, error) {
	if t := bytes.TrimSpace(input); len(t) == 0 || string(t) == "null" {
		input = json.RawMessage(`{}`)
	}
	input = c.jsonText(c.unasked(input))
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(input))
	if err != nil {
		return nil, Invalid("input of %s is not JSON: %v", c.Name, err)
	}
	if err := c.schema.Validate(v); err != nil {
		return nil, Invalid("input of %s: %s; deck.command_describe gives its parameters", c.Name, schemaProblems(err))
	}
	return input, nil
}

// schemaProblems is each place an input breaks its schema, as "param: problem".
func schemaProblems(err error) string {
	var ve *jsonschema.ValidationError
	if !errors.As(err, &ve) {
		return err.Error()
	}
	var out []string
	for _, u := range ve.BasicOutput().Errors {
		if u.Error == nil {
			continue
		}
		at := strings.TrimPrefix(u.InstanceLocation, "/")
		if at == "" {
			out = append(out, u.Error.String())
		} else {
			out = append(out, at+": "+u.Error.String())
		}
	}
	if len(out) == 0 {
		return err.Error()
	}
	return strings.Join(slices.Compact(out), "; ")
}

// jsonText turns the value of a json param, or of an input field of format json, that came as an object or a list
// into its JSON text.
func (c *Command) jsonText(input json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if len(c.json) == 0 || json.Unmarshal(input, &m) != nil {
		return input
	}
	changed := false
	for _, name := range c.json {
		if v := bytes.TrimSpace(m[name]); len(v) > 0 && (v[0] == '{' || v[0] == '[') {
			m[name], _ = json.Marshal(string(v))
			changed = true
		}
	}
	if !changed {
		return input
	}
	out, _ := json.Marshal(m)
	return out
}

// reachable reports whether the actor may call the command at all, before the class rules of the gateway.
func (r *Registry) reachable(a Actor, c *Command) error {
	if !slices.Contains(c.Callers, a.As()) {
		who := a.Kind
		if a.Run != "" {
			who += " (" + a.Run + ")"
		}
		return Refuse("%s cannot be called by %s; only %s may call it", c.Name, who, strings.Join(c.Callers, ", "))
	}
	if c.untokened && a.Kind == Token {
		return Refuse("%s is not open to automation tokens", c.Name)
	}
	if c.ProjectID != 0 && (a.Kind == Agent || a.Kind == Trigger) && a.ProjectID != c.ProjectID {
		return Refuse("%s belongs to another project", c.Name)
	}
	if !a.MayCall(c.Name) && a.Kind == Token {
		return Refuse("this automation token may not call %s", c.Name)
	}
	if !a.MayCall(c.Name) {
		return Refuse("the command of this agent does not list %s in its calls", c.Name)
	}
	if a.Trial && c.Class != Internal && c.Gate.Fingerprint == nil {
		return Refuse("trial run: %s is %s, so it was declined", c.Name, c.Class)
	}
	return nil
}

// Permits reports whether a list of command names allows a command; "x.*" allows every command under x.
func Permits(patterns []string, name string) bool {
	for _, p := range patterns {
		if p == name || p == "*" || strings.HasSuffix(p, ".*") && strings.HasPrefix(name, strings.TrimSuffix(p, "*")) {
			return true
		}
	}
	return false
}

// ToolName is the MCP tool name of a command: builtins drop the prefix, dots become "__" and other
// characters providers refuse in tool names become "_".
func ToolName(name string) string {
	if rest, ok := strings.CutPrefix(name, Builtin+"."); ok {
		return rest
	}
	return strings.Map(func(r rune) rune {
		if r == '-' || r == '_' || r == '.' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return r
		}
		return '_'
	}, strings.ReplaceAll(name, ".", "__"))
}

// ToolDescription is what the tool of a command tells an agent: its description, then the answer of a builtin, or the
// result of a module's command and an input of the examples of its parameters.
func (c *Command) ToolDescription() string {
	parts := []string{c.Description, c.Answer}
	if res := resultText(c.Result); !IsBuiltin(c.Name) && res != "" {
		parts = append(parts, "Result of its process: "+res+".")
	}
	if ex := exampleInput(c.Input); ex != "" {
		parts = append(parts, "Example input: "+ex+".")
	}
	return strings.Join(slices.DeleteFunc(parts, func(p string) bool { return p == "" }), " ")
}

// resultMost is the bytes of a result schema a tool gives whole; a longer one gives its fields and their types.
const resultMost = 500

// resultText is a result schema without its titles and descriptions, or past resultMost its fields and their types.
func resultText(raw json.RawMessage) string {
	var s any
	if len(raw) == 0 || json.Unmarshal(raw, &s) != nil {
		return ""
	}
	bare(s)
	b, _ := json.Marshal(s)
	if len(b) <= resultMost {
		return string(b)
	}
	var top struct {
		Type       any                        `json:"type"`
		Properties map[string]json.RawMessage `json:"properties"`
	}
	_ = json.Unmarshal(raw, &top)
	if len(top.Properties) == 0 {
		return typeText(top.Type)
	}
	var fields []string
	for _, name := range slices.Sorted(maps.Keys(top.Properties)) {
		var p struct {
			Type any `json:"type"`
		}
		_ = json.Unmarshal(top.Properties[name], &p)
		k, _ := json.Marshal(name)
		fields = append(fields, string(k)+": "+typeText(p.Type))
	}
	return "{" + strings.Join(fields, ", ") + "}"
}

// bare takes the titles and descriptions out of a schema and the schemas within it, not out of the names of fields.
func bare(s any) {
	m, ok := s.(map[string]any)
	if !ok {
		return
	}
	delete(m, "title")
	delete(m, "description")
	if props, ok := m["properties"].(map[string]any); ok {
		for _, p := range props {
			bare(p)
		}
	}
	for _, k := range []string{"items", "additionalProperties", "not"} {
		bare(m[k])
	}
	for _, k := range []string{"anyOf", "oneOf", "allOf", "prefixItems"} {
		if l, ok := m[k].([]any); ok {
			for _, x := range l {
				bare(x)
			}
		}
	}
}

// typeText is the type of a schema as a word, types joined by |; any when it names none.
func typeText(t any) string {
	switch t := t.(type) {
	case string:
		return t
	case []any:
		var ts []string
		for _, x := range t {
			if s, ok := x.(string); ok {
				ts = append(ts, s)
			}
		}
		if len(ts) > 0 {
			return strings.Join(ts, "|")
		}
	}
	return "any"
}

// exampleInput is an input of the first example of each parameter that gives some; empty when none does or a required
// one gives none, as a partial input copied whole would be refused.
func exampleInput(input json.RawMessage) string {
	var s struct {
		Properties map[string]struct {
			Examples []json.RawMessage `json:"examples"`
		} `json:"properties"`
		Required []string `json:"required"`
	}
	if json.Unmarshal(input, &s) != nil {
		return ""
	}
	for _, r := range s.Required {
		if len(s.Properties[r].Examples) == 0 {
			return ""
		}
	}
	var fields []string
	for _, name := range slices.Sorted(maps.Keys(s.Properties)) {
		if ex := s.Properties[name].Examples; len(ex) > 0 {
			var b bytes.Buffer
			if json.Compact(&b, ex[0]) == nil {
				k, _ := json.Marshal(name)
				fields = append(fields, string(k)+": "+b.String())
			}
		}
	}
	if len(fields) == 0 {
		return ""
	}
	return "{" + strings.Join(fields, ", ") + "}"
}

// Typed adapts a handler that takes a decoded input struct.
func Typed[T any](f func(ctx context.Context, a Actor, in T) (any, error)) Handler {
	return func(ctx context.Context, a Actor, raw json.RawMessage) (any, error) {
		var in T
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, Invalid("%v", err)
		}
		return f(inputLang(ctx, raw), a, in)
	}
}

func compile(name string, raw json.RawMessage) (*jsonschema.Schema, error) {
	return schemas.Compile(raw, "mem://input/"+name+".json")
}

// MaxBody is the largest body of a call.
const MaxBody = 32 << 20

// ReadBody reads the body of a call up to max bytes and answers 413 or 400 itself when it cannot.
func ReadBody(w http.ResponseWriter, r *http.Request, max int64) ([]byte, error) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, max))
	var tooBig *http.MaxBytesError
	switch {
	case errors.As(err, &tooBig):
		size := fmt.Sprintf("%d KB", max>>10)
		if max%(1<<20) == 0 {
			size = fmt.Sprintf("%d MB", max>>20)
		}
		http.Error(w, "deck: the body of a call is larger than "+size, http.StatusRequestEntityTooLarge)
	case err != nil:
		http.Error(w, err.Error(), http.StatusBadRequest)
	}
	return body, err
}
