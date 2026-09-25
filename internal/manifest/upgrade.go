package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

var upgrades = map[int]func(m map[string]any){
	1: func(m map[string]any) {
		delete(m, "description")
		dropEmpty(m, "collections", "commands", "triggers", "screens")
		for _, c := range items(m["collections"]) {
			rename(c, "title", "display")
			dropEmpty(c, "display", "description")
		}
		for _, c := range items(m["commands"]) {
			rename(c, "result_schema", "result")
			dropEmpty(c, "result")
		}
		for _, s := range items(m["screens"]) {
			for _, b := range items(s["blocks"]) {
				dropEmpty(b, "title", "source", "columns", "actions")
			}
		}
	},
	2: func(m map[string]any) {
		delete(m, "description")
	},
	3: func(m map[string]any) {
		board, _ := m["board"].(map[string]any)
		if board == nil {
			return
		}
		names := map[string]string{}
		taken := map[string]bool{}
		lanes := items(board["lanes"])
		runRoles := 0
		for _, l := range lanes {
			if slices.Contains(RunRoles, fmt.Sprint(l["role"])) {
				runRoles++
			}
		}
		for i, l := range lanes {
			if runRoles < len(RunRoles) && slices.Contains(RunRoles, fmt.Sprint(l["role"])) {
				delete(l, "role")
			}
			old, _ := l["name"].(string)
			role, _ := l["role"].(string)
			name := laneName(old, role, i, taken)
			taken[name] = true
			names[old] = name
			l["title"], l["name"] = old, name
		}
		for _, t := range items(m["triggers"]) {
			if lane, ok := t["lane"].(string); ok && names[lane] != "" {
				t["lane"] = names[lane]
			}
		}
	},
}

var laneSlug = regexp.MustCompile(`[^a-z0-9]+`)

// laneName makes a stable lane name from a human one: a slug, else the role, else lane-<n>.
func laneName(title, role string, i int, taken map[string]bool) string {
	name := strings.Trim(laneSlug.ReplaceAllString(strings.ToLower(title), "-"), "-")
	if name == "" || name[0] < 'a' || name[0] > 'z' || len(name) > 40 || taken[name] {
		name = role
	}
	if name == "" || taken[name] {
		name = fmt.Sprintf("lane-%d", i+1)
	}
	for n := 2; taken[name]; n++ {
		name = fmt.Sprintf("lane-%d-%d", i+1, n)
	}
	return name
}

// dropEmpty removes keys whose value is null, an empty string or an empty list.
func dropEmpty(o map[string]any, keys ...string) {
	for _, k := range keys {
		v, ok := o[k]
		if !ok {
			continue
		}
		switch x := v.(type) {
		case nil:
			delete(o, k)
		case string:
			if x == "" {
				delete(o, k)
			}
		case []any:
			if len(x) == 0 {
				delete(o, k)
			}
		}
	}
}

// Upgrade raises a manifest of an older format to the current one; changed is false when it was current.
func Upgrade(raw []byte) (out []byte, changed bool, err error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, false, fmt.Errorf("manifest: %w", err)
	}
	n, _ := m["version"].(json.Number)
	v, _ := n.Int64()
	version := int(v)
	if version == FormatVersion {
		return raw, false, nil
	}
	if version < 1 || version > FormatVersion {
		return nil, false, fmt.Errorf("manifest: version must be %d, got %v", FormatVersion, m["version"])
	}
	for ; version < FormatVersion; version++ {
		upgrades[version](m)
	}
	m["version"] = FormatVersion
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, false, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), true, nil
}

func items(v any) []map[string]any {
	list, _ := v.([]any)
	out := make([]map[string]any, 0, len(list))
	for _, e := range list {
		if o, ok := e.(map[string]any); ok {
			out = append(out, o)
		}
	}
	return out
}

func rename(o map[string]any, from, to string) {
	if v, ok := o[from]; ok {
		o[to] = v
		delete(o, from)
	}
}

// LegacyDescription is the project description a manifest before format 3 carried; it moves to the md prompt.
func LegacyDescription(raw []byte) string {
	var m struct {
		Version     int    `json:"version"`
		Description string `json:"description"`
	}
	if json.Unmarshal(raw, &m) != nil || m.Version >= 3 {
		return ""
	}
	return m.Description
}
