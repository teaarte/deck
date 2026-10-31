package harness

import (
	"encoding/json"
	"strings"
	"testing"
)

func MustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}

func Jsonl(recs ...map[string]any) string {
	var b strings.Builder
	for _, r := range recs {
		line, _ := json.Marshal(r)
		b.Write(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// JsonLines decodes a JSON value per line.
func JsonLines[T any](text string) []T {
	var out []T
	for _, l := range strings.Split(strings.TrimSpace(text), "\n") {
		if l == "" {
			continue
		}
		var v T
		_ = json.Unmarshal([]byte(l), &v)
		out = append(out, v)
	}
	return out
}

func quoteJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

func roundTrip(v any) any {
	b, _ := json.Marshal(v)
	var out any
	_ = json.Unmarshal(b, &out)
	return out
}

func BlockData[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return v
}

func Merge(a, b map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func Keys(m map[string]map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
