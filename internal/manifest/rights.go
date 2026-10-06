package manifest

import (
	"bytes"
	"cmp"
	"deck2/internal/registry"
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"regexp"
	"slices"
)

var secretRef = regexp.MustCompile(`\$secret\.([A-Za-z0-9_.-]+)`)

// Widens lists what next grants that prev does not: allowed external actions, domains, secrets, models,
// milder classes, dropped requirements, providers, callers, MCP servers, reads, network, HTTP tools that skip asking.
func Widens(prev, next *Manifest) []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }
	if prev.NoNetwork && !next.NoNetwork {
		add("no_network removed: agents get their own web")
	}
	for _, n := range added(prev.AllowedExternal, next.AllowedExternal) {
		add("allowed external: %s", n)
	}
	for _, d := range added(prev.Domains.Read, next.Domains.Read) {
		add("read domain: %s", d)
	}
	for _, d := range added(prev.Domains.Act, next.Domains.Act) {
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
		if old == nil {
			add("reads project %s", r.Project)
			continue
		}
		for _, c := range added(old.Collections, r.Collections) {
			add("reads collection %s.%s", r.Project, c)
		}
		for _, d := range added(old.Documents, r.Documents) {
			add("reads document %s/%s", r.Project, d)
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

// Registries are the package registries a manifest reaches by naming them, before any grant.
var Registries = []string{"pypi.org", "files.pythonhosted.org", "registry.npmjs.org", "proxy.golang.org", "sum.golang.org"}

// AgentHosts are what the shell of an agent reaches besides the read domains of its project: the registries,
// GitHub, where code and its packages come from, and Hugging Face, where models do.
var AgentHosts = append(slices.Clone(Registries), "github.com", "codeload.github.com", "objects.githubusercontent.com",
	"huggingface.co", "hf.co")

// ShellHosts are the hosts the shell of an agent of m reaches: none without network, every one for "*" in read.
func ShellHosts(m *Manifest) []string {
	switch {
	case m.NoNetwork:
		return nil
	case slices.Contains(m.Domains.Read, "*"):
		return []string{"*"}
	}
	out := slices.Clone(AgentHosts)
	for _, d := range m.Domains.Read {
		if !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
}

// Clip is m with only the rights granted gives, everything Widens sees; an HTTP tool or MCP server that would widen
// them asks on every call, and a new server with secrets is dropped.
func Clip(granted, m *Manifest) *Manifest {
	c := *m
	c.Commands = slices.Clone(m.Commands)
	c.commands = make(map[string]*Command, len(c.Commands))
	var required []string
	for _, g := range granted.Commands {
		required = append(required, g.Requires...)
	}
	for i := range c.Commands {
		cmd := &c.Commands[i]
		if old := granted.Command(cmd.Name); old != nil {
			clipCommand(cmd, old)
		} else {
			cmd.Requires = append(slices.Clone(cmd.Requires), added(cmd.Requires, required)...)
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
	c.Domains = Domains{Read: kept(m.Domains.Read, append(slices.Clone(Registries), granted.Domains.Read...)), Act: kept(m.Domains.Act, granted.Domains.Act)}
	c.AllowedExternal = kept(m.AllowedExternal, granted.AllowedExternal)
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
		r.Collections, r.Documents = kept(r.Collections, g.Collections), kept(r.Documents, g.Documents)
		c.Reads = append(c.Reads, r)
	}
	return &c
}

// clipCommand gives cmd back what old had where cmd is wider: a milder class, fewer requirements, another
// provider, more calls or callers.
func clipCommand(cmd, old *Command) {
	if registry.ClassRank(cmd.Class) < registry.ClassRank(old.Class) {
		cmd.Class = old.Class
	}
	cmd.Requires = append(slices.Clone(cmd.Requires), added(cmd.Requires, old.Requires)...)
	cmd.Provider = old.Provider
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

// kept are the values of next that granted has too.
func kept(next, granted []string) []string {
	var out []string
	for _, n := range next {
		if slices.Contains(granted, n) {
			out = append(out, n)
		}
	}
	return out
}

func added(prev, next []string) []string {
	var out []string
	for _, n := range next {
		if !slices.Contains(prev, n) && !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	return out
}

// Secrets are the secrets the tools and MCP servers of a manifest use, sorted.
func (m *Manifest) Secrets() []string {
	all := serverSecrets(m)
	for _, t := range m.Tools.HTTP {
		all = append(all, toolSecrets(t)...)
	}
	slices.Sort(all)
	return slices.Compact(all)
}

// serverSecrets are the secrets MCP servers of a manifest get in their environment.
func serverSecrets(m *Manifest) []string {
	b, _ := json.Marshal(m.Tools.MCP)
	var out []string
	for _, s := range secretRef.FindAllSubmatch(b, -1) {
		out = append(out, string(s[1]))
	}
	return out
}

// toolSecrets are the secrets an HTTP tool sends: $secret.<name> in its request and the names in auth.
func toolSecrets(t HTTPTool) []string {
	b, _ := json.Marshal([]any{t.URL, t.Headers, t.Body})
	var out []string
	for _, s := range secretRef.FindAllSubmatch(b, -1) {
		out = append(out, string(s[1]))
	}
	var a struct {
		Secret       string `json:"secret"`
		ClientID     string `json:"client_id"`
		ClientSecret string `json:"client_secret"`
		Token        string `json:"token"`
	}
	_ = json.Unmarshal(t.Auth, &a)
	for _, n := range []string{a.Secret, a.ClientID, a.ClientSecret, a.Token} {
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}

func sameCall(a, b HTTPTool) bool {
	return a.Method == b.Method && a.URL == b.URL && reflect.DeepEqual(a.Headers, b.Headers) &&
		bytes.Equal(compact(a.Body), compact(b.Body)) && bytes.Equal(compact(a.Auth), compact(b.Auth))
}

func compact(raw json.RawMessage) []byte {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return raw
	}
	return b.Bytes()
}

// AskEachTime are what waits for the owner on every call: external tools and commands outside allowed_external,
// and the tools of an MCP server without a class.
func (m *Manifest) AskEachTime() []string {
	var out []string
	external := func(name, class string) {
		if cmp.Or(class, "external") == "external" && !slices.Contains(m.AllowedExternal, name) {
			out = append(out, name)
		}
	}
	for _, t := range m.Tools.HTTP {
		external(t.Name, t.Class)
	}
	for _, server := range slices.Sorted(maps.Keys(m.Tools.MCP.Servers)) {
		classes := m.Tools.MCP.Servers[server].Classes
		for _, tool := range slices.Sorted(maps.Keys(classes)) {
			external(server+"."+tool, classes[tool])
		}
		out = append(out, server+".* without a class")
	}
	for _, c := range m.Commands {
		external(c.Name, c.Class)
	}
	return out
}
