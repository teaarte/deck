package manifest

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"deck2/schemas"
)

func (m *Manifest) compile() error {
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }

	m.scripts = map[string]*Script{}
	for i := range m.Scripts {
		s := &m.Scripts[i]
		if m.scripts[s.Name] != nil {
			fail("script %q: duplicate", s.Name)
		}
		m.scripts[s.Name] = s
	}
	m.compileCollections(fail)
	m.compileCommands(fail)
	m.checkTools(fail)
	m.checkTriggers(fail)
	m.checkBoard(fail)
	m.checkScreens(fail)
	for _, name := range m.AllowedExternal {
		if !m.knownAction(name) {
			fail("allowed_external: %q is neither a command nor a tool of this project", name)
		}
	}
	for i, r := range m.Reads {
		if r.Project == "" {
			fail("reads[%d]: project is required", i)
		}
	}
	return errors.Join(errs...)
}

func (m *Manifest) compileCollections(fail func(string, ...any)) {
	m.collections = map[string]*Collection{}
	for i := range m.Collections {
		c := &m.Collections[i]
		if m.collections[c.Name] != nil {
			fail("collection %q: duplicate", c.Name)
		}
		m.collections[c.Name] = c
		var err error
		if c.schema, err = compileSchema("collection-"+c.Name, c.Schema); err != nil {
			fail("collection %q schema: %v", c.Name, err)
		}
		c.fields = schemaFields(c.Schema)
		if c.keyProg, err = compileExpr(c.Key, c.fields); err != nil {
			fail("collection %q key: %v", c.Name, err)
		}
		c.keyFields = exprFields(c.Key, c.fields)
		if hasLet(c.Key) {
			fail("collection %q key: variables (let) are not allowed in a key", c.Name)
		}
		if c.Display != "" {
			if c.displayProg, err = compileField(c.Display, c.fields); err != nil {
				fail("collection %q display: %v", c.Name, err)
			}
		}
		for _, f := range c.Immutable {
			if c.fields != nil && !slices.Contains(c.fields, f) {
				fail("collection %q immutable: unknown field %q", c.Name, f)
			}
		}
		if c.Migration != "" && m.scripts[c.Migration] == nil {
			fail("collection %q migration: unknown script %q", c.Name, c.Migration)
		}
	}
}

func (m *Manifest) compileCommands(fail func(string, ...any)) {
	m.commands = map[string]*Command{}
	for i := range m.Commands {
		c := &m.Commands[i]
		if m.commands[c.Name] != nil {
			fail("command %q: duplicate", c.Name)
		}
		m.commands[c.Name] = c
		var err error
		if len(c.Input) > 0 {
			if err := schemas.CheckInput(c.Input); err != nil {
				fail("command %q input: parameters must use App Intents types: %v", c.Name, err)
			} else if c.input, err = compileSchema("input-"+c.Name, c.Input); err != nil {
				fail("command %q input: %v", c.Name, err)
			}
		}
		if len(c.Result) > 0 {
			if c.result, err = compileSchema("result-"+c.Name, c.Result); err != nil {
				fail("command %q result: %v", c.Name, err)
			}
		}
		switch e := c.Executor; e.Type {
		case "script":
			if m.scripts[e.Script] == nil {
				fail("command %q: unknown script %q", c.Name, e.Script)
			}
		case "tool":
			if !m.knownTool(e.Tool) {
				fail("command %q: unknown tool %q", c.Name, e.Tool)
			}
		}
	}
}

func (m *Manifest) checkTools(fail func(string, ...any)) {
	seen := map[string]bool{}
	for _, t := range m.Tools.HTTP {
		if seen[t.Name] {
			fail("http tool %q: duplicate", t.Name)
		}
		seen[t.Name] = true
		if m.commands[t.Name] != nil {
			fail("http tool %q: a command has the same name", t.Name)
		}
		if len(t.Input) > 0 {
			if _, err := compileSchema("tool-input-"+t.Name, t.Input); err != nil {
				fail("http tool %q input: %v", t.Name, err)
			}
		}
		if len(t.Result) > 0 {
			if _, err := compileSchema("tool-result-"+t.Name, t.Result); err != nil {
				fail("http tool %q result: %v", t.Name, err)
			}
		}
	}
}

