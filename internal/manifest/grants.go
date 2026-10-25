package manifest

import (
	"fmt"
	"maps"
	"reflect"
	"slices"

	"deck2/internal/registry"
)

// say adds a line to what widens rights.
type say func(format string, a ...any)

// right is one right of a manifest: what next grants that prev does not, and c, a copy of m, cut to what granted
// gives. Widens and Clip go through the same rights.
type right struct {
	widens func(prev, next *Manifest, add say)
	clip   func(granted, m, c *Manifest)
}

// commandRight is one right of a command; old is the namesake in force, for a new command one with the
// requirements of every command in force. Rights for a new command apply to new ones too.
type commandRight struct {
	forNew bool
	widens func(old, c *Command, isNew bool, add say)
	clip   func(old, c *Command)
}

var rights = []right{
	{func(prev, next *Manifest, add say) {
		if prev.NoNetwork && !next.NoNetwork {
			add("no_network removed: agents get their own web")
		}
	}, func(granted, m, c *Manifest) { c.NoNetwork = m.NoNetwork || granted.NoNetwork }},
	{func(prev, next *Manifest, add say) {
		for _, n := range added(prev.AllowedExternal, next.AllowedExternal) {
			add("allowed external: %s", n)
		}
	}, func(granted, m, c *Manifest) {
		c.AllowedExternal = slices.DeleteFunc(kept(m.AllowedExternal, granted.AllowedExternal), func(n string) bool {
			old, cmd := granted.Command(n), m.Command(n)
			return old != nil && cmd != nil && otherExecutor(old, cmd)
		})
	}},
	{func(prev, next *Manifest, add say) {
		for _, d := range uncovered(readGrant(prev), next.Domains.Read) {
			add("read domain: %s", d)
		}
	}, func(granted, m, c *Manifest) { c.Domains.Read = covered(readGrant(granted), m.Domains.Read) }},
	{func(prev, next *Manifest, add say) {
		for _, d := range uncovered(prev.Domains.Act, next.Domains.Act) {
			add("act domain: %s", d)
		}
	}, func(granted, m, c *Manifest) { c.Domains.Act = covered(granted.Domains.Act, m.Domains.Act) }},
	// The right of MCP servers below drops a new server, so its secrets are not in force either.
	{func(prev, next *Manifest, add say) {
		for _, s := range added(serverSecrets(prev), serverSecrets(next)) {
			add("secret: %s", s)
		}
	}, func(granted, m, c *Manifest) {}},
	{func(prev, next *Manifest, add say) {
		for _, u := range added(prev.Uses(), next.Uses()) {
			add("calls models through %s", u)
		}
	}, func(granted, m, c *Manifest) {
		uses := kept(m.Uses(), granted.Uses())
		c.granted = &uses
	}},
	{func(prev, next *Manifest, add say) {
		for _, c := range next.Commands {
			old, isNew := inForce(prev, c.Name)
			for _, r := range commandRights {
				if r.forNew || !isNew {
					r.widens(old, &c, isNew, add)
				}
			}
		}
	}, func(granted, m, c *Manifest) {
		c.Commands = slices.Clone(m.Commands)
		c.commands = make(map[string]*Command, len(c.Commands))
		for i := range c.Commands {
			cmd := &c.Commands[i]
			old, isNew := inForce(granted, cmd.Name)
			for _, r := range commandRights {
				if r.forNew || !isNew {
					r.clip(old, cmd)
				}
			}
			c.commands[cmd.Name] = cmd
		}
	}},
	{func(prev, next *Manifest, add say) {
		for _, t := range next.Tools.HTTP {
			toolWidens(prev, next, t, add)
		}
	}, func(granted, m, c *Manifest) {
		c.Tools.HTTP = slices.Clone(m.Tools.HTTP)
		for i, t := range c.Tools.HTTP {
			var w []string
			toolWidens(granted, m, t, func(format string, a ...any) { w = append(w, format) })
			if len(w) > 0 {
				c.Tools.HTTP[i].Class = registry.External
				c.AllowedExternal = slices.DeleteFunc(c.AllowedExternal, func(n string) bool { return n == t.Name })
			}
		}
	}},
	{func(prev, next *Manifest, add say) {
		for _, name := range slices.Sorted(maps.Keys(next.Tools.MCP.Servers)) {
			s := next.Tools.MCP.Servers[name]
			old, ok := prev.Tools.MCP.Servers[name]
			if !ok || !sameServer(old, s) {
				add("MCP server %s: new or changed", name)
			} else if len(old.ToolFilter) > 0 && len(s.ToolFilter) == 0 {
				add("MCP server %s: every tool instead of the tool filter", name)
			} else if len(old.ToolFilter) > 0 {
				for _, t := range added(old.ToolFilter, s.ToolFilter) {
					add("MCP server %s: tool %s", name, t)
				}
			}
			for _, tool := range slices.Sorted(maps.Keys(s.Classes)) {
				was, known := old.Classes[tool]
				if !known {
					was = registry.External
				}
				if registry.ClassRank(s.Classes[tool]) < registry.ClassRank(was) {
					add("MCP tool %s.%s: class %s instead of %s", name, tool, s.Classes[tool], was)
				}
			}
		}
	}, func(granted, m, c *Manifest) {
		c.Tools.MCP.Servers = map[string]MCPServer{}
		for name, s := range m.Tools.MCP.Servers {
			if s, ok := clipServer(granted.Tools.MCP.Servers, name, s); ok {
				c.Tools.MCP.Servers[name] = s
			}
		}
	}},
	{func(prev, next *Manifest, add say) {
		for _, r := range next.Reads {
			var old *Read
			for i := range prev.Reads {
				if prev.Reads[i].Project == r.Project {
					old = &prev.Reads[i]
				}
			}
			if r.Models && (old == nil || !old.Models) {
				add("reads which models do the work of %s", readsWhose(r.Project))
			}
			if r.Runs && (old == nil || !old.Runs) {
				add("reads the runs of %s", readsWhose(r.Project))
			}
			if r.Project != AnyProject && r.Data() && (old == nil || !old.Data()) {
				add("reads project %s", r.Project)
			}
			if old == nil {
				continue
			}
			for _, c := range added(old.Collections, r.Collections) {
				add("reads collection %s.%s", r.Project, c)
			}
			for _, d := range added(old.Documents, r.Documents) {
				add("reads document %s/%s", r.Project, d)
			}
		}
	}, func(granted, m, c *Manifest) {
		c.Reads = nil
		for _, r := range m.Reads {
			i := slices.IndexFunc(granted.Reads, func(g Read) bool { return g.Project == r.Project })
			if i < 0 {
				continue
			}
			g := granted.Reads[i]
			r.Collections, r.Documents, r.Models, r.Runs = kept(r.Collections, g.Collections), kept(r.Documents, g.Documents), r.Models && g.Models, r.Runs && g.Runs
			if r.Data() && !g.Data() {
				continue
			}
			c.Reads = append(c.Reads, r)
		}
	}},
	{func(prev, next *Manifest, add say) {
		for _, w := range next.Writes {
			if !prev.MayWrite(w) {
				add("writes document %s", w)
			}
		}
	}, func(granted, m, c *Manifest) {
		c.Writes = slices.DeleteFunc(slices.Clone(m.Writes), func(w string) bool { return !granted.MayWrite(w) })
	}},
}

