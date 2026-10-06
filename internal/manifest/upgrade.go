package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"regexp"
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
	7: func(m map[string]any) {
		fromRecord(m["screens"])
	},
	8: func(m map[string]any) {
		delete(m, "autonomy")
		delete(m, "board")
		if list, ok := m["triggers"].([]any); ok {
			m["triggers"] = slices.DeleteFunc(list, func(t any) bool { tm, _ := t.(map[string]any); return tm["lane"] != nil })
		}
		dropBoard(m["screens"])
	},
	6: func(m map[string]any) {
		for _, c := range items(m["commands"]) {
			r, ok := c["result"].(map[string]any)
			switch {
			case !ok:
			case r["type"] == nil:
				c["result"] = map[string]any{"anyOf": []any{r, map[string]any{"type": "null"}}}
			default:
				r["type"] = withNull(r["type"])
			}
		}
	},
}

// dropBoard drops what format 9 has no more from the screens: the board block and the cell format label.
func dropBoard(v any) {
	switch v := v.(type) {
	case map[string]any:
		if v["format"] == "label" {
			delete(v, "format")
		}
		if blocks, ok := v["blocks"].([]any); ok {
			v["blocks"] = slices.DeleteFunc(blocks, func(b any) bool { bm, _ := b.(map[string]any); return bm["type"] == "board" })
		}
		for _, x := range v {
			dropBoard(x)
		}
	case []any:
		for _, x := range v {
			dropBoard(x)
		}
	}
}

// withNull is the type of a result schema that takes null too, as every result did before format 7.
func withNull(t any) any {
	switch t := t.(type) {
	case string:
		if t == "null" {
			return t
		}
		return []any{t, "null"}
	case []any:
		if !slices.Contains(t, any("null")) {
			return append(t, "null")
		}
	}
	return t
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

var identifier = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// keywords are names an expression reads as itself, not as a field.
var keywords = []string{"in", "not", "and", "or", "matches", "contains", "startsWith", "endsWith", "let", "true", "false", "nil"}

// fromRecord turns the $record.<field> values of format 7 into =<field>, $record.id into =_id and a field that is no
// name of an expression into =$env["<field>"], in place.
func fromRecord(v any) any {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			v[k] = fromRecord(x)
		}
	case []any:
		for i, x := range v {
			v[i] = fromRecord(x)
		}
	case string:
		if f, ok := strings.CutPrefix(v, "$record."); ok {
			switch {
			case f == "id":
				return "=_id"
			case identifier.MatchString(f) && !slices.Contains(keywords, f):
				return "=" + f
			}
			q, _ := json.Marshal(f)
			return "=$env[" + string(q) + "]"
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
		got := fmt.Sprint(m["version"])
		if t, ok := m["version"].(string); ok {
			got = fmt.Sprintf("the text %q", t)
		}
		return nil, false, fmt.Errorf("manifest: version must be the number %d, got %s", FormatVersion, got)
	}
	for ; version < FormatVersion; version++ {
		upgrades[version](m)
	}
	m["version"] = FormatVersion
	var buf bytes.Buffer
	if err := json.Indent(&buf, ordered(m, raw), "", "  "); err != nil {
		return nil, false, err
	}
	return buf.Bytes(), true, nil
}

// ordered is v as JSON with the keys of each object in the order the same object has in was, new keys after them
// sorted, so a raised manifest keeps the order of its fields and of the properties of its schemas.
func ordered(v any, was []byte) []byte {
	switch v := v.(type) {
	case map[string]any:
		var old map[string]json.RawMessage
		_ = json.Unmarshal(was, &old)
		keys := keysOf(was)
		for _, k := range slices.Sorted(maps.Keys(v)) {
			if !slices.Contains(keys, k) {
				keys = append(keys, k)
			}
		}
		out := []byte{'{'}
		for _, k := range keys {
			x, ok := v[k]
			if !ok {
				continue
			}
			if len(out) > 1 {
				out = append(out, ',')
			}
			name, _ := json.Marshal(k)
			out = append(append(append(out, name...), ':'), ordered(x, old[k])...)
		}
		return append(out, '}')
	case []any:
		var old []json.RawMessage
		_ = json.Unmarshal(was, &old)
		out := []byte{'['}
		for i, x := range v {
			if i > 0 {
				out = append(out, ',')
			}
			var w []byte
			if i < len(old) {
				w = old[i]
			}
			out = append(out, ordered(x, w)...)
		}
		return append(out, ']')
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
	return bytes.TrimRight(buf.Bytes(), "\n")
}

// keysOf are the keys of a JSON object in the order they are written.
func keysOf(raw []byte) []string {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return nil
	}
	var keys []string
	for dec.More() {
		t, err := dec.Token()
		k, ok := t.(string)
		if err != nil || !ok {
			return keys
		}
		keys = append(keys, k)
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return keys
		}
	}
	return keys
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
