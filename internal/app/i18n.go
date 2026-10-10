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

// phrases are deck's own words in a language, for what read commands say.
func phrases(lang string) map[string]string { return shipped().Words[lang] }
