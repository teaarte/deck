package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// upgrades raise a manifest one format up; a format older than the first step here is refused.
var upgrades = map[int]func(m map[string]any){
	4: func(m map[string]any) {
		delete(m, "master_events")
		if list, ok := m["allowed_external"].([]any); ok {
			m["allowed_external"] = slices.DeleteFunc(list, func(n any) bool { return strings.HasPrefix(fmt.Sprint(n), "deck.browser_") })
			dropEmpty(m, "allowed_external")
		}
		for _, c := range items(m["commands"]) {
			reqs, _ := c["requires"].([]any)
			c["requires"] = slices.DeleteFunc(reqs, func(r any) bool { return r == "live_input" || r == "structured_output" })
			dropEmpty(c, "requires")
		}
	},
	5: func(m map[string]any) {
		for _, s := range items(m["screens"]) {
			params, _ := s["params"].(map[string]any)
			if params == nil {
				params = map[string]any{}
			}
			s["blocks"] = fromParam(s["blocks"], params)
			if len(params) > 0 {
				s["params"] = params
			}
		}
	},
}

// fromParam turns the $param.<name> values of format 5 into =param.<name>, declaring each name as text, and
// drops menu of an actions block.
func fromParam(v any, params map[string]any) any {
	switch v := v.(type) {
	case map[string]any:
		if v["type"] == "actions" {
			delete(v, "menu")
		}
		for k, x := range v {
			v[k] = fromParam(x, params)
		}
	case []any:
		for i, x := range v {
			v[i] = fromParam(x, params)
		}
	case string:
		if name, ok := strings.CutPrefix(v, "$param."); ok {
			if _, declared := params[name]; !declared {
				params[name] = "string"
			}
			return "=param." + name
		}
	}
	return v
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
		return nil, false, fmt.Errorf("manifest: %w", where(raw, err))
	}
	n, _ := m["version"].(json.Number)
	v, _ := n.Int64()
	version := int(v)
	if version == FormatVersion {
		return raw, false, nil
	}
	if version < 4 || version > FormatVersion {
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

// where adds to a JSON error the line and column it stopped at and the text around.
func where(raw []byte, err error) error {
	var off int64
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn):
		off = syn.Offset
	case errors.As(err, &typ):
		off = typ.Offset
	default:
		return err
	}
	off = min(off, int64(len(raw)))
	before := raw[:off]
	line := bytes.Count(before, []byte("\n")) + 1
	col := int(off) - bytes.LastIndexByte(before, '\n')
	from, to := max(0, int(off)-40), min(len(raw), int(off)+20)
	return fmt.Errorf("%w at line %d, column %d: %q", err, line, col, raw[from:to])
}