// knownTool reports whether a tool reference resolves: an http tool, "<mcp server>.<tool>" or a builtin "deck.<tool>".
func (m *Manifest) knownTool(name string) bool {
	if slices.ContainsFunc(m.Tools.HTTP, func(t HTTPTool) bool { return t.Name == name }) {
		return true
	}
	server, _, ok := strings.Cut(name, ".")
	if !ok {
		return false
	}
	_, declared := m.Tools.MCP.Servers[server]
	return declared || server == schemas.Builtin
}

func (m *Manifest) knownAction(name string) bool {
	return m.commands[name] != nil || m.knownTool(name)
}

func (m *Manifest) checkTriggers(fail func(string, ...any)) {
	lanes := map[string]bool{}
	if m.Board != nil {
		for _, l := range m.Board.Lanes {
			lanes[l.Name] = true
		}
	}
	after := map[string][]string{}
	for i, t := range m.Triggers {
		where := fmt.Sprintf("triggers[%d]", i)
		for j := range i {
			if reflect.DeepEqual(m.Triggers[j], t) {
				fail("%s: duplicate trigger for %q", where, t.Command)
			}
		}
		c := m.commands[t.Command]
		if c == nil {
			fail("%s: unknown command %q", where, t.Command)
		} else {
			if !slices.Contains(c.Callable(), "trigger") {
				fail("%s: command %q cannot be called by triggers", where, t.Command)
			}
			in := TriggerInput(t, 1)
			for _, f := range []string{"card", "process"} {
				if _, ok := in[f]; ok && !slices.Contains(schemaFields(c.Input), f) {
					fail("%s: command %q must declare input field %q to be triggered this way", where, t.Command, f)
				}
			}
			switch {
			case c.input != nil:
				if err := c.input.Validate(normalize(in)); err != nil {
					fail("%s: params do not match the input of %q: %v", where, t.Command, err)
				}
			case len(c.Input) == 0 && len(in) > 0:
				fail("%s: command %q takes no input; lane triggers pass card, after triggers pass process", where, t.Command)
			}
		}
		switch {
		case t.Cron != "":
			for j := range i {
				if u := m.Triggers[j]; u.Cron == t.Cron && u.Command == t.Command && !reflect.DeepEqual(u, t) {
					fail("%s: %q already has a trigger with cron %q", where, t.Command, t.Cron)
				}
			}
			if _, err := CronParser.Parse(t.Cron); err != nil {
				fail("%s: cron %q: %v", where, t.Cron, err)
			}
		case t.Lane != "":
			if !lanes[t.Lane] {
				fail("%s: unknown lane %q", where, t.Lane)
			}
		case t.After != "":
			if m.commands[t.After] == nil {
				fail("%s: unknown command %q in after", where, t.After)
			}
			after[t.After] = append(after[t.After], t.Command)
		}
	}
	if cycle := findCycle(after); cycle != nil {
		fail("triggers: commands trigger each other in a cycle: %s", strings.Join(cycle, " → "))
	}
}

// TriggerInput is the command input a trigger passes: its params, plus the card for lane triggers and the
// finished process for after triggers.
func TriggerInput(t Trigger, ref int64) map[string]any {
	in := map[string]any{}
	for k, v := range t.Params {
		in[k] = v
	}
	switch {
	case t.Lane != "":
		in["card"] = ref
	case t.After != "":
		in["process"] = ref
	}
	return in
}

// findCycle returns one cycle of the "finished → starts" graph, or nil.
func findCycle(edges map[string][]string) []string {
	state := map[string]int{}
	var stack []string
	var visit func(n string) []string
	visit = func(n string) []string {
		state[n] = 1
		stack = append(stack, n)
		for _, next := range edges[n] {
			switch state[next] {
			case 1:
				i := slices.Index(stack, next)
				return append(append([]string{}, stack[i:]...), next)
			case 0:
				if c := visit(next); c != nil {
					return c
				}
			}
		}
		stack = stack[:len(stack)-1]
		state[n] = 2
		return nil
	}
	keys := make([]string, 0, len(edges))
	for k := range edges {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if state[k] == 0 {
			if c := visit(k); c != nil {
				return c
			}
		}
	}
	return nil
}

