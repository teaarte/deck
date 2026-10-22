package manifest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
)

// checkVersion refuses a manifest that is no JSON object or not of the current format.
func checkVersion(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return fmt.Errorf("manifest: %w", where(raw, err))
	}
	if n, _ := m["version"].(json.Number); n.String() == fmt.Sprint(FormatVersion) {
		return nil
	}
	got := fmt.Sprint(m["version"])
	if t, ok := m["version"].(string); ok {
		got = fmt.Sprintf("the text %q", t)
	}
	return fmt.Errorf("manifest: version must be the number %d, got %s", FormatVersion, got)
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
