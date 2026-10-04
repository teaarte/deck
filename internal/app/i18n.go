package app

import (
	_ "embed"
	"maps"

	"deck2/internal/manifest"
)

// words are the translations into a language of deck's screens and then of the manifest, which wins.
func words(lang string, m *manifest.Manifest) map[string]string {
	if lang == "" {
		return nil
	}
	out := maps.Clone(shipped().I18n[lang])
	if m != nil {
		if out == nil {
			out = map[string]string{}
		}
		maps.Copy(out, m.I18n[lang])
	}
	return out
}

// tr is a text in a language by words, or the text itself.
func tr(ws map[string]string, s string) string {
	if t := ws[s]; t != "" {
		return t
	}
	return s
}
