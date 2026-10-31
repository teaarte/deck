package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	Handler Handler `json:"-"`
	schema  *jsonschema.Schema
	// params are the parameters Def read from the input struct; json the fields of the input that hold a JSON text.
	params []Param
	json   []string
	// foreign marks an input schema from elsewhere, not held to App Intents types; retries one that takes a retry key.
	foreign, retries, keyed bool
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
	// Journals marks a command that writes its own intent to the gateway journal.
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
	// Spends marks a command that costs money: the owner's press of a button never stands for its yes.
	Spends bool
}

// Projects yields the commands declared by project manifests of a workspace; project "" means every project.
type Projects func(ctx context.Context, workspace int64, project string) ([]*Command, error)

type Registry struct {
	builtins map[string]*Command
	projects Projects
	extra    func(ctx context.Context) []*Command
	issued   bool
}

// SetExtra adds builtin tools whose descriptions come from a running server rather than from Go.
func (r *Registry) SetExtra(f func(ctx context.Context) []*Command) { r.extra = f }

func (r *Registry) extras(ctx context.Context) []*Command {
	if r.extra == nil {
		return nil
	}
	return r.extra(ctx)
}

func New(projects Projects) *Registry {
	return &Registry{builtins: map[string]*Command{}, projects: projects}
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
		in, err := withRetryKey(c.Input)
		if err != nil {
			return fmt.Errorf("command %q input: %w", c.Name, err)
		}
		c.Input, c.Handler, c.keyed = in, keyed(c.Handler), true
	}
	s, err := compile(c.Name, c.Input)
	if err != nil {
		return fmt.Errorf("command %q input: %w", c.Name, err)
	}
	c.schema = s
	c.json = jsonFields(c.Input)
	return nil
}

// withRetryKey is an input schema with the optional retry key among its properties.
func withRetryKey(in json.RawMessage) (json.RawMessage, error) {
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
	props[RetryKey] = map[string]any{"type": "string",
		"description": "Optional: while a process of this command with the same key is still going, the call returns it instead of starting another"}
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
	pcs, err := r.projects(ctx, a.Workspace, "")
	if err != nil {
		return nil, err
	}
	for _, c := range append(r.extras(ctx), pcs...) {
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
	for _, c := range r.extras(ctx) {
		if c.Name == name {
			return c, nil
		}
	}
	project, _, ok := strings.Cut(name, ".")
	if ok && project != Builtin {
		pcs, err := r.projects(ctx, workspace, project)
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
	if a.Kind == Owner {
		a.Allows, input = allows(input)
	} else if got, _ := allows(input); got != "" {
		return nil, Invalid("%s comes only with a press of the owner", AllowsKey)
	}
	c, input, err := r.Check(ctx, a, name, input)
	if err != nil {
		return nil, err
	}
	return c.Handler(ctx, a, input)
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
	input = c.jsonText(input)
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

// Typed adapts a handler that takes a decoded input struct.
func Typed[T any](f func(ctx context.Context, a Actor, in T) (any, error)) Handler {
	return func(ctx context.Context, a Actor, raw json.RawMessage) (any, error) {
		var in T
		if err := json.Unmarshal(raw, &in); err != nil {
			return nil, Invalid("%v", err)
		}
		return f(ctx, a, in)
	}
}

func compile(name string, raw json.RawMessage) (*jsonschema.Schema, error) {
	return schemas.Compile(raw, "mem://input/"+name+".json")
}
