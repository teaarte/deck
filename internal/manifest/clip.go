package manifest

import (
	"fmt"
	"reflect"
	"slices"

	"deck2/internal/registry"
)

// Clip is m with only the rights granted gives, everything Widens sees; an HTTP tool or MCP server that would widen
// them asks on every call, and a new server with secrets is dropped.
func Clip(granted, m *Manifest) *Manifest {
	c := *m
	c.Commands = slices.Clone(m.Commands)
	c.commands = make(map[string]*Command, len(c.Commands))
	var required, asks []string
	for _, g := range granted.Commands {
		required = append(required, g.Requires...)
	}
	for i := range c.Commands {
		cmd := &c.Commands[i]
		if old := granted.Command(cmd.Name); old != nil {
			clipCommand(cmd, old)
			if cmd.Executor.Type != old.Executor.Type || cmd.Executor.Tool != old.Executor.Tool {
				cmd.Class, asks = registry.External, append(asks, cmd.Name)
			}
		} else {
			cmd.Requires = append(slices.Clone(cmd.Requires), added(cmd.Requires, required)...)
			cmd.Executor.Folder = Folder{}
		}
		c.commands[cmd.Name] = cmd
	}
	c.Tools.MCP.Servers = map[string]MCPServer{}
	for name, s := range m.Tools.MCP.Servers {
		if s, ok := clipServer(granted.Tools.MCP.Servers, name, s); ok {
			c.Tools.MCP.Servers[name] = s
		}
	}
	c.NoNetwork = m.NoNetwork || granted.NoNetwork
	c.Domains = Domains{Read: covered(readGrant(granted), m.Domains.Read), Act: covered(granted.Domains.Act, m.Domains.Act)}
	c.AllowedExternal = slices.DeleteFunc(kept(m.AllowedExternal, granted.AllowedExternal), func(n string) bool { return slices.Contains(asks, n) })
	uses := kept(m.Uses(), granted.Uses())
	c.granted = &uses
	c.Tools.HTTP = slices.Clone(m.Tools.HTTP)
	for i, t := range c.Tools.HTTP {
		if len(toolWidens(granted, m, t)) > 0 {
			c.Tools.HTTP[i].Class = registry.External
			c.AllowedExternal = slices.DeleteFunc(c.AllowedExternal, func(n string) bool { return n == t.Name })
		}
	}
	c.Reads = nil
	for _, r := range m.Reads {
		i := slices.IndexFunc(granted.Reads, func(g Read) bool { return g.Project == r.Project })
		if i < 0 {
			continue
		}
		g := granted.Reads[i]
		r.Collections, r.Documents, r.Models = kept(r.Collections, g.Collections), kept(r.Documents, g.Documents), r.Models && g.Models
		if r.Data() && !g.Data() {
			continue
		}
		c.Reads = append(c.Reads, r)
	}
	c.Writes = slices.DeleteFunc(slices.Clone(m.Writes), func(w string) bool { return !granted.MayWrite(w) })
	return &c
}

// Trial is the manifest in force of a trial of a draft: in, what the owner granted, with the reads, writes and models
// of its current manifest m; each call of a model that is not free waits for the owner.
func Trial(in, m *Manifest) *Manifest {
	c := *in
	c.Reads, c.Writes = slices.Clone(m.Reads), slices.Clone(m.Writes)
	uses := m.Uses()
	c.granted = &uses
	return &c
}

// executors name the kinds of executor in what widens rights.
var executors = map[string]string{"agent": "an agent", "script": "a script", "tool": "a tool", "model": "a model"}

// clipCommand gives cmd back what old had where cmd is wider: a milder class, fewer requirements, another
// provider or folder, more calls or callers; another executor asks the owner on each call, as a changed tool does.
func clipCommand(cmd, old *Command) {
	if registry.ClassRank(cmd.Class) < registry.ClassRank(old.Class) {
		cmd.Class = old.Class
	}
	cmd.Requires = append(slices.Clone(cmd.Requires), added(cmd.Requires, old.Requires)...)
	cmd.Provider = old.Provider
	if !cmd.Executor.Folder.IsZero() {
		cmd.Executor.Folder = old.Executor.Folder
	}
	switch {
	case old.Executor.Calls != nil && cmd.Executor.Calls == nil:
		cmd.Executor.Calls = old.Executor.Calls
	case old.Executor.Calls != nil:
		calls := []string{}
		for _, n := range cmd.Executor.Calls {
			if registry.Permits(old.Executor.Calls, n) {
				calls = append(calls, n)
			}
		}
		cmd.Executor.Calls = calls
	}
	if len(added(old.Callable(), cmd.Callable())) > 0 {
		if cmd.Callers = kept(cmd.Callable(), old.Callable()); len(cmd.Callers) == 0 {
			cmd.Callers = old.Callable()
		}
	}
}

// clipServer is MCP server s of m as granted: a new or changed server only without secrets and with every tool
// asking; a granted one with no tools past its filter and no milder classes. False drops it.
func clipServer(granted map[string]MCPServer, name string, s MCPServer) (MCPServer, bool) {
	old, ok := granted[name]
	if !ok || !sameServer(old, s) {
		if len(secretRef.FindAllString(fmt.Sprint(s.Env, s.Args, s.URL, s.Command), -1)) > 0 {
			return s, false
		}
		s.Classes = nil
		return s, true
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
