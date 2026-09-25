package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	Owner   = "owner"
	Master  = "master"
	Agent   = "agent"
	Trigger = "trigger"
)

const (
	Internal   = "internal"
	Reversible = "reversible"
	External   = "external"
)

// Builtin is the name prefix of commands implemented in Go.
const Builtin = "deck"

// Actor is who invokes a command; agents and triggers act inside one project.
type Actor struct {
	Kind      string
	ProjectID int64
	ProcessID int64
}

// Author is the actor as recorded in document revisions.
func (a Actor) Author() string {
	if a.Kind == Agent && a.ProcessID != 0 {
		return fmt.Sprintf("agent:%d", a.ProcessID)
	}
	return a.Kind
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

	Handler Handler `json:"-"`
	schema  *jsonschema.Schema
}

// Error carries the text shown verbatim to the owner and the agent, and an HTTP status.
type Error struct {
	Status int
	Text   string
}

func (e *Error) Error() string { return e.Text }

func Refuse(format string, a ...any) error {
	return &Error{http.StatusForbidden, fmt.Sprintf(format, a...)}
}

func Invalid(format string, a ...any) error {
	return &Error{http.StatusUnprocessableEntity, fmt.Sprintf(format, a...)}
}

func NotFound(format string, a ...any) error {
	return &Error{http.StatusNotFound, fmt.Sprintf(format, a...)}
}

// Status maps an invocation error to an HTTP status.
func Status(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return http.StatusInternalServerError
}

// Projects yields the commands declared by project manifests; project "" means every project.
type Projects func(ctx context.Context, project string) ([]*Command, error)

type Registry struct {
	builtins map[string]*Command
	projects Projects
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
	r.builtins[c.Name] = c
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
	if err := CheckInput(c.Input); err != nil {
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
		if slices.Contains(c.Callers, a.Kind) {
			out = append(out, c)
		}
	}
	pcs, err := r.projects(ctx, "")
	if err != nil {
		return nil, err
	}
	for _, c := range pcs {
		if r.allowed(a, c) == nil {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func (r *Registry) Lookup(ctx context.Context, name string) (*Command, error) {
	if c := r.builtins[name]; c != nil {
		return c, nil
	}
	project, _, ok := strings.Cut(name, ".")
	if ok && project != Builtin {
		pcs, err := r.projects(ctx, project)
		if err != nil {
			return nil, err
		}
		for _, c := range pcs {
			if c.Name == name {
				return c, nil
			}
		}
	}
	return nil, NotFound("deck: unknown command %q", name)
}

// Invoke checks the caller, validates the input against the command schema and runs the command.
func (r *Registry) Invoke(ctx context.Context, a Actor, name string, input json.RawMessage) (any, error) {
	c, err := r.Lookup(ctx, name)
	if err != nil {
		return nil, err
	}
	if err := r.allowed(a, c); err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(input)) == 0 {
		input = json.RawMessage(`{}`)
	}
	v, err := jsonschema.UnmarshalJSON(bytes.NewReader(input))
	if err != nil {
		return nil, Invalid("deck: input of %s is not JSON: %v", name, err)
	}
	if err := c.schema.Validate(v); err != nil {
		return nil, Invalid("deck: input of %s does not match its schema: %v", name, err)
	}
	return c.Handler(ctx, a, input)
}

func (r *Registry) allowed(a Actor, c *Command) error {
	if !slices.Contains(c.Callers, a.Kind) {
		return Refuse("deck: %s cannot be called by %s", c.Name, a.Kind)
	}
	if c.ProjectID != 0 && (a.Kind == Agent || a.Kind == Trigger) && a.ProjectID != c.ProjectID {
		return Refuse("deck: %s belongs to another project", c.Name)
	}
	if a.Kind == Agent && c.Class != Internal {
		return Refuse("deck: %s is %s; agents cannot call it yet", c.Name, c.Class)
	}
	return nil
}

// ToolName is the MCP tool name of a command: builtins drop the prefix, project commands use "__".
func ToolName(name string) string {
	if rest, ok := strings.CutPrefix(name, Builtin+"."); ok {
		return rest
	}
	return strings.Replace(name, ".", "__", 1)
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
