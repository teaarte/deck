package manifest

import (
	"encoding/json"
	"regexp"
	"slices"
	"sort"
	"strings"
	"unicode"
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

// UnusedWords are the keys of the i18n of a manifest that no text of it or of also is, nor a value of data or a text
// its expressions write, by language.
func (m *Manifest) UnusedWords(also, values []string) []string {
	used := m.shown(also)
	for _, v := range append(values, m.literals()...) {
		used[v] = true
	}
	var out []string
	for lang, words := range m.I18n {
		for k := range words {
			if !used[k] {
				out = append(out, lang+": "+k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// Untranslated are the texts of a manifest and also that a language of its i18n has no word for, nor known, by
// language; only a text of Latin words wants one, not a ticker, a number, a sign or a text in another script.
func (m *Manifest) Untranslated(known func(lang, text string) bool, also ...string) []string {
	texts := m.shown(also)
	var out []string
	for lang, words := range m.I18n {
		for t := range texts {
			if words[t] == "" && !known(lang, t) && wordy(t) {
				out = append(out, lang+": "+t)
			}
		}
	}
	sort.Strings(out)
	return out
}

// wordy is a text of words in Latin letters alone, which a translation wants.
func wordy(t string) bool {
	return strings.IndexFunc(t, unicode.IsLower) >= 0 && strings.IndexFunc(t, func(r rune) bool { return unicode.IsLetter(r) && !unicode.Is(unicode.Latin, r) }) < 0
}

// shown are the texts of a manifest the owner sees, with also, the values of the enums of its collections and of the
// inputs of its commands and the options of its filters.
func (m *Manifest) shown(also []string) map[string]bool {
	texts := Texts(m.text)
	for _, t := range also {
		texts[t] = true
	}
	var schemas []json.RawMessage
	for _, c := range m.Collections {
		schemas = append(schemas, c.Schema)
	}
	for _, c := range m.Commands {
		schemas = append(schemas, c.Input)
	}
	for _, s := range schemas {
		for _, f := range schemaFields(s) {
			for _, v := range Enum(s, f) {
				texts[v] = true
			}
		}
	}
	var walk func(blocks []Block)
	walk = func(blocks []Block) {
		for _, b := range blocks {
			for _, o := range b.Options {
				texts[o] = true
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
	delete(texts, "")
	return texts
}

// literals are the texts the expressions of a manifest write, which a screen shows in its words.
func (m *Manifest) literals() []string {
	var v any
	_ = json.Unmarshal(m.text, &v)
	var out []string
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			for _, x := range v {
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		case string:
			out = append(out, Literals(v)...)
		}
	}
	walk(v)
	return out
}

// quoted is a text an expression writes.
var quoted = regexp.MustCompile(`'([^'\\]*)'`)

// Literals are the texts an expression writes in single quotes.
func Literals(expr string) []string {
	var out []string
	for _, m := range quoted.FindAllStringSubmatch(expr, -1) {
		out = append(out, m[1])
	}
	return out
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
