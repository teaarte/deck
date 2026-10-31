package manifest

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"
)

// JSON is v as JSON text with &, < and > as they are, indented by indent unless it is empty.
func JSON(v any, indent string) []byte {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent != "" {
		enc.SetIndent("", indent)
	}
	_ = enc.Encode(v)
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// Canonical is a manifest text in the one form deck stores: indented by two spaces, keys in their order, &, < and >
// as they are; as it is when it is not JSON.
func Canonical(text string) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(strings.TrimSpace(text)), "", "  "); err != nil {
		return text
	}
	return Unescaped(buf.String())
}

// htmlEscapes are the escapes json.Marshal writes for &, < and >.
var htmlEscapes = map[string]byte{"0026": '&', "003c": '<', "003e": '>'}

// Unescaped is JSON text with the escapes &, < and > read as &, < and >.
func Unescaped(text string) string {
	if !strings.Contains(text, `\u00`) {
		return text
	}
	var b strings.Builder
	for i := 0; i < len(text); i++ {
		c := text[i]
		if c == '\\' && i+5 < len(text) && text[i+1] == 'u' {
			if r, ok := htmlEscapes[strings.ToLower(text[i+2:i+6])]; ok {
				b.WriteByte(r)
				i += 5
				continue
			}
		}
		b.WriteByte(c)
		if c == '\\' && i+1 < len(text) {
			i++
			b.WriteByte(text[i])
		}
	}
	return b.String()
}

// object is a JSON object that keeps the order of its keys.
type object struct {
	keys []string
	vals map[string]any
}

// Ordered is v as JSON with the keys of each object in the order the same object has in was, new keys after them
// sorted, so a changed manifest keeps the order of its fields and of the properties of its schemas.
func Ordered(v any, was []byte) []byte {
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
			out = append(append(append(out, JSON(k, "")...), ':'), Ordered(x, old[k])...)
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
			out = append(out, Ordered(x, w)...)
		}
		return append(out, ']')
	}
	return JSON(v, "")
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

// PropertyOrder lists the properties of an object schema in the order the schema writes them.
func PropertyOrder(schema json.RawMessage) []string {
	var top map[string]json.RawMessage
	if json.Unmarshal(schema, &top) != nil || top["properties"] == nil {
		return []string{}
	}
	return append([]string{}, keysOf(top["properties"])...)
}

func decodeOrdered(raw []byte) (any, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	v, err := decodeValue(d)
	if err != nil {
		return nil, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, fmt.Errorf("manifest: text after the JSON value")
	}
	return v, nil
}

func decodeValue(d *json.Decoder) (any, error) {
	t, err := d.Token()
	if err != nil {
		return nil, err
	}
	switch t {
	case json.Delim('{'):
		o := &object{vals: map[string]any{}}
		for d.More() {
			k, err := d.Token()
			if err != nil {
				return nil, err
			}
			v, err := decodeValue(d)
			if err != nil {
				return nil, err
			}
			key := k.(string)
			if _, dup := o.vals[key]; !dup {
				o.keys = append(o.keys, key)
			}
			o.vals[key] = v
		}
		_, err := d.Token()
		return o, err
	case json.Delim('['):
		a := []any{}
		for d.More() {
			v, err := decodeValue(d)
			if err != nil {
				return nil, err
			}
			a = append(a, v)
		}
		_, err := d.Token()
		return a, err
	}
	return t, nil
}

// encodeOrdered writes v as Canonical does: two spaces a level.
func encodeOrdered(w *bytes.Buffer, v any, indent string) {
	in := indent + "  "
	switch x := v.(type) {
	case *object:
		if len(x.keys) == 0 {
			w.WriteString("{}")
			return
		}
		w.WriteString("{\n")
		for i, k := range x.keys {
			b, _ := json.Marshal(k)
			w.WriteString(in)
			w.Write(b)
			w.WriteString(": ")
			encodeOrdered(w, x.vals[k], in)
			if i < len(x.keys)-1 {
				w.WriteByte(',')
			}
			w.WriteByte('\n')
		}
		w.WriteString(indent + "}")
	case []any:
		if len(x) == 0 {
			w.WriteString("[]")
			return
		}
		w.WriteString("[\n")
		for i, e := range x {
			w.WriteString(in)
			encodeOrdered(w, e, in)
			if i < len(x)-1 {
				w.WriteByte(',')
			}
			w.WriteByte('\n')
		}
		w.WriteString(indent + "]")
	case json.Number:
		w.WriteString(x.String())
	default:
		b, _ := json.Marshal(x)
		w.Write(b)
	}
}
