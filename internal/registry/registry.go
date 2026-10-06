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
	// Journals marks a command that writes its own intent to the gateway journal.
	Journals bool `json:"-"`
	// Reads marks a builtin command that changes nothing; only such a command fills a screen block. Topics are
	// the events after which its result may differ.
	Reads  bool     `json:"-"`
	Topics []string `json:"-"`
	// changes are the event topics a call that went through changes; the registry publishes them.
	changes []string
	// Loose marks a tool whose input schema comes from elsewhere and is not held to App Intents types.
	Loose bool `json:"-"`
	// Fingerprint is what the tool would do apart from its input, so an old yes does not make a changed tool.
	Fingerprint func(ctx context.Context, project int64) string `json:"-"`
	// Target says where a call goes, from its fingerprint, for the owner who decides on it.
	Target func(print string, input json.RawMessage) string `json:"-"`
	// ConfirmTTL is the tool's own term for its confirmations.
	ConfirmTTL time.Duration `json:"-"`
	// Allowed decides whether an external call with this input runs without the owner; listed says the project
	// allows it.
	Allowed func(ctx context.Context, a Actor, input json.RawMessage, listed bool) (bool, error) `json:"-"`
	// Free says a call costs nothing and stays on the owner's machine, so it runs in a trial or a draft too.
	Free func(ctx context.Context, a Actor, input json.RawMessage) bool `json:"-"`

	Handler Handler `json:"-"`
	schema  *jsonschema.Schema
	// params are the parameters Def read from the input struct.
	params []Param
}

// Projects yields the commands declared by project manifests of a workspace; project "" means every project.
type Projects func(ctx context.Context, workspace int64, project string) ([]*Command, error)

type Registry struct {
	builtins map[string]*Command
	projects Projects
	extra    func(ctx context.Context) []*Command
	issued   bool
	publish  func(topic string)
}

// SetPublish is how the registry tells clients that a call changed what they show.
func (r *Registry) SetPublish(f func(topic string)) { r.publish = f }

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
	if !strings.HasPrefix(c.Name, Builtin+".") || r.builtins[c.Name] != nil {
		panic("registry: bad or duplicate builtin " + c.Name)
	}
	if err := Prepare(c); err != nil {
		panic(err)
	}
	if c.Reads && len(c.Topics) == 0 {
		panic("registry: read " + c.Name + " names no topic it follows")
	}
	r.builtins[c.Name] = c
	if c.Reads {
		reads.Store(c.Name, c)
	}
}

// reads are the builtin reads of deck by name: the same commands in every registry, so screens check against them
// without one.
var reads sync.Map

// Reads is the builtin read command name; nil when there is none.
func Reads(name string) *Command {
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
		c.Input = json.RawMessage(`{"type":"object","properties":{},"additionalProperties":false}`)
	}
	if err := schemas.CheckInput(c.Input); err != nil && !c.Loose {
		return fmt.Errorf("command %q input: %w", c.Name, err)
	}
	s, err := compile(c.Name, c.Input)
	if err != nil {
		return fmt.Errorf("command %q input: %w", c.Name, err)
	}
	c.schema = s
	return nil
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
	if hint := commandName(name); hint != "" {
		if _, err := r.Lookup(ctx, workspace, hint); err == nil {
			return nil, NotFound("deck: unknown command %q; commands are named by project, as %q", name, hint)
		}
	}
	return nil, NotFound("deck: unknown command %q", name)
}

// commandName is the command name a tool name stands for, as an agent sees it: records_list, code__run.
func commandName(tool string) string {
	switch {
	case strings.Contains(tool, "."):
		return ""
	case strings.Contains(tool, "__"):
		return strings.Replace(tool, "__", ".", 1)
	}
	return Builtin + "." + tool
}

// Invoke checks the caller, validates the input against the command schema and runs the command.
func (r *Registry) Invoke(ctx context.Context, a Actor, name string, input json.RawMessage) (any, error) {
	c, input, err := r.Check(ctx, a, name, input)
	if err != nil {
		return nil, err
	}
	out, err := c.Handler(ctx, a, input)
	if err == nil && r.publish != nil {
		for _, t := range c.changes {
			r.publish(t)
		}
	}
	return out, err
}

// Check does everything Invoke does except running the command.
func (r *Registry) Check(ctx context.Context, a Actor, name string, input json.RawMessage) (*Command, json.RawMessage, error) {
	c, input, err := r.Resolve(ctx, a, name, input)
	if err == nil && a.Kind != Owner && c.Class != Internal && !a.Cleared(c.Name) {
		err = Refuse("deck: %s is %s; only the gateway calls it for %s", c.Name, c.Class, a.Kind)
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
		return nil, Invalid("deck: input of %s is not JSON: %v", c.Name, err)
	}
	if err := c.schema.Validate(v); err != nil {
		return nil, Invalid("deck: input of %s: %s; deck.command_describe gives its parameters", c.Name, schemaProblems(err))
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
	if json.Unmarshal(input, &m) != nil {
		return input
	}
	var names []string
	for _, p := range c.params {
		if p.Type == "json" {
			names = append(names, p.Name)
		}
	}
	if c.params == nil {
		var s struct {
			Properties map[string]struct {
				Format string `json:"format"`
			} `json:"properties"`
		}
		_ = json.Unmarshal(c.Input, &s)
		for name, p := range s.Properties {
			if p.Format == "json" {
				names = append(names, name)
			}
		}
	}
	changed := false
	for _, name := range names {
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
		return Refuse("deck: %s cannot be called by %s", c.Name, a.Kind)
	}
	if c.ProjectID != 0 && (a.Kind == Agent || a.Kind == Trigger) && a.ProjectID != c.ProjectID {
		return Refuse("deck: %s belongs to another project", c.Name)
	}
	if a.Kind == Token && !Permits(a.Commands, c.Name) {
		return Refuse("deck: this automation token may not call %s", c.Name)
	}
	if a.Commands != nil && !Permits(a.Commands, c.Name) {
		return Refuse("deck: the command of this agent does not list %s in its calls", c.Name)
	}
	if a.Trial && c.Class != Internal && c.Fingerprint == nil {
		return Refuse("deck: trial run: %s is %s, so it was declined", c.Name, c.Class)
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
			return nil, Invalid("deck: %v", err)
		}
		return f(ctx, a, in)
	}
}

func compile(name string, raw json.RawMessage) (*jsonschema.Schema, error) {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	c := jsonschema.NewCompiler()
	url := "mem://input/" + name + ".json"
	if err := c.AddResource(url, doc); err != nil {
		return nil, err
	}
	return c.Compile(url)
}
