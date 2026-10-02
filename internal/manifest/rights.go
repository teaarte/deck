package manifest

import (
	"bytes"
	"deck2/internal/registry"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
)

var secretRef = regexp.MustCompile(`\$secret\.([A-Za-z0-9_.-]+)`)

// Widens lists what next grants that prev does not: autonomy, allowed external actions, domains, secrets, milder
// classes, dropped requirements, providers, callers, MCP servers, reads, network, changed HTTP tools that skip asking.
func Widens(prev, next *Manifest) []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }
	if next.Autonomy == "auto" && prev.Autonomy != "auto" {
		add("autonomy: auto")
	}
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
			for _, n := range added(old.Executor.Calls, c.Executor.Calls) {
				add("command %s: its agent may call %s", c.Name, n)
			}
		}
		for _, who := range added(old.Callable(), c.Callable()) {
			add("command %s: callable by %s", c.Name, who)
		}
	}
	prevHTTP := map[string]HTTPTool{}
	for _, t := range prev.Tools.HTTP {
		prevHTTP[t.Name] = t
	}
	for _, t := range next.Tools.HTTP {
		old, ok := prevHTTP[t.Name]
		for _, sc := range added(toolSecrets(old), toolSecrets(t)) {
			add("secret: %s", sc)
		}
		switch {
		case !ok && t.Class != "external":
			add("tool %s: new with class %s", t.Name, t.Class)
		case ok && registry.ClassRank(t.Class) < registry.ClassRank(old.Class):
			add("tool %s: class %s instead of %s", t.Name, t.Class, old.Class)
		case ok && (t.Class != "external" || slices.Contains(prev.AllowedExternal, t.Name) && slices.Contains(next.AllowedExternal, t.Name)) && !sameCall(old, t):
			add("tool %s runs without asking: method, URL, headers, auth or body changed", t.Name)
		}
	}
	for _, name := range sortedKeys(next.Tools.MCP.Servers) {
		s := next.Tools.MCP.Servers[name]
		old, ok := prev.Tools.MCP.Servers[name]
		if !ok || old.Command != s.Command || !slices.Equal(old.Args, s.Args) || old.URL != s.URL || !reflect.DeepEqual(old.Env, s.Env) {
			add("MCP server %s: new or changed", name)
		} else if len(old.ToolFilter) > 0 && len(s.ToolFilter) == 0 {
			add("MCP server %s: every tool instead of the tool filter", name)
		} else if len(old.ToolFilter) > 0 {
			for _, t := range added(old.ToolFilter, s.ToolFilter) {
				add("MCP server %s: tool %s", name, t)
			}
		}
		for _, tool := range sortedKeys(s.Classes) {
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

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
