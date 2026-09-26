package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"sort"
)

var secretRef = regexp.MustCompile(`\$secret\.([A-Za-z0-9_.-]+)`)

var classRank = map[string]int{"internal": 0, "reversible": 1, "external": 2}

// Widens lists what next grants that prev does not: autonomy, allowed external actions, domains, secrets,
// milder classes, MCP servers deck would start, reads, and changed HTTP tools that run without asking.
func Widens(prev, next *Manifest) []string {
	var out []string
	add := func(format string, a ...any) { out = append(out, fmt.Sprintf(format, a...)) }
	if next.Autonomy == "auto" && prev.Autonomy != "auto" {
		add("autonomy: auto")
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
	for _, s := range added(secrets(prev), secrets(next)) {
		add("secret: %s", s)
	}
	for _, c := range next.Commands {
		if old := prev.Command(c.Name); old != nil && classRank[c.Class] < classRank[old.Class] {
			add("command %s: class %s instead of %s", c.Name, c.Class, old.Class)
		}
	}
	prevHTTP := map[string]HTTPTool{}
	for _, t := range prev.Tools.HTTP {
		prevHTTP[t.Name] = t
	}
	for _, t := range next.Tools.HTTP {
		old, ok := prevHTTP[t.Name]
		switch {
		case !ok && t.Class != "external":
			add("tool %s: new with class %s", t.Name, t.Class)
		case ok && classRank[t.Class] < classRank[old.Class]:
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
		}
		for _, tool := range sortedKeys(s.Classes) {
			was, known := old.Classes[tool]
			if !known {
				was = "external"
			}
			if classRank[s.Classes[tool]] < classRank[was] {
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

func secrets(m *Manifest) []string {
	b, _ := json.Marshal(m.Tools)
	var out []string
	for _, s := range secretRef.FindAllSubmatch(b, -1) {
		out = append(out, string(s[1]))
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
