package manifest

import (
	"bytes"
	"encoding/json"
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
