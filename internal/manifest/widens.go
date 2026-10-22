package manifest

import (
	"fmt"
	"maps"
	"slices"

	"deck2/internal/registry"
)

// Widens lists what next grants that prev does not: allowed external actions, domains, secrets, models, milder classes,
// dropped requirements, providers, callers, MCP servers, reads, writes, network, HTTP tools that skip asking.
func Widens(prev, next *Manifest) []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }
	if prev.NoNetwork && !next.NoNetwork {
		add("no_network removed: agents get their own web")
	}
	for _, n := range added(prev.AllowedExternal, next.AllowedExternal) {
		add("allowed external: %s", n)
	}
	for _, d := range uncovered(readGrant(prev), next.Domains.Read) {
		add("read domain: %s", d)
	}
	for _, d := range uncovered(prev.Domains.Act, next.Domains.Act) {
		add("act domain: %s", d)
	}
	for _, s := range added(serverSecrets(prev), serverSecrets(next)) {
		add("secret: %s", s)
	}
	for _, u := range added(prev.Uses(), next.Uses()) {
		add("calls models through %s", u)
	}
	var required []string
	for _, c := range prev.Commands {
		required = append(required, c.Requires...)
	}
	for _, c := range next.Commands {
		old := prev.Command(c.Name)
		was := Folder{}
		if old != nil {
			was = old.Executor.Folder
		}
		if f := c.Executor.Folder; f != was && f.Only {
			add("command %s: works in the one folder of the project", c.Name)
		} else if f != was && f.Name != "" {
			add("command %s: works in folder %s", c.Name, f.Name)
		}
		if old == nil {
			for _, r := range added(c.Requires, required) {
				add("command %s: new without %s", c.Name, r)
			}
			continue
		}
		if registry.ClassRank(c.Class) < registry.ClassRank(old.Class) {
			add("command %s: class %s instead of %s", c.Name, c.Class, old.Class)
		}
		for _, r := range added(c.Requires, old.Requires) {
			add("command %s: no longer requires %s", c.Name, r)
		}
		if c.Provider != old.Provider {
			add("command %s: provider %q instead of %q", c.Name, c.Provider, old.Provider)
		}
		switch e, was := c.Executor, old.Executor; {
		case e.Type != was.Type:
			add("command %s: runs %s instead of %s", c.Name, executors[e.Type], executors[was.Type])
		case e.Tool != was.Tool:
			add("command %s: runs tool %s instead of %s", c.Name, e.Tool, was.Tool)
		}
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
		for _, who := range added(old.Callable(), c.Callable()) {
			add("command %s: callable by %s", c.Name, who)
		}
	}
	for _, t := range next.Tools.HTTP {
		out = append(out, toolWidens(prev, next, t)...)
	}
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
				was = "external"
			}
			if registry.ClassRank(s.Classes[tool]) < registry.ClassRank(was) {
				add("MCP tool %s.%s: class %s instead of %s", name, tool, s.Classes[tool], was)
			}
		}
	}
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
	for _, w := range next.Writes {
		if !prev.MayWrite(w) {
			add("writes document %s", w)
		}
	}
	return out
}

// toolWidens lists what HTTP tool t of next grants beyond its namesake in prev: secrets, a milder class, a
// changed call that runs without asking.
func toolWidens(prev, next *Manifest, t HTTPTool) []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }
	i := slices.IndexFunc(prev.Tools.HTTP, func(o HTTPTool) bool { return o.Name == t.Name })
	var old HTTPTool
	if i >= 0 {
		old = prev.Tools.HTTP[i]
	}
	for _, sc := range added(toolSecrets(old), toolSecrets(t)) {
		add("secret: %s", sc)
	}
	switch {
	case i < 0 && t.Class != "external":
		add("tool %s: new with class %s", t.Name, t.Class)
	case i >= 0 && registry.ClassRank(t.Class) < registry.ClassRank(old.Class):
		add("tool %s: class %s instead of %s", t.Name, t.Class, old.Class)
	case i >= 0 && (t.Class != "external" || slices.Contains(prev.AllowedExternal, t.Name) && slices.Contains(next.AllowedExternal, t.Name)) && !sameCall(old, t):
		add("tool %s runs without asking: method, URL, headers, auth or body changed", t.Name)
	}
	return out
}
