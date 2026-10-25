package manifest

import (
	"encoding/json"
	"slices"
	"sort"
	"strings"
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
					// A label of an expression is no text: what it says comes from the item.
					if !strings.HasPrefix(s, "=") {
						out[s] = true
					}
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

// UnusedWords are the keys of the i18n of a manifest that no text of it or of also is, by language.
func (m *Manifest) UnusedWords(also ...string) []string {
	texts := m.shown(also)
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

// Untranslated are the texts of a manifest and also that a language of its i18n has no word for, nor known, by
// language.
func (m *Manifest) Untranslated(known func(lang, text string) bool, also ...string) []string {
	texts := m.shown(also)
	var out []string
	for lang, words := range m.I18n {
		for t := range texts {
			if words[t] == "" && !known(lang, t) {
				out = append(out, lang+": "+t)
			}
		}
	}
	sort.Strings(out)
	return out
}

// shown are the texts of a manifest the owner sees, with also and the values of the enums of its collections.
func (m *Manifest) shown(also []string) map[string]bool {
	texts := Texts(m.text)
	for _, t := range also {
		texts[t] = true
	}
	for _, c := range m.Collections {
		for _, f := range schemaFields(c.Schema) {
			for _, v := range Enum(c.Schema, f) {
				texts[v] = true
			}
		}
	}
	delete(texts, "")
	return texts
}

// Filtered are the fields of each collection a block of a screen filters by.
func (m *Manifest) Filtered() map[string][]string {
	out := map[string][]string{}
	var walk func(blocks []Block)
	walk = func(blocks []Block) {
		for _, b := range blocks {
			if b.Source != nil && b.Source.Collection != "" {
				for _, f := range append(slices.Clone(b.Filter), b.Field) {
					if f == "" {
						continue
					}
					if !slices.Contains(out[b.Source.Collection], f) {
						out[b.Source.Collection] = append(out[b.Source.Collection], f)
					}
				}
			}
			walk(b.Blocks)
			for _, t := range b.Tabs {
				walk(t.Blocks)
			}
		}
	}
	for _, s := range m.Screens {
		walk(s.All())
	}
	return out
}
