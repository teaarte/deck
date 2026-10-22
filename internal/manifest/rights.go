package manifest

import (
	"bytes"
	"cmp"
	"encoding/json"
	"maps"
	"net"
	"net/netip"
	"reflect"
	"slices"
	"strings"
)

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
	host := d
	if h, _, err := net.SplitHostPort(d); err == nil {
		host = strings.Trim(h, "[]")
	}
	_, prefixErr := netip.ParsePrefix(d)
	_, ipErr := netip.ParseAddr(strings.Trim(host, "[]"))
	return prefixErr != nil && ipErr != nil && slices.Contains(granted, "*")
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

func readsWhose(project string) string {
	if project == AnyProject {
		return "every project"
	}
	return "project " + project
}