var commandRights = []commandRight{
	{true, func(old, c *Command, _ bool, add say) {
		if f, was := c.Executor.Folder, old.Executor.Folder; f != was && f.Only {
			add("command %s: works in the one folder of the project", c.Name)
		} else if f != was && f.Name != "" {
			add("command %s: works in folder %s", c.Name, f.Name)
		}
	}, func(old, c *Command) {
		if !c.Executor.Folder.IsZero() {
			c.Executor.Folder = old.Executor.Folder
		}
	}},
	{false, func(old, c *Command, _ bool, add say) {
		if registry.ClassRank(c.Class) < registry.ClassRank(old.Class) {
			add("command %s: class %s instead of %s", c.Name, c.Class, old.Class)
		}
	}, func(old, c *Command) {
		if registry.ClassRank(c.Class) < registry.ClassRank(old.Class) {
			c.Class = old.Class
		}
	}},
	{true, func(old, c *Command, isNew bool, add say) {
		for _, r := range added(c.Requires, old.Requires) {
			if isNew {
				add("command %s: new without %s", c.Name, r)
			} else {
				add("command %s: no longer requires %s", c.Name, r)
			}
		}
	}, func(old, c *Command) {
		c.Requires = append(slices.Clone(c.Requires), added(c.Requires, old.Requires)...)
	}},
	{false, func(old, c *Command, _ bool, add say) {
		if c.Provider != old.Provider {
			add("command %s: provider %q instead of %q", c.Name, c.Provider, old.Provider)
		}
	}, func(old, c *Command) { c.Provider = old.Provider }},
	// Another executor asks the owner on each call, as a changed tool does.
	{false, func(old, c *Command, _ bool, add say) {
		switch e, was := c.Executor, old.Executor; {
		case e.Type != was.Type:
			add("command %s: runs %s instead of %s", c.Name, executors[e.Type], executors[was.Type])
		case e.Tool != was.Tool:
			add("command %s: runs tool %s instead of %s", c.Name, e.Tool, was.Tool)
		}
	}, func(old, c *Command) {
		if otherExecutor(old, c) {
			c.Class = registry.External
		}
	}},
	{false, func(old, c *Command, _ bool, add say) {
		switch {
		case old.Executor.Calls != nil && c.Executor.Calls == nil:
			add("command %s: its agent may call every command", c.Name)
		case old.Executor.Calls != nil:
			for _, n := range c.Executor.Calls {
				if !registry.Permits(old.Executor.Calls, n) {
					add("command %s: its agent may call %s", c.Name, n)
				}
			}
		}
	}, func(old, c *Command) {
		switch {
		case old.Executor.Calls != nil && c.Executor.Calls == nil:
			c.Executor.Calls = old.Executor.Calls
		case old.Executor.Calls != nil:
			calls := []string{}
			for _, n := range c.Executor.Calls {
				if registry.Permits(old.Executor.Calls, n) {
					calls = append(calls, n)
				}
			}
			c.Executor.Calls = calls
		}
	}},
	{false, func(old, c *Command, _ bool, add say) {
		for _, who := range added(old.Callable(), c.Callable()) {
			add("command %s: callable by %s", c.Name, who)
		}
	}, func(old, c *Command) {
		if len(added(old.Callable(), c.Callable())) > 0 {
			if c.Callers = kept(c.Callable(), old.Callable()); len(c.Callers) == 0 {
				c.Callers = old.Callable()
			}
		}
	}},
}