func (m *Manifest) checkBoard(fail func(string, ...any)) {
	if m.Board == nil {
		return
	}
	names, roles := map[string]bool{}, map[string]bool{}
	for _, l := range m.Board.Lanes {
		if names[l.Name] {
			fail("board: duplicate lane %q", l.Name)
		}
		names[l.Name] = true
		if l.Role != "" {
			if roles[l.Role] {
				fail("board: role %q is used by more than one lane", l.Role)
			}
			roles[l.Role] = true
		}
	}
	var have, miss []string
	for _, r := range RunRoles {
		if roles[r] {
			have = append(have, r)
		} else {
			miss = append(miss, r)
		}
	}
	if len(have) > 0 && len(miss) > 0 {
		fail("board: code runs need lanes with roles %s together; missing %s", strings.Join(RunRoles, ", "), strings.Join(miss, ", "))
	}
}

func (m *Manifest) checkScreens(fail func(string, ...any)) {
	screens := map[string]bool{}
	for _, s := range m.Screens {
		if screens[s.Name] {
			fail("screen %q: duplicate", s.Name)
		}
		screens[s.Name] = true
		for j, b := range s.Blocks {
			where := fmt.Sprintf("screen %q block %d", s.Name, j)
			switch b.Type {
			case "table":
				c := (*Collection)(nil)
				if b.Source != nil {
					c = m.collections[b.Source.Collection]
				}
				if c == nil {
					fail("%s: unknown collection", where)
					continue
				}
				fields := c.fields
				if fields != nil {
					fields = append(slices.Clone(fields), "_id", "_updated")
				}
				if b.Source.Where != "" {
					if _, err := compileField(b.Source.Where, fields); err != nil {
						fail("%s where: %v", where, err)
					}
				}
				if sortField := strings.TrimPrefix(b.Source.Sort, "-"); sortField != "" && fields != nil && !slices.Contains(fields, sortField) {
					fail("%s sort: unknown field %q", where, sortField)
				}
				for _, col := range b.Columns {
					if _, err := compileField(col.Bind, fields); err != nil {
						fail("%s column %q: %v", where, col.Label, err)
					}
				}
				for _, a := range b.Actions {
					for k, v := range a.Params {
						if f, ok := strings.CutPrefix(v, "$record."); ok && f != "id" && fields != nil && !slices.Contains(fields, f) {
							fail("%s action %q param %q: unknown field %q", where, a.Label, k, f)
						}
					}
				}
			case "actions":
			default:
				fail("%s: unknown block type %q", where, b.Type)
			}
			for _, a := range b.Actions {
				if err := m.checkAction(a); err != nil {
					fail("%s action %q: %v", where, a.Label, err)
				}
			}
		}
	}
	for _, name := range m.Menu {
		if !screens[name] {
			fail("menu: unknown screen %q", name)
		}
	}
}

// checkAction verifies that a screen button can call its command: the owner may call it and the params
// cover the required input fields and nothing else.
func (m *Manifest) checkAction(a Action) error {
	c := m.commands[a.Command]
	if c == nil {
		return fmt.Errorf("unknown command %q", a.Command)
	}
	if !slices.Contains(c.Callable(), "owner") {
		return fmt.Errorf("command %q cannot be called by the owner", a.Command)
	}
	var in struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	_ = json.Unmarshal(c.InputSchema(), &in)
	for _, r := range in.Required {
		if _, ok := a.Params[r]; !ok {
			return fmt.Errorf("command %q needs param %q", a.Command, r)
		}
	}
	for k := range a.Params {
		if _, ok := in.Properties[k]; !ok {
			return fmt.Errorf("command %q has no input field %q", a.Command, k)
		}
	}
	return nil
}
