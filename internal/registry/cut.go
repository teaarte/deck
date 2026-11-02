package registry

import (
	"bytes"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// shape is the one list of rows of a result object, the other fields of the object and the fields of a row.
type shape struct {
	rows     string
	top, row []string
	// names are the fields of the result's type: of a row of a list, else of the object, with its list and its rows.
	names []string
}

func shapeOf(t reflect.Type) shape {
	sch := resultSchema(t)
	if items, _ := sch["items"].(map[string]any); items != nil {
		props, _ := items["properties"].(map[string]any)
		return shape{names: slices.Collect(maps.Keys(props))}
	}
	s, _ := sch["properties"].(map[string]any)
	sh := shape{names: slices.Collect(maps.Keys(s))}
	lists := 0
	for name, p := range s {
		if items, _ := p.(map[string]any)["items"].(map[string]any); items["type"] == "object" {
			lists++
			sh.rows = name
			props, _ := items["properties"].(map[string]any)
			sh.row = slices.Collect(maps.Keys(props))
		} else {
			sh.top = append(sh.top, name)
		}
	}
	if lists != 1 {
		return shape{names: sh.names}
	}
	sh.names = append(sh.names, sh.row...)
	return sh
}

// unknown are the fields asked whose first name no field of the result's type has nor any row of the result v, with
// the names there are; none when the result has no rows to tell by.
func (sh shape) unknown(v any, asked []string) (bad, names []string) {
	rows := []any{v}
	if l, ok := v.([]any); ok {
		rows = l
	} else if m, ok := v.(map[string]any); ok && sh.rows != "" {
		l, _ := m[sh.rows].([]any)
		// Rows of no type tell their fields only by themselves.
		if len(l) == 0 && len(sh.row) == 0 {
			return nil, nil
		}
		rows = append(rows, l...)
	}
	known := map[string]bool{}
	for _, n := range sh.names {
		known[n] = true
	}
	objects := false
	for _, r := range rows {
		if m, ok := r.(map[string]any); ok {
			objects = true
			for k := range m {
				known[k] = true
			}
		}
	}
	if !objects {
		return nil, nil
	}
	for _, f := range asked {
		if name, _, _ := strings.Cut(f, "."); !known[name] && !slices.Contains(bad, name) {
			bad = append(bad, name)
		}
	}
	return bad, slices.Sorted(maps.Keys(known))
}

// fields are those asked of the rows of an object with one list, the rest of it kept, unless every one asked is
// the object's own and some is not a field of a row.
func (sh shape) fields(fields []string) []string {
	if sh.rows == "" {
		return fields
	}
	own, rows := true, true
	for _, f := range fields {
		name, _, _ := strings.Cut(f, ".")
		own = own && (name == sh.rows || slices.Contains(sh.top, name))
		rows = rows && slices.Contains(sh.row, name)
	}
	if own && !rows {
		return fields
	}
	out := slices.Clone(sh.top)
	for _, f := range fields {
		out = append(out, sh.rows+"."+f)
	}
	return out
}

// plain is a result as JSON values.
func plain(out any) (any, error) {
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var v any
	err = d.Decode(&v)
	return v, err
}

// cut keeps only the named fields of a result v, of every object of a list.
func cut(v any, fields []string) any {
	keep := tree{}
	for _, f := range fields {
		t := keep
		for name := range strings.SplitSeq(f, ".") {
			if t[name] == nil {
				t[name] = tree{}
			}
			t = t[name]
		}
	}
	return keep.cut(v)
}

type tree map[string]tree

func (t tree) cut(v any) any {
	switch v := v.(type) {
	case []any:
		for i := range v {
			v[i] = t.cut(v[i])
		}
	case string:
		if s, ok := t.text(v); ok {
			return s
		}
	case map[string]any:
		out := map[string]any{}
		for name, sub := range t {
			if x, ok := v[name]; ok && len(sub) > 0 {
				out[name] = sub.cut(x)
			} else if ok {
				out[name] = x
			}
		}
		return out
	}
	return v
}

// text is the parts of a JSON text named by the tree, in the order and indentation of the text; ok is false when
// the text is no JSON object or array.
func (t tree) text(s string) (string, bool) {
	raw := bytes.TrimSpace([]byte(s))
	if len(t) == 0 || len(raw) == 0 || raw[0] != '{' && raw[0] != '[' || !json.Valid(raw) {
		return s, false
	}
	var buf bytes.Buffer
	if json.Indent(&buf, t.keep(raw), "", "  ") != nil {
		return s, false
	}
	return buf.String(), true
}

// keep is the parts of a JSON value named by the tree, the kept keys in their order; the name of an item of a list
// picks the items of that name, and keys that name no item nor a field of one pick none.
func (t tree) keep(raw json.RawMessage) json.RawMessage {
	raw = bytes.TrimSpace(raw)
	if len(t) == 0 || len(raw) == 0 {
		return raw
	}
	var b bytes.Buffer
	switch raw[0] {
	case '{':
		d := json.NewDecoder(bytes.NewReader(raw))
		_, _ = d.Token()
		b.WriteByte('{')
		for d.More() {
			k, _ := d.Token()
			var v json.RawMessage
			if d.Decode(&v) != nil {
				return raw
			}
			name, _ := k.(string)
			sub, ok := t[name]
			if !ok {
				continue
			}
			if b.Len() > 1 {
				b.WriteByte(',')
			}
			e := json.NewEncoder(&b)
			e.SetEscapeHTML(false)
			_ = e.Encode(name)
			b.WriteByte(':')
			b.Write(sub.keep(v))
		}
		b.WriteByte('}')
	case '[':
		var items []map[string]json.RawMessage
		if json.Unmarshal(raw, &items) != nil {
			var list []json.RawMessage
			if json.Unmarshal(raw, &list) != nil {
				return raw
			}
			return t.list(list, func(json.RawMessage) (tree, bool) { return t, true })
		}
		named, fields := tree{}, false
		for k, sub := range t {
			if slices.ContainsFunc(items, func(it map[string]json.RawMessage) bool { return string(it["name"]) == strconv.Quote(k) }) {
				named[k] = sub
			}
			fields = fields || slices.ContainsFunc(items, func(it map[string]json.RawMessage) bool { _, ok := it[k]; return ok })
		}
		var list []json.RawMessage
		_ = json.Unmarshal(raw, &list)
		return t.list(list, func(it json.RawMessage) (tree, bool) {
			if len(named) == 0 {
				return t, fields
			}
			var n struct{ Name *string }
			if json.Unmarshal(it, &n) != nil || n.Name == nil {
				return nil, false
			}
			sub, ok := named[*n.Name]
			return sub, ok
		})
	default:
		return raw
	}
	return b.Bytes()
}

// list is the items of a list each picks, cut by the tree it gives.
func (t tree) list(items []json.RawMessage, pick func(json.RawMessage) (tree, bool)) json.RawMessage {
	b := []byte{'['}
	for _, it := range items {
		sub, ok := pick(it)
		if !ok {
			continue
		}
		if len(b) > 1 {
			b = append(b, ',')
		}
		b = append(b, sub.keep(it)...)
	}
	return append(b, ']')
}
