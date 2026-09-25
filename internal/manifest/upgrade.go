package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
)

var upgrades = map[int]func(m map[string]any){
	1: func(m map[string]any) {
		dropEmpty(m, "description", "collections", "commands", "triggers", "screens")
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
