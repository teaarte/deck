package manifest

import (
	"encoding/json"
	"slices"
	"sort"
)

// textKeys name the texts the owner sees; the values under notTexts are expressions and inputs, not texts.
var (
	textKeys = []string{"title", "label", "description", "empty"}
	notTexts = []string{"item", "source", "choices", "values", "params", "i18n"}
)

// Texts are the texts of a manifest the owner sees, which its i18n translates; the names of fields are texts too,
// shown where a field has no title.
func Texts(raw []byte) map[string]bool {
	var v any
	_ = json.Unmarshal(raw, &v)
	out := map[string]bool{}
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if props, ok := v["properties"].(map[string]any); ok {
				for name := range props {
					out[name] = true
				}
			}
			for k, x := range v {
				if s, ok := x.(string); ok && slices.Contains(textKeys, k) {
					out[s] = true
				} else if !slices.Contains(notTexts, k) {
					walk(x)
				}
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(v)
	return out
}

// Translate gives a decoded JSON value with each of its texts put through tr.
func Translate(v any, tr func(string) string) any {
	switch v := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for k, x := range v {
			switch s, ok := x.(string); {
			case ok && slices.Contains(textKeys, k):
				out[k] = tr(s)
			case slices.Contains(notTexts, k):
				out[k] = x
			default:
				out[k] = Translate(x, tr)
			}
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, x := range v {
			out[i] = Translate(x, tr)
		}
		return out
	}
	return v
}

// UnusedWords are the keys of the i18n of a manifest that no text of it is, by language.
func (m *Manifest) UnusedWords() []string {
	texts := Texts(m.text)
	var out []string
	for lang, words := range m.I18n {
		for k := range words {
			if !texts[k] {
				out = append(out, lang+": "+k)
			}
		}
	}
	sort.Strings(out)
	return out
}
