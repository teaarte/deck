package manifest

import (
	"bytes"
	"cmp"
	"deck2/internal/registry"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strings"
)

// sets are the arrays of a manifest whose order means nothing, by path with "*" for any name: a merge adds and
// takes their values one by one.
var sets = []string{"/allowed_external", "/models", "/writes", "/domains/read", "/domains/act", "/reads/*/collections",
	"/reads/*/documents", "/commands/*/callers", "/commands/*/requires", "/commands/*/executor/calls",
	"/commands/*/executor/skills", "/collections/*/immutable", "/tools/mcp/servers/*/toolFilter"}

// named are the arrays of a manifest whose entries a key names, by path: a merge goes entry by entry.
var named = map[string]string{"/collections": "name", "/commands": "name", "/scripts": "name", "/scripts/*/programs": "name",
	"/screens": "name", "/tools/http": "name", "/reads": "project"}

// setPath reports whether the array at path is a set.
func setPath(path string) bool {
	return slices.ContainsFunc(sets, func(p string) bool { return pathIs(path, p) })
}

// keyOf is the key that names the entries of the array at path, empty for an array of other values.
func keyOf(path string) string {
	for p, key := range named {
		if pathIs(path, p) {
			return key
		}
	}
	return ""
}

// pathIs reports whether path is of pattern, "*" standing for any one part.
func pathIs(path, pattern string) bool {
	a, b := strings.Split(path, "/"), strings.Split(pattern, "/")
	if len(a) != len(b) {
		return false
	}
	for i := range b {
		if b[i] != "*" && b[i] != a[i] {
			return false
		}
	}
	return true
}

// MayWrite reports whether the agents and scripts of the project may write the document of this name, or every
// document of a folder named with "/" at its end.
func (m *Manifest) MayWrite(name string) bool {
	return slices.ContainsFunc(m.Writes, func(w string) bool {
		return w == name || strings.HasSuffix(w, "/") && strings.HasPrefix(name, w)
	})
}

// covers says whether granted domains reach domain d: d itself, or "*" for a name; an address or a network is
// reached only when named.
func covers(granted []string, d string) bool {
	if slices.Contains(granted, d) {
		return true
	}
	_, literal := address(d)
	return !literal && slices.Contains(granted, "*")
}

// readGrant are the read domains m reaches without a grant of their own: the registries and its read domains.
func readGrant(m *Manifest) []string { return append(slices.Clone(Registries), m.Domains.Read...) }

// covered are the domains of next the granted ones cover.
func covered(granted, next []string) []string {
	return slices.DeleteFunc(slices.Clone(next), func(d string) bool { return !covers(granted, d) })
}

// uncovered are the domains of next the granted ones do not cover, each once.
func uncovered(granted, next []string) []string {
	var out []string
	for _, d := range next {
		if !covers(granted, d) && !slices.Contains(out, d) {
			out = append(out, d)
		}
	}
	return out
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

func sameCall(a, b HTTPTool) bool {
	return a.Method == b.Method && a.URL == b.URL && slices.Equal(a.Hosts, b.Hosts) && reflect.DeepEqual(a.Headers, b.Headers) &&
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
		if cmp.Or(class, registry.External) == registry.External && !slices.Contains(m.AllowedExternal, name) {
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

func readsWhose(project string) string {
	if project == AnyProject {
		return "every project"
	}
	return "project " + project
}