// Widens lists what next grants that prev does not: allowed external actions, domains, secrets, models, milder classes,
// dropped requirements, providers, callers, MCP servers, reads, writes, network, HTTP tools that skip asking.
func Widens(prev, next *Manifest) []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }
	for _, r := range rights {
		r.widens(prev, next, add)
	}
	return out
}

// Clip is m with only the rights granted gives, everything Widens sees; an HTTP tool or a command that would widen
// them asks on every call, and an MCP server runs only as granted: a new one does not run.
func Clip(granted, m *Manifest) *Manifest {
	c := *m
	for _, r := range rights {
		r.clip(granted, m, &c)
	}
	return &c
}

// Trial is the manifest in force of a trial of a draft: what the owner granted, with the writes and models of its
// current manifest m but none of its reads; each call of a model that is not free waits for the owner.
func Trial(in, m *Manifest) *Manifest {
	c := *in
	c.Writes = slices.Clone(m.Writes)
	uses := m.Uses()
	c.granted = &uses
	return &c
}

// inForce is the command of m named name, or for a new one a command with the requirements of every command of m.
func inForce(m *Manifest, name string) (*Command, bool) {
	if old := m.Command(name); old != nil {
		return old, false
	}
	var required []string
	for _, c := range m.Commands {
		required = append(required, c.Requires...)
	}
	return &Command{Requires: required}, true
}

func otherExecutor(old, c *Command) bool {
	return c.Executor.Type != old.Executor.Type || c.Executor.Tool != old.Executor.Tool
}

// executors name the kinds of executor in what widens rights.
var executors = map[string]string{"agent": "an agent", "script": "a script", "tool": "a tool", "model": "a model"}

// toolWidens tells what HTTP tool t of next grants beyond its namesake in prev: secrets, a milder class, a changed
// call that runs without asking.
func toolWidens(prev, next *Manifest, t HTTPTool, add say) {
	i := slices.IndexFunc(prev.Tools.HTTP, func(o HTTPTool) bool { return o.Name == t.Name })
	var old HTTPTool
	if i >= 0 {
		old = prev.Tools.HTTP[i]
	}
	for _, sc := range added(toolSecrets(old), toolSecrets(t)) {
		add("secret: %s", sc)
	}
	switch {
	case i < 0 && t.Class != registry.External:
		add("tool %s: new with class %s", t.Name, t.Class)
	case i >= 0 && registry.ClassRank(t.Class) < registry.ClassRank(old.Class):
		add("tool %s: class %s instead of %s", t.Name, t.Class, old.Class)
	case i >= 0 && (t.Class != registry.External || slices.Contains(prev.AllowedExternal, t.Name) && slices.Contains(next.AllowedExternal, t.Name)) && !sameCall(old, t):
		add("tool %s runs without asking: method, URL, headers, auth or body changed", t.Name)
	}
}

// clipServer is MCP server s of m as granted: a new one is not in force, a changed one runs as granted, with no
// tools past its filter and no milder classes. False drops it.
func clipServer(granted map[string]MCPServer, name string, s MCPServer) (MCPServer, bool) {
	old, ok := granted[name]
	if !ok {
		return s, false
	}
	if !sameServer(old, s) {
		s = old
	}
	if len(old.ToolFilter) > 0 && len(s.ToolFilter) == 0 {
		s.ToolFilter = old.ToolFilter
	} else if len(old.ToolFilter) > 0 {
		if s.ToolFilter = kept(s.ToolFilter, old.ToolFilter); len(s.ToolFilter) == 0 {
			return s, false
		}
	}
	classes := map[string]string{}
	for tool, class := range s.Classes {
		was, known := old.Classes[tool]
		if !known {
			was = registry.External
		}
		if registry.ClassRank(class) < registry.ClassRank(was) {
			class = was
		}
		classes[tool] = class
	}
	s.Classes = classes
	return s, true
}

func sameServer(a, b MCPServer) bool {
	return a.Command == b.Command && slices.Equal(a.Args, b.Args) && a.URL == b.URL && reflect.DeepEqual(a.Env, b.Env)
}
